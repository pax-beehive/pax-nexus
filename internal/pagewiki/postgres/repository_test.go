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

	s.Require().NoError(reopened.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1"))
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

	err = repository.RebuildPageWiki(ctx, s.scopeID, "page_wiki", "v1")

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

	err = repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1")

	s.Require().ErrorContains(err, "clear derived state")
	s.Require().ErrorContains(err, "read-only transaction")
}
