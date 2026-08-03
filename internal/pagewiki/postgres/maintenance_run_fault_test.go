package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	pagewikipostgres "github.com/pax-beehive/pax-nexus/internal/pagewiki/postgres"
	platformpostgres "github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

// maintenanceRunFaultRepository delegates to the real postgres Repository but
// can force SaveMaintenanceRun's database leg to fail: it passes an
// already-cancelled context, which the in-memory mirror ignores and the pool
// write rejects — exactly the partial-failure mode a database outage causes
// mid write-through.
type maintenanceRunFaultRepository struct {
	pagewiki.Repository
	fail atomic.Bool
}

func (r *maintenanceRunFaultRepository) SaveMaintenanceRun(
	ctx context.Context,
	run pagewiki.MaintenanceRun,
) error {
	if r.fail.Load() {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		ctx = cancelled
	}
	return r.Repository.SaveMaintenanceRun(ctx, run)
}

type maintenanceRunFaultSuite struct {
	suite.Suite
	ctx     context.Context
	store   *platformpostgres.Store
	scopeID string
}

func TestMaintenanceRunFaultSuite(t *testing.T) {
	suite.Run(t, new(maintenanceRunFaultSuite))
}

func (s *maintenanceRunFaultSuite) SetupSuite() {
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

func (s *maintenanceRunFaultSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *maintenanceRunFaultSuite) SetupTest() {
	s.scopeID = fmt.Sprintf("pagewiki-run-fault-%d", time.Now().UnixNano())
}

func (s *maintenanceRunFaultSuite) TearDownTest() {
	if s.store == nil {
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
	} {
		_, err := s.store.Pool().Exec(s.ctx, query, s.scopeID)
		s.Require().NoError(err)
	}
}

// TestFailedRunPersistDoesNotStrandTheRunOnRetry pins the write-through
// journal contract: when SaveMaintenanceRun's database write fails after the
// in-memory mirror recorded the run, memory must not keep claiming success —
// otherwise the retried injection short-circuits on the succeeded in-memory
// run, the cursor advances, and the run row never lands in the database.
func (s *maintenanceRunFaultSuite) TestFailedRunPersistDoesNotStrandTheRunOnRetry() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	faulty := &maintenanceRunFaultRepository{Repository: repository}
	service := pagewiki.NewService(
		faulty,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "[event:runtime-event sequence:1 type:assistant] Runtime verification passed."
	request := pagewiki.InjectSessionRequest{
		SourceID:       fmt.Sprintf("session:%s:runtime-agent:runtime-session", s.scopeID),
		IdempotencyKey: "manual-1",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "runtime-event", StartByte: 0, EndByte: len(raw),
		}},
	}

	faulty.fail.Store(true)
	_, err = service.InjectSession(s.ctx, request)
	s.Require().ErrorContains(err, "save MaintenanceRun")

	faulty.fail.Store(false)
	result, err := service.InjectSession(s.ctx, request)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)

	var count int
	s.Require().NoError(s.store.Pool().QueryRow(s.ctx, `
SELECT COUNT(*) FROM pagewiki_maintenance_runs WHERE scope_id = $1`, s.scopeID).Scan(&count))
	s.Equal(1, count, "the retried injection must persist the maintenance run row")
}
