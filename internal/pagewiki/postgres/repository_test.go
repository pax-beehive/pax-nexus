package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	pagewikipostgres "github.com/pax-beehive/pax-nexus/internal/pagewiki/postgres"
	platformpostgres "github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type repositorySuite struct {
	suite.Suite
	ctx     context.Context
	store   *platformpostgres.Store
	scopeID string
}

func TestRepositorySuite(t *testing.T) {
	suite.Run(t, new(repositorySuite))
}

func (s *repositorySuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not configured")
	}
	s.ctx = context.Background()
	var err error
	s.store, err = platformpostgres.Open(s.ctx, dsn)
	s.Require().NoError(err)
	s.Require().NoError(s.store.Migrate(s.ctx))
}

func (s *repositorySuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *repositorySuite) SetupTest() {
	s.scopeID = fmt.Sprintf("pagewiki-repository-%d", time.Now().UnixNano())
}

func (s *repositorySuite) TearDownTest() {
	if s.store == nil || s.scopeID == "" {
		return
	}
	for _, query := range []string{
		"DELETE FROM pagewiki_maintenance_runs WHERE scope_id = $1",
		"DELETE FROM pagewiki_publications WHERE scope_id = $1",
		"DELETE FROM pagewiki_source_revisions WHERE scope_id = $1",
		"DELETE FROM pagewiki_topic_trees WHERE scope_id = $1",
		"DELETE FROM pagewiki_page_lifecycle WHERE scope_id = $1",
		"DELETE FROM pagewiki_curation_runs WHERE scope_id = $1",
		"DELETE FROM pagewiki_page_embeddings WHERE scope_id = $1",
		"DELETE FROM pagewiki_type_registry WHERE scope_id = $1",
		"DELETE FROM session_processor_cursors WHERE scope_id = $1",
		"DELETE FROM session_events WHERE scope_id = $1",
		"DELETE FROM session_streams WHERE scope_id = $1",
	} {
		_, err := s.store.Pool().Exec(s.ctx, query, s.scopeID)
		s.Require().NoError(err)
	}
}

func (s *repositorySuite) TestPersistsAndHydratesCompleteWikiState() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "[event:runtime-event sequence:1 type:assistant] Runtime verification passed."
	result, err := service.InjectSession(s.ctx, pagewiki.InjectSessionRequest{
		SourceID:       "session:local-team:runtime-agent:runtime-session",
		IdempotencyKey: "manual-1",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "runtime-event", StartByte: 0, EndByte: len(raw),
		}},
	})
	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, result.Run.Status)

	reloaded, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	navigation, err := reloaded.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(navigation.Roots)
	s.Require().Len(navigation.Pages, 1)
	pageSummary := navigation.Pages[0]
	page, err := reloaded.PageByID(s.ctx, pageSummary.ID)
	s.Require().NoError(err)
	bySlug, err := reloaded.PageBySlug(s.ctx, page.Slug)
	s.Require().NoError(err)
	s.Equal(page.ID, bySlug.ID)
	revision, err := reloaded.PageRevision(s.ctx, page.CurrentRevisionID)
	s.Require().NoError(err)
	s.Contains(revision.Markdown, "Runtime verification passed.")
	history, err := reloaded.PageRevisionHistory(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Len(history, 1)
	catalog, err := reloaded.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Len(catalog, 1)
	source, err := reloaded.SourceRevision(s.ctx, result.SourceRevisionID)
	s.Require().NoError(err)
	s.Equal("runtime-event", source.Events[0].ID)
	search, err := reloaded.Search(s.ctx, "verification")
	s.Require().NoError(err)
	s.Require().Len(search, 1)
	links, err := reloaded.PageLinks(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Empty(links.Outgoing)
	backlinks, err := reloaded.SourceBacklinks(s.ctx, result.SourceRevisionID)
	s.Require().NoError(err)
	s.Len(backlinks, 1)
	run, err := reloaded.MaintenanceRun(s.ctx, result.Run.ID)
	s.Require().NoError(err)
	s.Equal(result.Run.ID, run.ID)
	s.Require().NoError(reloaded.RebuildSearchIndex(s.ctx))
}

func (s *repositorySuite) TestRetriedMaintenanceRunSurvivesRehydration() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	run := pagewiki.MaintenanceRun{
		ID:               "run-retry",
		SourceRevisionID: "source-revision-1",
		Status:           pagewiki.RunStatusFailed,
		Targets: []pagewiki.MaintenanceTarget{
			{ID: "target-1", Status: pagewiki.TargetStatusFailed},
		},
	}
	s.Require().NoError(repository.SaveMaintenanceRun(s.ctx, run))
	run.Status = pagewiki.RunStatusSucceeded
	run.Targets[0].Status = pagewiki.TargetStatusSucceeded

	s.Require().NoError(repository.SaveMaintenanceRun(s.ctx, run))

	reloaded, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	stored, err := reloaded.MaintenanceRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, stored.Status)
	s.Equal(pagewiki.TargetStatusSucceeded, stored.Targets[0].Status)
}

