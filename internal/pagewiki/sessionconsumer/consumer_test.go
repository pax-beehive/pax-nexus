package sessionconsumer_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type consumerSuite struct {
	suite.Suite
	store     *consumerStore
	injector  *recordingInjector
	rebuilder *recordingRebuilder
	consumer  *sessionconsumer.Controller
}

func TestConsumerSuite(t *testing.T) {
	suite.Run(t, new(consumerSuite))
}

func (s *consumerSuite) SetupTest() {
	s.store = &consumerStore{
		enabled: map[string]bool{},
		streams: []sessionconsumer.Stream{{
			ScopeID: "local-team",
			Actor:   session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "runtime-demo"},
			Head:    2,
		}},
		events: []session.SessionEvent{
			{ID: "event-1", Sequence: 1, Type: "assistant", Content: "Runtime started."},
			{ID: "event-2", Sequence: 2, Type: "assistant", Content: "Runtime verified."},
		},
		advanced: make(chan struct{}, 1),
	}
	s.injector = &recordingInjector{}
	s.rebuilder = &recordingRebuilder{done: make(chan struct{}, 4)}
	var err error
	s.consumer, err = sessionconsumer.New(
		s.store, s.injector, s.rebuilder, slog.New(slog.DiscardHandler), time.Hour,
	)
	s.Require().NoError(err)
}

func (s *consumerSuite) TestManualInjectionBuildsCitedSourceAndAdvancesIndependentCursor() {
	result, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().NoError(err)
	s.Equal(1, result.ProcessedStreams)
	s.Equal(1, s.store.advances)
	s.Require().Len(s.injector.requests, 1)
	request := s.injector.requests[0]
	s.Equal("page_wiki/knowledge-llm-v1/2", request.IdempotencyKey)
	s.Len(request.Events, 2)
	s.Contains(string(request.Raw), "[event:event-2 sequence:2 type:assistant] Runtime verified.")
}

func (s *consumerSuite) TestFailedInjectionDoesNotAdvanceCursor() {
	s.injector.err = errors.New("planner unavailable")

	_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().ErrorContains(err, "planner unavailable")
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestInjectionContextCarriesStreamScope() {
	_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().NoError(err)
	s.Require().Len(s.injector.contexts, 1)
	scopeID, err := session.ScopeFromContext(s.injector.contexts[0])
	s.Require().NoError(err)
	s.Equal("local-team", scopeID)
}

func (s *consumerSuite) TestAutoSettingRoundTrips() {
	status, err := s.consumer.SetAutoInject(context.Background(), "local-team", true)
	s.Require().NoError(err)
	s.True(status.AutoInject)

	status, err = s.consumer.Status(context.Background(), "local-team")
	s.Require().NoError(err)
	s.True(status.AutoInject)
}

func (s *consumerSuite) TestRebuildQueuesAndRunsInBackground() {
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	status, err := s.consumer.Rebuild(context.Background(), "local-team", cutoff)

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	s.Zero(s.rebuilder.callCount())

	log := &eventLog{}
	s.injector.log = log
	s.rebuilder.log = log
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)

	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("background rebuild did not run")
		return
	}
	scopeID, name, version, since := s.rebuilder.lastCall()
	s.Equal("local-team", scopeID)
	s.Equal(sessionconsumer.ProcessorName, name)
	s.Equal(sessionconsumer.ProcessorVersion, version)
	s.Equal(cutoff, since)
	entries := log.snapshot()
	s.Require().NotEmpty(entries)
	s.Equal("rebuild", entries[0], "tick must run a queued rebuild before scanning")
	s.Eventually(func() bool {
		st, statusErr := s.consumer.Status(context.Background(), "local-team")
		return statusErr == nil &&
			st.Rebuild.State == sessionconsumer.RebuildIdle &&
			st.Rebuild.FinishedAt != nil
	}, time.Second, 10*time.Millisecond)
}

