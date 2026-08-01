package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
)

type Repository struct {
	pool    *pgxpool.Pool
	scopeID string
	memory  *memory.Repository
}

func NewRepository(ctx context.Context, pool *pgxpool.Pool, scopeID string) (*Repository, error) {
	if pool == nil || scopeID == "" {
		return nil, fmt.Errorf("create Page Wiki postgres repository: pool and scope are required")
	}
	repository := &Repository{pool: pool, scopeID: scopeID, memory: memory.NewRepository()}
	if err := repository.hydrate(ctx); err != nil {
		return nil, err
	}
	return repository, nil
}

func (r *Repository) hydrate(ctx context.Context) error {
	legacyPages, err := r.hydrateLegacyWiki(ctx)
	if err != nil {
		return fmt.Errorf("hydrate legacy LLM Wiki: %w", err)
	}
	if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_source_revisions
WHERE scope_id = $1 ORDER BY created_at, source_revision_id`, func(payload []byte) error {
		var revision pagewiki.SourceRevision
		if err := json.Unmarshal(payload, &revision); err != nil {
			return err
		}
		return r.memory.SaveSourceRevision(ctx, revision)
	}); err != nil {
		return fmt.Errorf("hydrate Page Wiki sources: %w", err)
	}
	if err := r.hydratePublications(ctx, legacyPages); err != nil {
		return fmt.Errorf("hydrate Page Wiki publications: %w", err)
	}
	if err := r.hydrateLifecycleEvents(ctx); err != nil {
		return fmt.Errorf("hydrate Page Wiki lifecycle events: %w", err)
	}
	if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_maintenance_runs
WHERE scope_id = $1 ORDER BY created_at, run_id`, func(payload []byte) error {
		var run pagewiki.MaintenanceRun
		if err := json.Unmarshal(payload, &run); err != nil {
			return err
		}
		return r.memory.SaveMaintenanceRun(ctx, run)
	}); err != nil {
		return fmt.Errorf("hydrate Page Wiki runs: %w", err)
	}
	if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_curation_runs
WHERE scope_id = $1 ORDER BY created_at, run_id`, func(payload []byte) error {
		var run pagewiki.CurationRun
		if err := json.Unmarshal(payload, &run); err != nil {
			return err
		}
		return r.memory.SaveCurationRun(ctx, run)
	}); err != nil {
		return fmt.Errorf("hydrate Page Wiki curation runs: %w", err)
	}
	if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_page_embeddings
WHERE scope_id = $1`, func(payload []byte) error {
		var embedding pagewiki.PageEmbedding
		if err := json.Unmarshal(payload, &embedding); err != nil {
			return err
		}
		return r.memory.SavePageEmbedding(ctx, embedding)
	}); err != nil {
		return fmt.Errorf("hydrate Page Wiki page embeddings: %w", err)
	}
	if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_topic_trees
WHERE scope_id = $1`, func(payload []byte) error {
		var tree pagewiki.TopicTree
		if err := json.Unmarshal(payload, &tree); err != nil {
			return err
		}
		return r.memory.ReplaceTopicTree(ctx, tree)
	}); err != nil {
		return fmt.Errorf("hydrate Page Wiki topic tree: %w", err)
	}
	return nil
}

// hydratePublications replays the publication log. A payload is either a
// single PagePublication or a JSON array written by PublishPages; batches must
// replay atomically because members may link to each other.
func (r *Repository) hydratePublications(ctx context.Context, legacyPages int) error {
	return r.loadRows(ctx, `
SELECT payload FROM pagewiki_publications
WHERE scope_id = $1 ORDER BY ordinal`, func(payload []byte) error {
		var batch []pagewiki.PagePublication
		if err := json.Unmarshal(payload, &batch); err == nil && len(batch) > 0 {
			if legacyPages > 0 {
				filtered := make([]pagewiki.PagePublication, 0, len(batch))
				for _, publication := range batch {
					if !isSessionPublication(publication) {
						filtered = append(filtered, publication)
					}
				}
				batch = filtered
			}
			if len(batch) == 0 {
				return nil
			}
			return r.memory.PublishPages(ctx, batch)
		}
		var publication pagewiki.PagePublication
		if err := json.Unmarshal(payload, &publication); err != nil {
			return err
		}
		if legacyPages > 0 && isSessionPublication(publication) {
			return nil
		}
		return r.memory.PublishPage(ctx, publication)
	})
}

// hydrateLifecycleEvents replays the retire log after publications have been
// applied. A retire event whose page was later revived by a newer publication
// (already applied above) no longer matches its ExpectedBaseRevisionID, so
// the CAS check in memory.RetirePage rejects it; that rejection is expected
// and is swallowed here rather than treated as a hydration failure.
func (r *Repository) hydrateLifecycleEvents(ctx context.Context) error {
	return r.loadRows(ctx, `