func (s *repositorySuite) TestTopicTreeSurvivesRehydration() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "[event:runtime-event sequence:1 type:assistant] Runtime verification passed."
	result, err := service.InjectSession(s.ctx, pagewiki.InjectSessionRequest{
		SourceID:       "session:local-team:runtime-agent:runtime-session",
		IdempotencyKey: "manual-1",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "runtime-event", StartByte: 0, EndByte: len(raw),
		}},
	})
	s.Require().NoError(err)
	catalog, err := repository.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(catalog, 1)
	pageID := catalog[0].ID

	tree := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-a", Slug: "runtime", Title: "Runtime"}},
		Placements: []pagewiki.PagePlacement{{PageID: pageID, TopicID: "topic-a"}},
	}
	s.Require().NoError(repository.ReplaceTopicTree(s.ctx, tree))

	reopened, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	loaded, err := reopened.TopicTree(s.ctx)
	s.Require().NoError(err)
	s.Equal(tree, loaded)

	s.Require().NoError(reopened.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", time.Time{}))
	rebuilt, err := reopened.TopicTree(s.ctx)
	s.Require().NoError(err)
	s.Empty(rebuilt.Topics)
	s.Empty(rebuilt.Placements)

	afterRebuild, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	afterRebuildTree, err := afterRebuild.TopicTree(s.ctx)
	s.Require().NoError(err)
	s.Empty(afterRebuildTree.Topics)
	s.Empty(afterRebuildTree.Placements)

	_ = result
}

func (s *repositorySuite) injectSinglePage(repository *pagewikipostgres.Repository) pagewiki.Page {
	s.T().Helper()
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "[event:runtime-event sequence:1 type:assistant] Runtime verification passed."
	_, err := service.InjectSession(s.ctx, pagewiki.InjectSessionRequest{
		SourceID:       "session:local-team:runtime-agent:runtime-session",
		IdempotencyKey: "manual-1",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "runtime-event", StartByte: 0, EndByte: len(raw),
		}},
	})
	s.Require().NoError(err)
	catalog, err := repository.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(catalog, 1)
	page, err := repository.PageByID(s.ctx, catalog[0].ID)
	s.Require().NoError(err)
	return page
}

