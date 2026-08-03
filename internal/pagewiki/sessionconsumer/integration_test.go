package sessionconsumer_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	pagewikipostgres "github.com/pax-beehive/pax-nexus/internal/pagewiki/postgres"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	platformpostgres "github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type postgresConsumerSuite struct {
	suite.Suite
	ctx          context.Context
	store        *platformpostgres.Store
	scopeID      string
	otherScopeID string
}

func TestPostgresConsumerSuite(t *testing.T) {
	suite.Run(t, new(postgresConsumerSuite))
}

func (s *postgresConsumerSuite) SetupSuite() {
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

func (s *postgresConsumerSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *postgresConsumerSuite) SetupTest() {
	stamp := time.Now().UnixNano()
	s.scopeID = fmt.Sprintf("pagewiki-consumer-%d", stamp)
	s.otherScopeID = fmt.Sprintf("pagewiki-consumer-other-%d", stamp)
}

func (s *postgresConsumerSuite) TearDownTest() {
	if s.store == nil {
		return
	}
	for _, scopeID := range []string{s.scopeID, s.otherScopeID} {
		if scopeID == "" {
			continue
		}
		for _, query := range []string{
			"DELETE FROM session_processor_cursors WHERE scope_id = $1",
			"DELETE FROM pagewiki_ingestion_settings WHERE scope_id = $1",
			"DELETE FROM pagewiki_maintenance_runs WHERE scope_id = $1",
			"DELETE FROM pagewiki_publications WHERE scope_id = $1",
			"DELETE FROM pagewiki_source_revisions WHERE scope_id = $1",
			"DELETE FROM session_events WHERE scope_id = $1",
			"DELETE FROM session_streams WHERE scope_id = $1",
		} {
			_, err := s.store.Pool().Exec(s.ctx, query, scopeID)
			s.Require().NoError(err)
		}
	}
}

// injectorFor and rebuilderFor build the consumer's per-scope resolvers over
// one already-hydrated repository, mirroring what wiring does with the Page
// Wiki service and repository managers.
func (s *postgresConsumerSuite) injectorFor(
	repository *pagewikipostgres.Repository,
) sessionconsumer.InjectorFor {
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	return func(context.Context, string) (sessionconsumer.Injector, error) {
		return service, nil
	}
}

func (s *postgresConsumerSuite) rebuilderFor(
	repository *pagewikipostgres.Repository,
) sessionconsumer.RebuilderFor {
	return func(context.Context, string) (sessionconsumer.Rebuilder, error) {
		return repository, nil
	}
}

// streamsForScope filters a PendingStreams result down to one scope. The
// query is process-wide now, so sibling suites sharing the test database
// must not make these assertions flaky.
func (s *postgresConsumerSuite) streamsForScope(
	streams []sessionconsumer.Stream,
	scopeID string,
) []sessionconsumer.Stream {
	filtered := make([]sessionconsumer.Stream, 0, len(streams))
	for _, stream := range streams {
		if stream.ScopeID == scopeID {
			filtered = append(filtered, stream)
		}
	}
	return filtered
}

func (s *postgresConsumerSuite) TestManualInjectionPersistsPageAndIndependentCursor() {
	s.seedSession(s.scopeID)
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)
	controller, err := sessionconsumer.New(
		consumerStore,
		s.injectorFor(repository),
		s.rebuilderFor(repository),
		slog.New(slog.DiscardHandler),
		time.Hour,
	)
	s.Require().NoError(err)

	result, err := controller.InjectSession(s.ctx, s.scopeID, "runtime-session")

	s.Require().NoError(err)
	s.Equal(1, result.ProcessedStreams)
	navigation, err := repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(navigation.Roots)
	s.Require().Len(navigation.Pages, 1)
	var cursor int64
	err = s.store.Pool().QueryRow(s.ctx, `
SELECT committed_sequence
FROM session_processor_cursors
WHERE processor_name = $1 AND processor_version = $2
  AND scope_id = $3 AND agent_id = 'runtime-agent' AND session_id = 'runtime-session'`,
		sessionconsumer.ProcessorName,
		sessionconsumer.ProcessorVersion,
		s.scopeID,
	).Scan(&cursor)
	s.Require().NoError(err)
	s.Equal(int64(1), cursor)
}

func (s *postgresConsumerSuite) TestRebuildClearsDerivedWikiAndMakesSessionPendingAgain() {
	s.seedSession(s.scopeID)
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)
	controller, err := sessionconsumer.New(
		consumerStore,
		s.injectorFor(repository),
		s.rebuilderFor(repository),
		slog.New(slog.DiscardHandler),
		time.Hour,
	)
	s.Require().NoError(err)
	_, err = controller.InjectSession(s.ctx, s.scopeID, "runtime-session")
	s.Require().NoError(err)

	status, err := controller.Rebuild(s.ctx, s.scopeID, time.Time{})

	s.Require().NoError(err)
	s.True(status.AutoInject)
	// Rebuild only queues; the consumer loop executes it. Drive that pass
	// explicitly so the DB assertions below observe the post-rebuild state
	// without racing a background scan.
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	controller.RunQueuedRebuildForTest(s.ctx)
	executed, err := controller.Status(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildIdle, executed.Rebuild.State)
	navigation, err := repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Empty(navigation.Roots)
	var cursorCount int
	err = s.store.Pool().QueryRow(s.ctx, `
SELECT COUNT(*) FROM session_processor_cursors
WHERE processor_name = $1 AND processor_version = $2 AND scope_id = $3`,
		sessionconsumer.ProcessorName, sessionconsumer.ProcessorVersion, s.scopeID,
	).Scan(&cursorCount)
	s.Require().NoError(err)
	s.Zero(cursorCount)
	pending, err := consumerStore.PendingStreams(s.ctx)
	s.Require().NoError(err)
	own := s.streamsForScope(pending, s.scopeID)
	s.Require().Len(own, 1)
	s.Equal("runtime-session", own[0].Actor.SessionID)
}

