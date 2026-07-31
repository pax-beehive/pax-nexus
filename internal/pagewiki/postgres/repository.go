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
	if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_publications
WHERE scope_id = $1 ORDER BY ordinal`, func(payload []byte) error {
		var publication pagewiki.PagePublication
		if err := json.Unmarshal(payload, &publication); err != nil {
			return err
		}
		if legacyPages > 0 && isSessionPublication(publication) {
			return nil
		}
		return r.memory.PublishPage(ctx, publication)
	}); err != nil {
		return fmt.Errorf("hydrate Page Wiki publications: %w", err)
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

func (r *Repository) SaveMaintenanceRun(ctx context.Context, run pagewiki.MaintenanceRun) error {
	if err := r.memory.SaveMaintenanceRun(ctx, run); err != nil {
		return err
	}
	return r.insertJSON(ctx, `
INSERT INTO pagewiki_maintenance_runs (scope_id, run_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id, run_id) DO NOTHING`, run.ID, run)
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

var _ pagewiki.Repository = (*Repository)(nil)