func (s *repositorySuite) TestRetiredPageSurvivesRehydration() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	page := s.injectSinglePage(repository)

	s.Require().NoError(repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 page.ID,
		ExpectedBaseRevisionID: page.CurrentRevisionID,
		SuccessorPageID:        "successor-page",
		RunID:                  "manual-retire",
	}))
	retired, err := repository.PageByID(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().True(retired.Retired())

	reopened, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	reloaded, err := reopened.PageByID(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().True(reloaded.Retired())
	s.Require().Equal("successor-page", reloaded.SuccessorPageID)
	s.Require().Equal("manual-retire", reloaded.RetiredByRunID)

	catalog, err := reopened.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(catalog)
}

func (s *repositorySuite) TestCurationRunAndPageEmbeddingSurviveRehydration() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	run := pagewiki.CurationRun{
		ID:          "curation-run-1",
		Fingerprint: "fingerprint-1",
		Status:      pagewiki.RunStatusSucceeded,
		Outcomes: []pagewiki.CurationOutcome{
			{
				Kind:      "pair",
				PageIDs:   []string{"page-1", "page-2"},
				Verdict:   pagewiki.CurationVerdictMerge,
				Rationale: "overlapping content",
				Status:    pagewiki.TargetStatusSucceeded,
			},
		},
	}
	s.Require().NoError(repository.SaveCurationRun(s.ctx, run))

	embedding := pagewiki.PageEmbedding{
		PageID:     "page-1",
		RevisionID: "rev-1",
		Vector:     []float32{0.1, 0.2, 0.3},
	}
	s.Require().NoError(repository.SavePageEmbedding(s.ctx, embedding))
	// Re-saving for the same page must replace, not duplicate, the row.
	embedding.RevisionID = "rev-2"
	embedding.Vector = []float32{0.4, 0.5, 0.6}
	s.Require().NoError(repository.SavePageEmbedding(s.ctx, embedding))

	reopened, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	storedRun, err := reopened.CurationRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Require().Equal(run, storedRun)

	embeddings, err := reopened.PageEmbeddings(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(embeddings, 1)
	s.Require().Equal(embedding, embeddings[0])
}

// TestCurationRunResaveAfterFailurePersistsAcrossRehydration guards against a
// regression where SaveCurationRun's SQL used ON CONFLICT DO NOTHING: a
// same-run-ID resave (which the memory layer only permits when the
// previously stored run's Status was failed — see RunCurationRound's
// idempotent-skip semantics) would silently drop, leaving the stale failed
// payload in place. On the next hydration that stale row would replay back
// into memory, reverting an otherwise-durable success to failed forever.
func (s *repositorySuite) TestCurationRunResaveAfterFailurePersistsAcrossRehydration() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	failedRun := pagewiki.CurationRun{
		ID:          "curation-run-retry",
		Fingerprint: "fingerprint-retry",
		Status:      pagewiki.RunStatusFailed,
		Outcomes: []pagewiki.CurationOutcome{
			{
				Kind:    "page",
				PageIDs: []string{"page-1"},
				Verdict: pagewiki.CurationVerdictKeep,
				Status:  pagewiki.TargetStatusFailed,
				Error:   "curator backend unavailable",
			},
		},
	}
	s.Require().NoError(repository.SaveCurationRun(s.ctx, failedRun))

	succeededRun := pagewiki.CurationRun{
		ID:          failedRun.ID,
		Fingerprint: failedRun.Fingerprint,
		Status:      pagewiki.RunStatusSucceeded,
		Outcomes: []pagewiki.CurationOutcome{
			{
				Kind:    "page",
				PageIDs: []string{"page-1"},
				Verdict: pagewiki.CurationVerdictKeep,
				Status:  pagewiki.TargetStatusSucceeded,
			},
		},
	}
	s.Require().NoError(repository.SaveCurationRun(s.ctx, succeededRun))

	reopened, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	storedRun, err := reopened.CurationRun(s.ctx, failedRun.ID)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, storedRun.Status,
		"the resaved successful run must survive rehydration, not revert to the stale failed payload")
	s.Require().Equal(succeededRun, storedRun)
}

func (s *repositorySuite) TestTypeRegistryEntrySurvivesRehydration() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	entry := pagewiki.TypeRegistryEntry{
		Kind:        pagewiki.TypeKindEntity,
		Name:        "incident",
		Description: "An operational incident under investigation or resolved.",
		Status:      pagewiki.TypeStatusCandidate,
	}
	s.Require().NoError(repository.SaveTypeRegistryEntry(s.ctx, entry))

	reopened, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	registry, err := reopened.TypeRegistry(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(registry, 12)
	var found *pagewiki.TypeRegistryEntry
	for index := range registry {
		if registry[index].Kind == pagewiki.TypeKindEntity && registry[index].Name == "incident" {
			found = &registry[index]
			break
		}
	}
	s.Require().NotNil(found, "expected incident entity type row to survive rehydration")
	s.Require().Equal(pagewiki.TypeStatusCandidate, found.Status)
}

