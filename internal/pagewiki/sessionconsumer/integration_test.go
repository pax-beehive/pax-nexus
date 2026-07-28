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
	ctx     context.Context
	store   *platformpostgres.Store
	scopeID string
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
	s.scopeID = fmt.Sprintf("pagewiki-consumer-%d", time.Now().UnixNano())
}

func (s *postgresConsumerSuite) TearDownTest() {
	if s.store == nil || s.scopeID == "" {
		return
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
		_, err := s.store.Pool().Exec(s.ctx, query, s.scopeID)
		s.Require().NoError(err)
	}
}

func (s *postgresConsumerSuite) TestManualInjectionPersistsPageAndIndependentCursor() {
	s.seedSession()
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	controller, err := sessionconsumer.New(
		consumerStore,
		pagewiki.NewService(
			repository,
			pagewiki.SessionDocumentPlanner{},
			pagewiki.SessionDocumentEditor{},
		),
		repository,
		slog.New(slog.DiscardHandler),
		time.Hour,
	)
	s.Require().NoError(err)

	result, err := controller.InjectSession(s.ctx, s.scopeID, "runtime-session")

	s.Require().NoError(err)
	s.Equal(1, result.ProcessedStreams)
	navigation, err := repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(navigation.Roots, 1)
	s.Require().Len(navigation.Roots[0].Pages, 1)
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
	s.seedSession()
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	controller, err := sessionconsumer.New(
		consumerStore,
		pagewiki.NewService(
			repository,
			pagewiki.SessionDocumentPlanner{},
			pagewiki.SessionDocumentEditor{},
		),
		repository,
		slog.New(slog.DiscardHandler),
		time.Hour,
	)
	s.Require().NoError(err)
	_, err = controller.InjectSession(s.ctx, s.scopeID, "runtime-session")
	s.Require().NoError(err)

	status, err := controller.Rebuild(s.ctx, s.scopeID)

	s.Require().NoError(err)
	s.True(status.AutoInject)
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
	s.Require().Len(pending, 1)
	s.Equal("runtime-session", pending[0].Actor.SessionID)
}

func (s *postgresConsumerSuite) TestAutoSettingSelectsPendingStreams() {
	s.seedSession()
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool(), s.scopeID)
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
	s.Require().Len(streams, 1)
	s.Equal("runtime-session", streams[0].Actor.SessionID)
}

func (s *postgresConsumerSuite) seedSession() {
	_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_streams (
    scope_id, user_id, agent_id, session_id, last_sequence, complete
) VALUES ($1, 'owner', 'runtime-agent', 'runtime-session', 1, TRUE)`, s.scopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(s.ctx, `
INSERT INTO session_events (
    scope_id, event_id, user_id, agent_id, session_id, sequence,
    event_type, content, occurred_at
) VALUES (
    $1, 'runtime-event', 'owner', 'runtime-agent', 'runtime-session', 1,
    'assistant', 'Runtime verification passed.', NOW()
)`, s.scopeID)
	s.Require().NoError(err)
}