SELECT payload FROM pagewiki_page_lifecycle
WHERE scope_id = $1 ORDER BY ordinal`, func(payload []byte) error {
		var request pagewiki.RetireRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			return err
		}
		err := r.memory.RetirePage(ctx, request)
		if err == nil || errors.Is(err, pagewiki.ErrRevisionConflict) || errors.Is(err, pagewiki.ErrNotFound) {
			return nil
		}
		return err
	})
}

func isSessionPublication(publication pagewiki.PagePublication) bool {
	for _, topic := range publication.Topics {
		if topic.ParentID == "" && topic.Title == "Sessions" {
			return true
		}
	}
	return false
}

func (r *Repository) loadRows(
	ctx context.Context,
	query string,
	load func([]byte) error,
) error {
	rows, err := r.pool.Query(ctx, query, r.scopeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		if err := load(payload); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *Repository) SaveSourceRevision(ctx context.Context, revision pagewiki.SourceRevision) error {
	if err := r.memory.SaveSourceRevision(ctx, revision); err != nil {
		return err
	}
	return r.insertJSON(ctx, `
INSERT INTO pagewiki_source_revisions (scope_id, source_revision_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, source_revision_id) DO NOTHING`, revision.ID, revision)
}

func (r *Repository) PublishPage(ctx context.Context, publication pagewiki.PagePublication) error {
	if err := r.memory.PublishPage(ctx, publication); err != nil {
		return err
	}
	return r.insertJSON(ctx, `
INSERT INTO pagewiki_publications (scope_id, page_revision_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, page_revision_id) DO NOTHING`, publication.Revision.ID, publication)
}

// PublishPages stores a multi-publication batch as one payload row so
// hydration replays it atomically: batch members may link to each other, and
// replaying them one row at a time would fail link validation.
func (r *Repository) PublishPages(ctx context.Context, publications []pagewiki.PagePublication) error {
	if len(publications) == 1 {
		return r.PublishPage(ctx, publications[0])
	}
	if err := r.memory.PublishPages(ctx, publications); err != nil {
		return err
	}
	return r.insertJSON(ctx, `
INSERT INTO pagewiki_publications (scope_id, page_revision_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, page_revision_id) DO NOTHING`,
		publications[len(publications)-1].Revision.ID, publications)
}

func (r *Repository) SaveMaintenanceRun(ctx context.Context, run pagewiki.MaintenanceRun) error {
	if err := r.memory.SaveMaintenanceRun(ctx, run); err != nil {
		return err
	}
	// The memory layer above has already enforced run immutability rules;
	// updating on conflict lets a retried run replace its failed predecessor.
	return r.insertJSON(ctx, `
INSERT INTO pagewiki_maintenance_runs (scope_id, run_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, run_id) DO UPDATE
SET payload = EXCLUDED.payload`, run.ID, run)
}

func (r *Repository) TopicTree(ctx context.Context) (pagewiki.TopicTree, error) {
	return r.memory.TopicTree(ctx)
}

func (r *Repository) ReplaceTopicTree(ctx context.Context, tree pagewiki.TopicTree) error {
	if err := r.memory.ReplaceTopicTree(ctx, tree); err != nil {
		return err
	}
	payload, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("marshal Page Wiki topic tree: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO pagewiki_topic_trees (scope_id, payload)
VALUES ($1, $2)
ON CONFLICT (scope_id) DO UPDATE
SET payload = EXCLUDED.payload, updated_at = NOW()`, r.scopeID, payload); err != nil {
		return fmt.Errorf("persist Page Wiki topic tree: %w", err)
	}
	return nil
}

func (r *Repository) GenerationSettings(ctx context.Context) (pagewiki.GenerationDirectives, error) {
	var directives pagewiki.GenerationDirectives
	err := r.pool.QueryRow(ctx, `
SELECT language, custom_instructions
FROM pagewiki_generation_settings
WHERE scope_id = $1`, r.scopeID).Scan(&directives.Language, &directives.CustomInstructions)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pagewiki.GenerationDirectives{}, nil
	case err != nil:
		return pagewiki.GenerationDirectives{}, fmt.Errorf("load Page Wiki generation settings: %w", err)
	}
	return directives, nil
}

func (r *Repository) SetGenerationSettings(ctx context.Context, directives pagewiki.GenerationDirectives) error {
	if _, err := r.pool.Exec(ctx, `
INSERT INTO pagewiki_generation_settings (scope_id, language, custom_instructions)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id) DO UPDATE
SET language = EXCLUDED.language,
    custom_instructions = EXCLUDED.custom_instructions,
    updated_at = NOW()`, r.scopeID, directives.Language, directives.CustomInstructions); err != nil {
		return fmt.Errorf("save Page Wiki generation settings: %w", err)
	}
	return nil
}