func (s *repositorySuite) TestRebuildClearsLifecycleCurationAndEmbeddingTables() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	page := s.injectSinglePage(repository)

	s.Require().NoError(repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 page.ID,
		ExpectedBaseRevisionID: page.CurrentRevisionID,
		RunID:                  "manual-retire",
	}))
	s.Require().NoError(repository.SaveCurationRun(s.ctx, pagewiki.CurationRun{
		ID:     "curation-run-1",
		Status: pagewiki.RunStatusSucceeded,
	}))
	s.Require().NoError(repository.SavePageEmbedding(s.ctx, pagewiki.PageEmbedding{
		PageID:     page.ID,
		RevisionID: page.CurrentRevisionID,
		Vector:     []float32{0.1},
	}))

	s.Require().NoError(repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", time.Time{}))

	for _, table := range []string{"pagewiki_page_lifecycle", "pagewiki_curation_runs", "pagewiki_page_embeddings"} {
		var count int
		s.Require().NoError(s.store.Pool().QueryRow(s.ctx,
			fmt.Sprintf("SELECT count(*) FROM %s WHERE scope_id = $1", table), s.scopeID,
		).Scan(&count))
		s.Require().Zero(count, "%s should be empty after rebuild", table)
	}

	_, err = repository.CurationRun(s.ctx, "curation-run-1")
	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
	embeddings, err := repository.PageEmbeddings(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(embeddings)
}

func (s *repositorySuite) TestRejectsInvalidConfigurationAndCorruptSnapshots() {
	_, err := pagewikipostgres.NewRepository(s.ctx, nil, s.scopeID)
	s.Require().Error(err)
	_, err = pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), "")
	s.Require().Error(err)

	tests := []struct {
		name  string
		table string
		id    string
	}{
		{name: "source", table: "pagewiki_source_revisions", id: "source_revision_id"},
		{name: "publication", table: "pagewiki_publications", id: "page_revision_id"},
		{name: "run", table: "pagewiki_maintenance_runs", id: "run_id"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			scopeID := s.scopeID + "-" + test.name
			query := fmt.Sprintf(
				"INSERT INTO %s (scope_id, %s, payload) VALUES ($1, 'broken', $2)",
				test.table,
				test.id,
			)
			_, insertErr := s.store.Pool().Exec(s.ctx, query, scopeID, []byte(`{"ID":123}`))
			s.Require().NoError(insertErr)

			_, loadErr := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), scopeID)

			s.Require().Error(loadErr)
			_, deleteErr := s.store.Pool().Exec(
				s.ctx,
				fmt.Sprintf("DELETE FROM %s WHERE scope_id = $1", test.table),
				scopeID,
			)
			s.Require().NoError(deleteErr)
		})
	}
}

func (s *repositorySuite) TestRebuildRejectsInvalidScopeAndProcessorIdentity() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	tests := []struct {
		name             string
		scopeID          string
		processorName    string
		processorVersion string
	}{
		{name: "missing scope", processorName: "page_wiki", processorVersion: "v1"},
		{name: "wrong scope", scopeID: "another-scope", processorName: "page_wiki", processorVersion: "v1"},
		{name: "missing processor name", scopeID: s.scopeID, processorVersion: "v1"},
		{name: "missing processor version", scopeID: s.scopeID, processorName: "page_wiki"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			err := repository.RebuildPageWiki(
				s.ctx,
				test.scopeID,
				test.processorName,
				test.processorVersion,
				time.Time{},
			)
			s.Require().ErrorContains(err, "scope and processor identity are required")
		})
	}
}

func (s *repositorySuite) TestRebuildReportsCanceledTransactionStart() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(s.ctx)
	cancel()

	err = repository.RebuildPageWiki(ctx, s.scopeID, "page_wiki", "v1", time.Time{})

	s.Require().ErrorContains(err, "begin transaction")
	s.Require().ErrorIs(err, context.Canceled)
}

