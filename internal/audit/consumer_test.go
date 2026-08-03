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
		cursors:    make(map[string]int64),
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

// TestStopDrainsTheScanLoop is the shutdown seam's leak guard: Stop must
// cancel the loop and block until the background goroutine has exited, so
// the composition root can shut consumers down in order rather than relying
// on context cancellation alone.
func (s *consumerSuite) TestStopDrainsTheScanLoop() {
	s.startConsumer()
	select {
	case <-s.store.applied:
	case <-time.After(2 * time.Second):
		s.Fail("consumer never ran a scan")
	}

	stopContext, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()
	s.Require().NoError(s.consumer.Stop(stopContext))

	// A second Stop is a no-op rather than a panic on the closed done channel.
	s.Require().NoError(s.consumer.Stop(stopContext))
}

// TestStopWithoutStartIsANoOp covers the failed-build unwind path, where the
// composition root stops consumers it assembled but never started.
func (s *consumerSuite) TestStopWithoutStartIsANoOp() {
	consumer, err := audit.New(s.store, slog.New(slog.DiscardHandler), time.Hour)
	s.Require().NoError(err)

	s.Require().NoError(consumer.Stop(context.Background()))
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

// TestSecondScanProjectsOnlyNewEvents guards against full-history replay: a
// stream that gains one event after a committed scan must contribute exactly
// that event's projection, without re-emitting findings or re-counting
// activity for the already-committed prefix.
func (s *consumerSuite) TestSecondScanProjectsOnlyNewEvents() {
	s.store.events[1] = session.SessionEvent{
		ID: "evt-2", Actor: s.stream.Actor, Sequence: 2,
		Type: audit.EventTypeToolCall, Visibility: "team",
		OccurredAt: time.Date(2026, 7, 30, 10, 0, 2, 0, time.UTC),
		Metadata: map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "rm -rf /tmp/cache",
		},
	}
	s.startConsumer()
	first := s.waitBatch()
	s.Equal(int64(2), first.Cursor)
	s.Require().Len(first.ToolCalls, 1)
	s.Require().Len(first.Findings, 1, "unapproved high-risk call yields one finding")
	s.Require().Len(first.Activity, 1)
	s.Equal(int64(2), first.Activity[0].EventCount)
	s.cancelCtx()

	s.store.appendEvent(session.SessionEvent{
		ID: "evt-3", Actor: s.stream.Actor, Sequence: 3,
		Type: "message", Visibility: "team",
		OccurredAt: time.Date(2026, 7, 30, 10, 0, 3, 0, time.UTC),
	})
	restarted, err := audit.New(s.store, slog.New(slog.DiscardHandler), time.Hour)
	s.Require().NoError(err)
	s.consumer = restarted
	s.startConsumer()
	second := s.waitBatch()
	s.Equal(int64(3), second.Cursor)
	s.Empty(second.ToolCalls, "committed tool call must not be re-projected")
	s.Empty(second.Findings, "committed finding must not be duplicated")
	s.Require().Len(second.Activity, 1)
	s.Equal(int64(1), second.Activity[0].EventCount,
		"second batch counts only the new event; totals across batches equal the true 3 events")
	s.Zero(second.Activity[0].ToolCallCount)
	s.Zero(second.Activity[0].HighRiskCount)
	s.Equal(int64(3), first.Activity[0].EventCount+second.Activity[0].EventCount,
		"daily activity across both batches sums to the true event total")
}

func (s *consumerSuite) waitBatch() audit.Batch {
	select {
	case batch := <-s.store.applied:
		return batch
	case <-time.After(2 * time.Second):
		s.Require().Fail("consumer did not apply a batch in time")
		return audit.Batch{}
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

// fakeAuditStore is a thread-safe in-memory Store mirroring the postgres
// contract: PendingStreams reports only streams whose head is past the
// committed cursor, SessionEvents returns the uncommitted (Committed, Head]
// window, and a successful ApplyBatch advances the cursor. applied receives
// only successfully committed batches, so a batch observed there implies the
// cursor advanced.
type fakeAuditStore struct {
	mu           sync.Mutex
	streams      []audit.Stream
	events       []session.SessionEvent
	cursors      map[string]int64
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
	pending := make([]audit.Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		stream.Committed = s.cursors[stream.Actor.SessionID]
		if stream.Head > stream.Committed {
			pending = append(pending, stream)
		}
	}
	return pending, nil
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
	window := make([]session.SessionEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.Actor.SessionID == stream.Actor.SessionID &&
			event.Sequence > stream.Committed && event.Sequence <= stream.Head {
			window = append(window, event)
		}
	}
	return window, nil
}

func (s *fakeAuditStore) ApplyBatch(_ context.Context, batch audit.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.applyErr != nil {
		return s.applyErr
	}
	s.committed++
	if batch.Cursor > s.cursors[batch.Actor.SessionID] {
		s.cursors[batch.Actor.SessionID] = batch.Cursor
	}
	s.applied <- batch
	return nil
}

// appendEvent adds one event to the backing log and moves the matching
// stream head forward, simulating a stream that gained an event between
// two scans.
func (s *fakeAuditStore) appendEvent(event session.SessionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	for i := range s.streams {
		if s.streams[i].Actor.SessionID == event.Actor.SessionID &&
			s.streams[i].Head < event.Sequence {
			s.streams[i].Head = event.Sequence
		}
	}
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
