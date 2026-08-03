package audit_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type consumerSuite struct {
	suite.Suite
	store     *fakeAuditStore
	consumer  *audit.Controller
	stream    audit.Stream
	events    []session.SessionEvent
	cancelCtx context.CancelFunc
}

func TestConsumerSuite(t *testing.T) {
	suite.Run(t, new(consumerSuite))
}

func (s *consumerSuite) SetupTest() {
	s.stream = audit.Stream{
		ScopeID: "local-team",
		Actor:   session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "sess-1"},
		Head:    2,
	}
	s.events = []session.SessionEvent{
		{ID: "evt-1", Actor: s.stream.Actor, Sequence: 1, Type: "message", Visibility: "team",
			OccurredAt: time.Date(2026, 7, 30, 10, 0, 1, 0, time.UTC)},
		{ID: "evt-2", Actor: s.stream.Actor, Sequence: 2, Type: "message", Visibility: "team",
			OccurredAt: time.Date(2026, 7, 30, 10, 0, 2, 0, time.UTC)},
	}
	s.store = &fakeAuditStore{
		streams:    []audit.Stream{s.stream},
		events:     s.events,
		applied:    make(chan audit.Batch, 16),
		pendingHit: make(chan struct{}, 16),
	}
	var err error
	s.consumer, err = audit.New(s.store, slog.New(slog.DiscardHandler), time.Hour)
	s.Require().NoError(err)
}

func (s *consumerSuite) startConsumer() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelCtx = cancel
	s.consumer.Start(ctx)
}

func (s *consumerSuite) TearDownTest() {
	if s.cancelCtx != nil {
		s.cancelCtx()
	}
}

func (s *consumerSuite) TestConsumesPendingStreamAndAdvancesCursor() {
	s.startConsumer()

	select {
	case batch := <-s.store.applied:
		s.Equal(int64(2), batch.Cursor)
		s.Equal("local-team", batch.ScopeID)
		s.Require().Len(batch.Activity, 1)
		s.Equal(int64(2), batch.Activity[0].EventCount)
	case <-time.After(2 * time.Second):
		s.Fail("consumer did not apply a batch")
	}
}

func (s *consumerSuite) TestCursorAdvancesOnlyWhenApplyBatchSucceeds() {
	s.store.applyErr = errors.New("store unavailable")
	s.startConsumer()

	s.Require().Eventually(func() bool { return s.store.applyAttempts() >= 1 }, 2*time.Second, 10*time.Millisecond)
	s.Zero(s.store.committedCount(), "failed ApplyBatch must not commit a cursor")
}

func (s *consumerSuite) TestStreamWithEventReadFailureIsSkipped() {
	second := audit.Stream{
		ScopeID: "local-team",
		Actor:   session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "sess-2"},
		Head:    1,
	}
	s.store.streams = []audit.Stream{s.stream, second}
	s.store.eventsErrFor = map[string]error{"sess-1": errors.New("events unavailable")}
	s.startConsumer()

	select {
	case batch := <-s.store.applied:
		s.Equal("sess-2", batch.Actor.SessionID, "healthy stream is still processed")
		s.Equal(int64(1), batch.Cursor)
	case <-time.After(2 * time.Second):
		s.Fail("consumer did not process the healthy stream")
	}
	s.Equal(1, s.store.committedCount(), "only the healthy stream commits a cursor")
}

func (s *consumerSuite) TestPendingStreamsFailureIsLoggedAndSkipped() {
	s.store.pendingErr = errors.New("pending unavailable")
	s.startConsumer()

	s.Require().Eventually(func() bool { return s.store.pendingCalls() >= 1 }, 2*time.Second, 10*time.Millisecond)
	s.Zero(s.store.applyAttempts())
	s.Zero(s.store.committedCount())
}

func (s *consumerSuite) TestStreamWithoutEventsStillAdvancesCursor() {
	s.store.events = nil
	s.startConsumer()

	select {
	case batch := <-s.store.applied:
		s.Equal(int64(2), batch.Cursor)
		s.Empty(batch.ToolCalls)
		s.Empty(batch.Activity)
	case <-time.After(2 * time.Second):
		s.Fail("consumer did not advance the empty stream cursor")
	}
}

func (s *consumerSuite) TestNewValidatesDependencies() {
	_, err := audit.New(nil, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = audit.New(s.store, nil, time.Second)
	s.Require().Error(err)

	controller, err := audit.New(s.store, slog.New(slog.DiscardHandler), 0)
	s.Require().NoError(err, "non-positive interval falls back to the default")
	s.NotNil(controller)
}

// fakeAuditStore is a thread-safe in-memory Store. applied receives only
// successfully committed batches, so a batch observed there implies the
// cursor advanced.
type fakeAuditStore struct {
	mu           sync.Mutex
	streams      []audit.Stream
	events       []session.SessionEvent
	pendingErr   error
	eventsErrFor map[string]error
	applyErr     error
	applied      chan audit.Batch
	pendingHit   chan struct{}
	attempts     int
	committed    int
}

func (s *fakeAuditStore) PendingStreams(context.Context) ([]audit.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case s.pendingHit <- struct{}{}:
	default:
	}
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	return append([]audit.Stream(nil), s.streams...), nil
}

func (s *fakeAuditStore) SessionEvents(
	_ context.Context,
	stream audit.Stream,
) ([]session.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.eventsErrFor[stream.Actor.SessionID]; ok {
		return nil, err
	}
	return append([]session.SessionEvent(nil), s.events...), nil
}

func (s *fakeAuditStore) ApplyBatch(_ context.Context, batch audit.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.applyErr != nil {
		return s.applyErr
	}
	s.committed++
	s.applied <- batch
	return nil
}

func (s *fakeAuditStore) applyAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func (s *fakeAuditStore) committedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed
}

func (s *fakeAuditStore) pendingCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingHit)
}