func (r *Repository) RebuildPageWiki(
	ctx context.Context,
	scopeID string,
	processorName string,
	processorVersion string,
	since time.Time,
) (returnedErr error) {
	if strings.TrimSpace(scopeID) == "" || scopeID != r.scopeID ||
		strings.TrimSpace(processorName) == "" || strings.TrimSpace(processorVersion) == "" {
		return fmt.Errorf("rebuild Page Wiki: scope and processor identity are required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rebuild Page Wiki: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(
				returnedErr,
				fmt.Errorf("rebuild Page Wiki: roll back transaction: %w", rollbackErr),
			)
		}
	}()
	for _, query := range []string{
		"DELETE FROM pagewiki_maintenance_runs WHERE scope_id = $1",
		"DELETE FROM pagewiki_publications WHERE scope_id = $1",
		"DELETE FROM pagewiki_source_revisions WHERE scope_id = $1",
		"DELETE FROM pagewiki_topic_trees WHERE scope_id = $1",
		"DELETE FROM pagewiki_page_lifecycle WHERE scope_id = $1",
		"DELETE FROM pagewiki_curation_runs WHERE scope_id = $1",
		"DELETE FROM pagewiki_page_embeddings WHERE scope_id = $1",
	} {
		if _, err := tx.Exec(ctx, query, scopeID); err != nil {
			return fmt.Errorf("rebuild Page Wiki: clear derived state: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
DELETE FROM session_processor_cursors
WHERE processor_name = $1 AND processor_version = $2 AND scope_id = $3`,
		processorName, processorVersion, scopeID,
	); err != nil {
		return fmt.Errorf("rebuild Page Wiki: reset ingestion cursors: %w", err)
	}
	if !since.IsZero() {
		if _, err := tx.Exec(ctx, `
INSERT INTO session_processor_cursors
    (processor_name, processor_version, scope_id, agent_id, session_id, committed_sequence)
SELECT $1, $2, stream.scope_id, stream.agent_id, stream.session_id, stream.last_sequence
FROM session_streams AS stream
WHERE stream.scope_id = $3
  AND stream.source = 'agent-session'
  AND stream.agent_id <> ''
  AND NOT EXISTS (
    SELECT 1 FROM session_events AS event
    WHERE event.scope_id = stream.scope_id
      AND event.agent_id = stream.agent_id
      AND event.session_id = stream.session_id
      AND event.occurred_at >= $4
  )`, processorName, processorVersion, scopeID, since); err != nil {
			return fmt.Errorf("rebuild Page Wiki: seed lookback cursors: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO pagewiki_ingestion_settings (scope_id, auto_inject)
VALUES ($1, TRUE)
ON CONFLICT (scope_id) DO UPDATE
SET auto_inject = TRUE, updated_at = NOW()`, scopeID); err != nil {
		return fmt.Errorf("rebuild Page Wiki: enable ingestion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rebuild Page Wiki: commit transaction: %w", err)
	}
	committed = true
	r.memory.Reset()
	return nil
}

func (r *Repository) insertJSON(ctx context.Context, query, id string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal Page Wiki record %q: %w", id, err)
	}
	if _, err := r.pool.Exec(ctx, query, r.scopeID, id, payload); err != nil {
		return fmt.Errorf("persist Page Wiki record %q: %w", id, err)
	}
	return nil
}

func (r *Repository) SourceRevision(ctx context.Context, id string) (pagewiki.SourceRevision, error) {
	return r.memory.SourceRevision(ctx, id)
}

func (r *Repository) PageCatalog(ctx context.Context) (pagewiki.PageCatalog, error) {
	return r.memory.PageCatalog(ctx)
}

func (r *Repository) PageByID(ctx context.Context, id string) (pagewiki.Page, error) {
	return r.memory.PageByID(ctx, id)
}

func (r *Repository) PageBySlug(ctx context.Context, slug string) (pagewiki.Page, error) {
	return r.memory.PageBySlug(ctx, slug)
}

func (r *Repository) PageRevision(ctx context.Context, id string) (pagewiki.PageRevision, error) {
	return r.memory.PageRevision(ctx, id)
}

func (r *Repository) PageRevisionHistory(ctx context.Context, pageID string) ([]pagewiki.PageRevision, error) {
	return r.memory.PageRevisionHistory(ctx, pageID)
}