func (s *repositorySuite) TestGenerationSettingsRoundTrip() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	directives, err := repository.GenerationSettings(s.ctx)
	s.Require().NoError(err)
	s.Require().True(directives.IsZero())

	want := pagewiki.GenerationDirectives{Language: "简体中文", CustomInstructions: "prefer tables"}
	s.Require().NoError(repository.SetGenerationSettings(s.ctx, want))
	got, err := repository.GenerationSettings(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(want, got)

	// Second write overwrites (upsert semantics).
	s.Require().NoError(repository.SetGenerationSettings(s.ctx, pagewiki.GenerationDirectives{Language: "English"}))
	got, err = repository.GenerationSettings(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.GenerationDirectives{Language: "English"}, got)

	_, err = s.store.Pool().Exec(s.ctx, "DELETE FROM pagewiki_generation_settings WHERE scope_id = $1", s.scopeID)
	s.Require().NoError(err)
}

func (s *repositorySuite) TestRebuildRollsBackWhenPersistentStateCannotBeCleared() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN") + "&default_transaction_read_only=on"
	readOnlyPool, err := pgxpool.New(s.ctx, dsn)
	s.Require().NoError(err)
	defer readOnlyPool.Close()
	repository, err := pagewikipostgres.NewRepository(s.ctx, readOnlyPool, s.scopeID)
	s.Require().NoError(err)

	err = repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", time.Time{})

	s.Require().ErrorContains(err, "clear derived state")
	s.Require().ErrorContains(err, "read-only transaction")
}

func (s *repositorySuite) seedStream(agentID, sessionID string, lastSequence int64, occurredAt []time.Time) {
	s.T().Helper()
	_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_streams (scope_id, user_id, agent_id, session_id, last_sequence, stream_id)
VALUES ($1, 'user-1', $2, $3, $4, $3)`, s.scopeID, agentID, sessionID, lastSequence)
	s.Require().NoError(err)
	for index, at := range occurredAt {
		_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_events
    (scope_id, event_id, user_id, agent_id, session_id, sequence, event_type, content, occurred_at, stream_id)
VALUES ($1, $2, 'user-1', $3, $4, $5, 'message', 'event content', $6, $4)`,
			s.scopeID, fmt.Sprintf("%s-event-%d", sessionID, index+1),
			agentID, sessionID, int64(index+1), at)
		s.Require().NoError(err)
	}
}

func (s *repositorySuite) processorCursors() map[string]int64 {
	s.T().Helper()
	rows, err := s.store.Pool().Query(s.ctx, `
SELECT session_id, committed_sequence FROM session_processor_cursors
WHERE scope_id = $1 AND processor_name = 'page_wiki' AND processor_version = 'v1'`, s.scopeID)
	s.Require().NoError(err)
	defer rows.Close()
	cursors := map[string]int64{}
	for rows.Next() {
		var sessionID string
		var committed int64
		s.Require().NoError(rows.Scan(&sessionID, &committed))
		cursors[sessionID] = committed
	}
	s.Require().NoError(rows.Err())
	return cursors
}

func (s *repositorySuite) TestRebuildWithLookbackSkipsStaleStreamsAndReplaysActiveOnes() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Entirely before the cutoff: must be skipped (cursor = last_sequence).
	s.seedStream("agent-1", "stale-session", 2,
		[]time.Time{cutoff.Add(-48 * time.Hour), cutoff.Add(-24 * time.Hour)})
	// Straddles the cutoff: must replay in full (no cursor row).
	s.seedStream("agent-1", "straddling-session", 2,
		[]time.Time{cutoff.Add(-24 * time.Hour), cutoff.Add(24 * time.Hour)})
	// Entirely after the cutoff: must replay in full (no cursor row).
	s.seedStream("agent-1", "fresh-session", 1, []time.Time{cutoff.Add(48 * time.Hour)})

	s.Require().NoError(repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", cutoff))

	s.Equal(map[string]int64{"stale-session": 2}, s.processorCursors())
}

func (s *repositorySuite) TestRebuildWithZeroSinceSeedsNoCursors() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	s.seedStream("agent-1", "old-session", 1,
		[]time.Time{time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})

	s.Require().NoError(repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", time.Time{}))

	s.Empty(s.processorCursors())
}