func (s *consumerSuite) TestRebuildReturnsImmediatelyAndMergesWhileInjectionHoldsLock() {
	s.injector.entered = make(chan struct{}, 4)
	s.injector.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	select {
	case <-s.injector.entered:
	case <-time.After(time.Second):
		s.Fail("injection did not start")
		return
	}

	// The scan goroutine now holds c.mu inside InjectSession. Rebuild must
	// not touch that lock: if it did, these calls would hang and the suite
	// would time out.
	first := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	status, err := s.consumer.Rebuild(context.Background(), "local-team", first)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	status, err = s.consumer.Rebuild(context.Background(), "local-team", second)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)

	// Verify Status() does not block behind the injection lock (F3+F4 fix).
	statusDone := make(chan sessionconsumer.Status, 1)
	go func() {
		st, statusErr := s.consumer.Status(context.Background(), "local-team")
		if statusErr == nil {
			statusDone <- st
		}
	}()
	select {
	case st := <-statusDone:
		s.Equal(sessionconsumer.RebuildQueued, st.Rebuild.State)
	case <-time.After(time.Second):
		s.Fail("Status blocked behind the injection lock")
		return
	}

	close(s.injector.release)
	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("background rebuild did not run")
		return
	}
	s.Equal(1, s.rebuilder.callCount())
	_, _, _, since := s.rebuilder.lastCall()
	s.Equal(first, since) // the merged second request's since was discarded
}

func (s *consumerSuite) TestBackgroundRebuildFailureSurfacesInStatusAndCanRequeue() {
	s.rebuilder.err = errors.New("rebuild unavailable")
	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)

	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("background rebuild did not run")
		return
	}

	status, statusErr := s.consumer.Status(context.Background(), "local-team")
	s.Require().NoError(statusErr)
	s.Equal(sessionconsumer.RebuildFailed, status.Rebuild.State)
	s.Contains(status.Rebuild.Error, "rebuild unavailable")

	status, rebuildErr := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(rebuildErr)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	s.Empty(status.Rebuild.Error)
}

func (s *consumerSuite) TestSuccessfulRebuildClearsInjectionBackoff() {
	s.injector.err = errors.New("planner unavailable")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	s.Eventually(func() bool {
		return s.injector.requestCount() > 0
	}, time.Second, 10*time.Millisecond)

	s.injector.mu.Lock()
	s.injector.err = nil
	s.injector.mu.Unlock()
	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)

	// Without the failures reset the stream stays backed off for
	// interval<<1 = 2h (interval is time.Hour in SetupTest); the
	// post-rebuild scan must inject immediately instead.
	select {
	case <-s.store.advanced:
	case <-time.After(time.Second):
		s.Fail("injection did not resume after rebuild cleared backoff")
		return
	}
}

func (s *consumerSuite) TestStatusReportsRunningWhileRebuildExecutes() {
	s.rebuilder.entered = make(chan struct{}, 1)
	s.rebuilder.release = make(chan struct{})
	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	select {
	case <-s.rebuilder.entered:
	case <-time.After(time.Second):
		s.Fail("rebuild did not start")
		return
	}

	status, err := s.consumer.Status(context.Background(), "local-team")
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildRunning, status.Rebuild.State)

	close(s.rebuilder.release)
	s.Eventually(func() bool {
		st, statusErr := s.consumer.Status(context.Background(), "local-team")
		return statusErr == nil && st.Rebuild.State == sessionconsumer.RebuildIdle
	}, time.Second, 10*time.Millisecond)
}

func (s *consumerSuite) TestRejectsMissingSession() {
	_, err := s.consumer.InjectSession(context.Background(), "local-team", "missing")

	s.Require().ErrorIs(err, sessionconsumer.ErrSessionNotFound)
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestStartsBackgroundScanAndConsumesPendingStream() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.consumer.Start(ctx)

	select {
	case <-s.store.advanced:
	case <-time.After(time.Second):
		s.Fail("background consumer did not advance")
	}
}

func (s *consumerSuite) TestValidationRejectsIncompleteConfigurationAndInput() {
	_, err := sessionconsumer.New(nil, s.injector, s.rebuilder, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, nil, s.rebuilder, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, s.injector, nil, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, s.injector, s.rebuilder, nil, time.Second)
	s.Require().Error(err)

	_, err = s.consumer.SetAutoInject(context.Background(), "", true)
	s.Require().Error(err)
	_, err = s.consumer.InjectSession(context.Background(), "", "runtime-demo")
	s.Require().Error(err)
	_, err = s.consumer.InjectSession(context.Background(), "local-team", "")
	s.Require().Error(err)
	_, err = s.consumer.Rebuild(context.Background(), "", time.Time{})
	s.Require().Error(err)
}

