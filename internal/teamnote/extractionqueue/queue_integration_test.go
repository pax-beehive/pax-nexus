package extractionqueue_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/stretchr/testify/suite"
)

type integrationSuite struct {
	suite.Suite
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(integrationSuite))
}

func (s *integrationSuite) TestCommittedSessionRunsInRiverWorker() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	defer pool.Close()
	s.Require().NoError(extractionqueue.Migrate(ctx, pool))

	processor := &integrationProcessor{calls: make(chan processedStream, 8)}
	queuePrefix := fmt.Sprintf("test_extract_%d", time.Now().UnixNano())
	queue, err := extractionqueue.New(pool, processor, extractionqueue.Config{
		QueuePrefix: queuePrefix, Shards: 2, Debounce: 10 * time.Millisecond, BatchTimeout: 20 * time.Millisecond,
	})
	s.Require().NoError(err)
	s.Require().NoError(queue.Start(ctx))
	defer func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Require().NoError(queue.Stop(stopContext))
	}()

	store, err := postgres.Open(ctx, dsn)
	s.Require().NoError(err)
	defer store.Close()
	s.Require().NoError(store.Migrate(ctx))
	s.Require().NoError(store.ConfigureExtractionEnqueuer(queue))

	scopeID := fmt.Sprintf("river-integration-%d", time.Now().UnixNano())
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	receipt, err := store.AppendSession(ctx, scopeID, teamnote.SessionBatch{
		Complete: true,
		Events: []teamnote.SessionEvent{{
			ID: "event", Actor: actor, Sequence: 1, Type: "assistant", Content: "status",
			OccurredAt: time.Now().UTC(),
		}},
	})
	s.Require().NoError(err)
	s.NotEmpty(receipt.RunID)

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()

completeJob:
	for {
		select {
		case call := <-processor.calls:
			if call.scopeID == scopeID {
				s.Equal(actor, call.actor)
				s.Equal(int64(1), call.expectedCursor)
				s.False(call.requireCurrent)
				break completeJob
			}
		case <-timeout.C:
			s.FailNow("River worker did not process the extraction job")
		}
	}

	incompleteScope := scopeID + "-incomplete"
	receipt, err = store.AppendSession(ctx, incompleteScope, teamnote.SessionBatch{Events: []teamnote.SessionEvent{{
		ID: "event", Actor: actor, Sequence: 1, Type: "assistant", Content: "status", OccurredAt: time.Now().UTC(),
	}}})
	s.Require().NoError(err)
	s.NotEmpty(receipt.RunID)
	for {
		select {
		case call := <-processor.calls:
			if call.scopeID == incompleteScope {
				s.Equal(int64(1), call.expectedCursor)
				s.True(call.requireCurrent)
				return
			}
		case <-timeout.C:
			s.FailNow("River worker did not process the timeout extraction job")
		}
	}
}

type processedStream struct {
	scopeID        string
	actor          teamnote.Actor
	expectedCursor int64
	requireCurrent bool
}

type integrationProcessor struct {
	calls chan processedStream
}

func (p *integrationProcessor) ProcessExtraction(ctx context.Context, actor teamnote.Actor, expectedCursor int64, requireCurrent bool) (bool, error) {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return false, err
	}
	p.calls <- processedStream{scopeID: scopeID, actor: actor, expectedCursor: expectedCursor, requireCurrent: requireCurrent}
	return false, nil
}