// RetirePage delegates to the in-memory repository for CAS validation, then
// persists the retire event keyed by page + expected base revision so
// hydration can replay it (see hydrateLifecycleEvents).
func (r *Repository) RetirePage(ctx context.Context, request pagewiki.RetireRequest) error {
	if err := r.memory.RetirePage(ctx, request); err != nil {
		return err
	}
	eventID := request.PageID + ":" + request.ExpectedBaseRevisionID
	return r.insertJSON(ctx, `
INSERT INTO pagewiki_page_lifecycle (scope_id, event_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, event_id) DO NOTHING`, eventID, request)
}

func (r *Repository) Navigation(ctx context.Context) (pagewiki.Navigation, error) {
	return r.memory.Navigation(ctx)
}

func (r *Repository) Search(ctx context.Context, query string) ([]pagewiki.SearchResult, error) {
	return r.memory.Search(ctx, query)
}

func (r *Repository) PageLinks(ctx context.Context, pageID string) (pagewiki.PageLinkSet, error) {
	return r.memory.PageLinks(ctx, pageID)
}

func (r *Repository) SourceBacklinks(ctx context.Context, id string) ([]pagewiki.SourceBacklink, error) {
	return r.memory.SourceBacklinks(ctx, id)
}

func (r *Repository) RebuildSearchIndex(ctx context.Context) error {
	return r.memory.RebuildSearchIndex(ctx)
}

func (r *Repository) MaintenanceRun(ctx context.Context, id string) (pagewiki.MaintenanceRun, error) {
	return r.memory.MaintenanceRun(ctx, id)
}

// SaveCurationRun delegates to the in-memory repository so it validates
// immutability/resave legality first, then upserts the run for hydration
// replay: the memory layer accepts a same-ID resave only when the stored
// run's Status was failed (a re-run against an unchanged catalog fingerprint,
// e.g. after a transient curator outage), so any write that reaches this
// point is legitimate to persist even when a row already exists. DO NOTHING
// would silently drop that resave, leaving the stale failed payload in
// place; hydration would then replay it back into memory on the next
// restart, reverting an otherwise-durable success to failed forever.
func (r *Repository) SaveCurationRun(ctx context.Context, run pagewiki.CurationRun) error {
	if err := r.memory.SaveCurationRun(ctx, run); err != nil {
		return err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal Page Wiki record %q: %w", run.ID, err)
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO pagewiki_curation_runs (scope_id, run_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, run_id) DO UPDATE
SET payload = EXCLUDED.payload, created_at = NOW()`, r.scopeID, run.ID, payload); err != nil {
		return fmt.Errorf("persist Page Wiki record %q: %w", run.ID, err)
	}
	return nil
}

// CurationRun reads from the in-memory repository, which is hydrated from
// pagewiki_curation_runs on startup.
func (r *Repository) CurationRun(ctx context.Context, id string) (pagewiki.CurationRun, error) {
	return r.memory.CurationRun(ctx, id)
}

// PageEmbeddings reads from the in-memory repository, which is hydrated from
// pagewiki_page_embeddings on startup.
func (r *Repository) PageEmbeddings(ctx context.Context) ([]pagewiki.PageEmbedding, error) {
	return r.memory.PageEmbeddings(ctx)
}

// SavePageEmbedding delegates to the in-memory repository, then upserts the
// embedding so later saves for the same page overwrite the persisted row.
func (r *Repository) SavePageEmbedding(ctx context.Context, embedding pagewiki.PageEmbedding) error {
	if err := r.memory.SavePageEmbedding(ctx, embedding); err != nil {
		return err
	}
	payload, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshal Page Wiki page embedding %q: %w", embedding.PageID, err)
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO pagewiki_page_embeddings (scope_id, page_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, page_id) DO UPDATE
SET payload = EXCLUDED.payload, updated_at = NOW()`, r.scopeID, embedding.PageID, payload); err != nil {
		return fmt.Errorf("persist Page Wiki page embedding %q: %w", embedding.PageID, err)
	}
	return nil
}

// SourceRevisionOrdinals delegates to the in-memory repository, which
// derives ordinals from the created_at replay order of
// pagewiki_source_revisions during hydration; there is no separate
// persistence for ordinals, nor is one needed.
func (r *Repository) SourceRevisionOrdinals(ctx context.Context) (map[string]int, error) {
	return r.memory.SourceRevisionOrdinals(ctx)
}

// TypeRegistry delegates to the in-memory repository, which is seeded on
// construction; persistence is added in a later task.
func (r *Repository) TypeRegistry(ctx context.Context) ([]pagewiki.TypeRegistryEntry, error) {
	return r.memory.TypeRegistry(ctx)
}

// SaveTypeRegistryEntry delegates to the in-memory repository; persistence
// is added in a later task.
func (r *Repository) SaveTypeRegistryEntry(ctx context.Context, entry pagewiki.TypeRegistryEntry) error {
	return r.memory.SaveTypeRegistryEntry(ctx, entry)
}

var _ pagewiki.Repository = (*Repository)(nil)