func (s *consumerSuite) TestEmptySessionDoesNotInjectOrAdvance() {
	s.store.events = nil

	result, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().NoError(err)
	s.Equal(1, result.ProcessedStreams)
	s.Empty(s.injector.requests)
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestStatusIncludesProgress() {
	s.store.enabled["local-team"] = true
	processed := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	s.store.progress = sessionconsumer.Progress{PendingSessions: 3, LastProcessedAt: &processed}

	status, err := s.consumer.Status(context.Background(), "local-team")

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Require().NotNil(status.Progress)
	s.Equal(3, status.Progress.PendingSessions)
	s.Equal(processed, *status.Progress.LastProcessedAt)
}

func (s *consumerSuite) TestStatusDegradesWhenProgressQueryFails() {
	s.store.enabled["local-team"] = true
	s.store.progressErr = errors.New("progress query failed")

	status, err := s.consumer.Status(context.Background(), "local-team")

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Nil(status.Progress)
}

func (s *consumerSuite) TestDoesNotAdvanceWhenMaintenanceRunIsNotSuccessful() {
	s.injector.status = pagewiki.RunStatusPartialSuccess

	_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().ErrorContains(err, "partial_success")
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestStoreFailuresAreReported() {
	tests := []struct {
		name      string
		configure func()
		run       func() error
		contains  string
	}{
		{
			name: "read status",
			configure: func() {
				s.store.statusErr = errors.New("status unavailable")
			},
			run: func() error {
				_, err := s.consumer.Status(context.Background(), "local-team")
				return err
			},
			contains: "status unavailable",
		},
		{
			name: "write status",
			configure: func() {
				s.store.settingErr = errors.New("setting unavailable")
			},
			run: func() error {
				_, err := s.consumer.SetAutoInject(context.Background(), "local-team", true)
				return err
			},
			contains: "setting unavailable",
		},
		{
			name: "list session",
			configure: func() {
				s.store.streamErr = errors.New("stream unavailable")
			},
			run: func() error {
				_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")
				return err
			},
			contains: "stream unavailable",
		},
		{
			name: "read events",
			configure: func() {
				s.store.eventsErr = errors.New("events unavailable")
			},
			run: func() error {
				_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")
				return err
			},
			contains: "events unavailable",
		},
		{
			name: "advance cursor",
			configure: func() {
				s.store.advanceErr = errors.New("cursor unavailable")
			},
			run: func() error {
				_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")
				return err
			},
			contains: "cursor unavailable",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.SetupTest()
			test.configure()

			err := test.run()

			s.Require().ErrorContains(err, test.contains)
		})
	}
}

type consumerStore struct {
	enabled     map[string]bool
	streams     []sessionconsumer.Stream
	events      []session.SessionEvent
	advances    int
	statusErr   error
	settingErr  error
	streamErr   error
	pendingErr  error
	eventsErr   error
	advanceErr  error
	advanced    chan struct{}
	progress    sessionconsumer.Progress
	progressErr error
}

func (s *consumerStore) AutoInjectEnabled(_ context.Context, scopeID string) (bool, error) {
	if s.statusErr != nil {
		return false, s.statusErr
	}
	return s.enabled[scopeID], nil
}

func (s *consumerStore) SetAutoInjectEnabled(_ context.Context, scopeID string, enabled bool) error {
	if s.settingErr != nil {
		return s.settingErr
	}
	s.enabled[scopeID] = enabled
	return nil
}

func (s *consumerStore) PendingStreams(context.Context) ([]sessionconsumer.Stream, error) {
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	return append([]sessionconsumer.Stream(nil), s.streams...), nil
}

func (s *consumerStore) StreamsBySessionID(
	_ context.Context,
	scopeID string,
	sessionID string,
) ([]sessionconsumer.Stream, error) {
	if s.streamErr != nil {
		return nil, s.streamErr
	}
	result := make([]sessionconsumer.Stream, 0)
	for _, stream := range s.streams {
		if stream.ScopeID == scopeID && stream.Actor.SessionID == sessionID {
			result = append(result, stream)
		}
	}
	return result, nil
}

func (s *consumerStore) SessionEvents(
	context.Context,
	sessionconsumer.Stream,
) ([]session.SessionEvent, error) {
	if s.eventsErr != nil {
		return nil, s.eventsErr
	}
	return append([]session.SessionEvent(nil), s.events...), nil
}

func (s *consumerStore) AdvanceCursor(context.Context, sessionconsumer.Stream) error {
	if s.advanceErr != nil {
		return s.advanceErr
	}
	s.advances++
	select {
	case s.advanced <- struct{}{}:
	default:
	}
	return nil
}

func (s *consumerStore) Progress(context.Context, string) (sessionconsumer.Progress, error) {
	if s.progressErr != nil {
		return sessionconsumer.Progress{}, s.progressErr
	}
	return s.progress, nil
}

// eventLog records cross-fake ordering so tests can assert what ran first.
type eventLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *eventLog) add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