func (s *postgresConsumerSuite) TestAutoSettingSelectsPendingStreams() {
	s.seedSession(s.scopeID)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)

	enabled, err := consumerStore.AutoInjectEnabled(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.False(enabled)
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.scopeID, true))
	enabled, err = consumerStore.AutoInjectEnabled(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.True(enabled)
	streams, err := consumerStore.PendingStreams(s.ctx)
	s.Require().NoError(err)
	own := s.streamsForScope(streams, s.scopeID)
	s.Require().Len(own, 1)
	s.Equal("runtime-session", own[0].Actor.SessionID)
}

// TestPendingStreamsSpanEveryScopeWithAutoInject pins the multi-scope sweep:
// one consumer store serves every tenant, and auto inject stays a per-scope
// decision carried by the ingestion-settings join.
func (s *postgresConsumerSuite) TestPendingStreamsSpanEveryScopeWithAutoInject() {
	s.seedSession(s.scopeID)
	s.seedSession(s.otherScopeID)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.scopeID, true))
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.otherScopeID, true))

	streams, err := consumerStore.PendingStreams(s.ctx)

	s.Require().NoError(err)
	own := s.streamsForScope(streams, s.scopeID)
	other := s.streamsForScope(streams, s.otherScopeID)
	s.Require().Len(own, 1)
	s.Require().Len(other, 1)
	s.Equal(s.scopeID, own[0].ScopeID)
	s.Equal(s.otherScopeID, other[0].ScopeID)
	s.Equal("runtime-session", other[0].Actor.SessionID)
	s.Equal(int64(1), other[0].Head)

	// Auto inject stays per scope: switching one off must not hide the other.
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.otherScopeID, false))
	streams, err = consumerStore.PendingStreams(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(s.streamsForScope(streams, s.scopeID), 1)
	s.Empty(s.streamsForScope(streams, s.otherScopeID))
}

func (s *postgresConsumerSuite) TestProgressCountsBacklogAndLastProcessed() {
	s.seedSession(s.scopeID)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)

	// Backlog is visible even though auto inject was never enabled.
	progress, err := consumerStore.Progress(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.Equal(1, progress.PendingSessions)
	s.Nil(progress.LastProcessedAt)

	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	controller, err := sessionconsumer.New(
		consumerStore,
		s.injectorFor(repository),
		s.rebuilderFor(repository),
		slog.New(slog.DiscardHandler),
		time.Hour,
	)
	s.Require().NoError(err)
	_, err = controller.InjectSession(s.ctx, s.scopeID, "runtime-session")
	s.Require().NoError(err)

	progress, err = consumerStore.Progress(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.Zero(progress.PendingSessions)
	s.Require().NotNil(progress.LastProcessedAt)
	s.WithinDuration(time.Now(), *progress.LastProcessedAt, time.Minute)
}

// TestPendingStreamsCapsEachScopeSoNoTenantStarvesAnother pins the
// multi-tenant fairness quota: one scope's huge (or permanently failing)
// backlog may take at most 20 of the 100 slots per scan, so every other
// scope's streams still surface.
func (s *postgresConsumerSuite) TestPendingStreamsCapsEachScopeSoNoTenantStarvesAnother() {
	for i := 0; i < 25; i++ {
		s.seedStream(s.scopeID, fmt.Sprintf("bulk-session-%02d", i))
	}
	s.seedSession(s.otherScopeID)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.scopeID, true))
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.otherScopeID, true))

	streams, err := consumerStore.PendingStreams(s.ctx)

	s.Require().NoError(err)
	own := s.streamsForScope(streams, s.scopeID)
	other := s.streamsForScope(streams, s.otherScopeID)
	s.Len(own, 20, "a single scope must be capped at 20 slots per scan")
	s.Require().Len(other, 1, "the second scope must not be starved")
	s.Equal("runtime-session", other[0].Actor.SessionID)
}

// seedStream seeds one pending stream with its own session ID, so a test
// can give a single scope a backlog wider than the per-scope quota.
func (s *postgresConsumerSuite) seedStream(scopeID, sessionID string) {
	_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_streams (
    scope_id, user_id, agent_id, session_id, last_sequence, complete, source, stream_id
) VALUES ($1, 'owner', 'runtime-agent', $2, 1, TRUE, 'agent-session', $3)`, scopeID, sessionID, fmt.Sprintf("runtime-agent:%s", sessionID))
	s.Require().NoError(err)
}

func (s *postgresConsumerSuite) seedSession(scopeID string) {
	_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_streams (
    scope_id, user_id, agent_id, session_id, last_sequence, complete, source, stream_id
) VALUES ($1, 'owner', 'runtime-agent', 'runtime-session', 1, TRUE, 'agent-session', 'runtime-agent:runtime-session')`, scopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(s.ctx, `
INSERT INTO session_events (
    scope_id, event_id, user_id, agent_id, session_id, sequence,
    event_type, content, occurred_at
) VALUES (
    $1, 'runtime-event', 'owner', 'runtime-agent', 'runtime-session', 1,
    'assistant', 'Runtime verification passed.', NOW()
)`, scopeID)
	s.Require().NoError(err)
}