type recordingRebuilder struct {
	mu               sync.Mutex
	calls            int
	scopeID          string
	processorName    string
	processorVersion string
	since            time.Time
	err              error
	done             chan struct{}
	log              *eventLog
	entered          chan struct{} // when non-nil, signaled as each call starts
	release          chan struct{} // when non-nil, each call blocks on one receive
}

func (r *recordingRebuilder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingRebuilder) lastCall() (string, string, string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scopeID, r.processorName, r.processorVersion, r.since
}

type recordingInjector struct {
	mu       sync.Mutex
	requests []pagewiki.InjectSessionRequest
	contexts []context.Context
	err      error
	status   pagewiki.RunStatus
	entered  chan struct{} // when non-nil, signaled as each call starts
	release  chan struct{} // when non-nil, each call blocks on one receive
	log      *eventLog
}

func (i *recordingInjector) requestCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.requests)
}

func (r *recordingRebuilder) RebuildPageWiki(
	_ context.Context,
	scopeID string,
	processorName string,
	processorVersion string,
	since time.Time,
) error {
	r.mu.Lock()
	r.calls++
	r.scopeID = scopeID
	r.processorName = processorName
	r.processorVersion = processorVersion
	r.since = since
	err := r.err
	r.mu.Unlock()
	if r.log != nil {
		r.log.add("rebuild")
	}
	if r.entered != nil {
		r.entered <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	select {
	case r.done <- struct{}{}:
	default:
	}
	return err
}

func (s *consumerSuite) TestScanYieldsToQueuedRebuildBetweenStreams() {
	log := &eventLog{}
	s.injector.log = log
	s.rebuilder.log = log
	s.store.streams = append(s.store.streams, sessionconsumer.Stream{
		ScopeID: "local-team",
		Actor:   session.Actor{UserID: "owner", AgentID: "agent-2", SessionID: "second-demo"},
		Head:    5,
	})
	s.injector.entered = make(chan struct{}, 8)
	s.injector.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	select {
	case <-s.injector.entered:
	case <-time.After(time.Second):
		s.Fail("first injection did not start")
		return
	}

	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)
	close(s.injector.release) // let the in-flight stream finish and unblock any future receives

	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("rebuild did not run after the in-flight stream")
		return
	}
	// scan yielded after the first stream: exactly one injection happened
	// before the rebuild.
	s.Equal([]string{"inject", "rebuild"}, log.snapshot()[:2])
}

func (i *recordingInjector) InjectSession(
	ctx context.Context,
	request pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	i.mu.Lock()
	i.requests = append(i.requests, request)
	i.contexts = append(i.contexts, ctx)
	entered, release, err, status := i.entered, i.release, i.err, i.status
	i.mu.Unlock()
	if i.log != nil {
		i.log.add("inject")
	}
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return pagewiki.InjectResult{}, err
	}
	if status == "" {
		status = pagewiki.RunStatusSucceeded
	}
	return pagewiki.InjectResult{
		Run: pagewiki.MaintenanceRun{Status: status},
	}, nil
}
