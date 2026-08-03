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
	resolver  *scopeResolver
	consumer  *sessionconsumer.Controller
}

// scopeResolver stands in for the Page Wiki service/repository managers: it
// hands every scope the suite's shared fakes unless a test registers a
// per-scope override or a resolution failure.
type scopeResolver struct {
	mu          sync.Mutex
	injector    *recordingInjector
	rebuilder   *recordingRebuilder
	injectors   map[string]*recordingInjector
	injectorErr map[string]error
}

func (r *scopeResolver) injectorFor(_ context.Context, scopeID string) (sessionconsumer.Injector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, found := r.injectorErr[scopeID]; found {
		return nil, err
	}
	if injector, found := r.injectors[scopeID]; found {
		return injector, nil
	}
	return r.injector, nil
}

func (r *scopeResolver) rebuilderFor(_ context.Context, _ string) (sessionconsumer.Rebuilder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuilder, nil
}

func (r *scopeResolver) setInjector(scopeID string, injector *recordingInjector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.injectors[scopeID] = injector
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
	s.resolver = &scopeResolver{
		injector: s.injector, rebuilder: s.rebuilder,
		injectors:   map[string]*recordingInjector{},
		injectorErr: map[string]error{},
	}
	var err error
	s.consumer, err = sessionconsumer.New(
		s.store, s.resolver.injectorFor, s.resolver.rebuilderFor,
		slog.New(slog.DiscardHandler), time.Hour,
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

func (s *consumerSuite) TestSetAutoInjectSurfacesQueuedRebuildState() {
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	_, err := s.consumer.Rebuild(context.Background(), "local-team", cutoff)
	s.Require().NoError(err)

	// The consumer is never Started, so the rebuild stays queued: this
	// isolates SetAutoInject's own reporting from the background loop.
	status, err := s.consumer.SetAutoInject(context.Background(), "local-team", true)

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
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

// TestQueuedRebuildsRunOncePerScope pins that a second scope's rebuild is
// not swallowed by a first scope's queued one: each scope keeps its own
// slot, its own cutoff, and reaches its own terminal state.
func (s *consumerSuite) TestQueuedRebuildsRunOncePerScope() {
	firstCutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	secondCutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	first, err := s.consumer.Rebuild(context.Background(), "scope-a", firstCutoff)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, first.Rebuild.State)
	second, err := s.consumer.Rebuild(context.Background(), "scope-b", secondCutoff)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, second.Rebuild.State)

	s.consumer.RunQueuedRebuildForTest(context.Background())

	scopeACalls := s.rebuilder.callsForScope("scope-a")
	scopeBCalls := s.rebuilder.callsForScope("scope-b")
	s.Require().Len(scopeACalls, 1, "scope-a must rebuild exactly once")
	s.Require().Len(scopeBCalls, 1, "scope-b's rebuild must not be swallowed by scope-a's")
	s.Equal(firstCutoff, scopeACalls[0].since)
	s.Equal(secondCutoff, scopeBCalls[0].since)
	for _, scopeID := range []string{"scope-a", "scope-b"} {
		status, statusErr := s.consumer.Status(context.Background(), scopeID)
		s.Require().NoError(statusErr)
		s.Equal(sessionconsumer.RebuildIdle, status.Rebuild.State, scopeID)
		s.Require().NotNil(status.Rebuild.FinishedAt, scopeID)
	}
}

// TestSecondScopeQueuesWhileFirstScopeRebuildRuns pins the running half of
// the same bug: while scope-a's rebuild executes, scope-b's request must
// queue (and later run) instead of reading back scope-a's running state.
func (s *consumerSuite) TestSecondScopeQueuesWhileFirstScopeRebuildRuns() {
	s.rebuilder.entered = make(chan struct{}, 2)
	s.rebuilder.release = make(chan struct{})
	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	_, err := s.consumer.Rebuild(context.Background(), "scope-a", time.Time{})
	s.Require().NoError(err)
	firstPass := make(chan struct{})
	go func() {
		defer close(firstPass)
		s.consumer.RunQueuedRebuildForTest(context.Background())
	}()
	select {
	case <-s.rebuilder.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a rebuild did not start")
		return
	}

	queued, err := s.consumer.Rebuild(context.Background(), "scope-b", cutoff)

	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, queued.Rebuild.State)
	running, err := s.consumer.Status(context.Background(), "scope-a")
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildRunning, running.Rebuild.State)
	close(s.rebuilder.release)
	<-firstPass
	s.consumer.RunQueuedRebuildForTest(context.Background())
	scopeBCalls := s.rebuilder.callsForScope("scope-b")
	s.Require().Len(scopeBCalls, 1, "scope-b's rebuild must run after scope-a's")
	s.Equal(cutoff, scopeBCalls[0].since)
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

// TestOneScanConsumesEveryScope pins the multi-scope sweep: one loop drains
// the pending streams of every scope, each through its own injector.
func (s *consumerSuite) TestOneScanConsumesEveryScope() {
	otherInjector := &recordingInjector{}
	s.resolver.setInjector("other-team", otherInjector)
	s.store.streams = []sessionconsumer.Stream{
		{
			ScopeID: "local-team",
			Actor:   session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "runtime-demo"},
			Head:    2,
		},
		{
			ScopeID: "other-team",
			Actor:   session.Actor{UserID: "owner", AgentID: "agent-2", SessionID: "other-demo"},
			Head:    7,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.consumer.Start(ctx)

	s.Eventually(func() bool {
		return s.injector.requestCount() == 1 && otherInjector.requestCount() == 1
	}, time.Second, 10*time.Millisecond)
	s.Equal([]string{"session:local-team:agent-1:runtime-demo"}, s.injector.sourceIDs())
	s.Equal([]string{"session:other-team:agent-2:other-demo"}, otherInjector.sourceIDs())
	scopeID, err := otherInjector.scopeAt(0)
	s.Require().NoError(err)
	s.Equal("other-team", scopeID)
}

// TestManualInjectionOfOneScopeDoesNotWaitForAnother pins the per-scope
// lock split: while scope-a's injection sits inside a slow LLM call,
// scope-b's manual injection must complete instead of queueing behind a
// process-global mutex.
func (s *consumerSuite) TestManualInjectionOfOneScopeDoesNotWaitForAnother() {
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
		{ScopeID: "scope-b", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-b"}, Head: 2},
	}
	blocked := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.resolver.setInjector("scope-a", blocked)

	go func() { _, _ = s.consumer.InjectSession(context.Background(), "scope-a", "session-a") }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a injection did not start")
		return
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.consumer.InjectSession(context.Background(), "scope-b", "session-b")
		done <- err
	}()
	select {
	case err := <-done:
		s.Require().NoError(err)
	case <-time.After(time.Second):
		s.Fail("scope-b's manual injection blocked behind scope-a's")
	}
	close(blocked.release)
}

// TestRebuildOfOneScopeDoesNotWaitForAnotherScopesInjection pins the same
// split for rebuilds: scope-b's queued rebuild must execute while scope-a
// holds its own injection lock.
func (s *consumerSuite) TestRebuildOfOneScopeDoesNotWaitForAnotherScopesInjection() {
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
	}
	blocked := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.resolver.setInjector("scope-a", blocked)
	go func() { _, _ = s.consumer.InjectSession(context.Background(), "scope-a", "session-a") }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a injection did not start")
		return
	}

	_, err := s.consumer.Rebuild(context.Background(), "scope-b", time.Time{})
	s.Require().NoError(err)
	rebuildDone := make(chan struct{})
	go func() {
		s.consumer.RunQueuedRebuildForTest(context.Background())
		close(rebuildDone)
	}()
	select {
	case <-rebuildDone:
		s.Require().Len(s.rebuilder.callsForScope("scope-b"), 1)
	case <-time.After(time.Second):
		s.Fail("scope-b's rebuild blocked behind scope-a's injection")
	}
	close(blocked.release)
}

func (s *consumerSuite) TestValidationRejectsIncompleteConfigurationAndInput() {
	injectorFor, rebuilderFor := s.resolver.injectorFor, s.resolver.rebuilderFor
	_, err := sessionconsumer.New(nil, injectorFor, rebuilderFor, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, nil, rebuilderFor, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, injectorFor, nil, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, injectorFor, rebuilderFor, nil, time.Second)
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

// rebuildCall is one recorded RebuildPageWiki invocation. Calls are kept in
// order so multi-scope tests can assert what each scope's rebuild was given.
type rebuildCall struct {
	scopeID          string
	processorName    string
	processorVersion string
	since            time.Time
}

type recordingRebuilder struct {
	mu      sync.Mutex
	calls   []rebuildCall
	err     error
	done    chan struct{}
	log     *eventLog
	entered chan struct{} // when non-nil, signaled as each call starts
	release chan struct{} // when non-nil, each call blocks on one receive
}

func (r *recordingRebuilder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingRebuilder) lastCall() (string, string, string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return "", "", "", time.Time{}
	}
	call := r.calls[len(r.calls)-1]
	return call.scopeID, call.processorName, call.processorVersion, call.since
}

func (r *recordingRebuilder) callsForScope(scopeID string) []rebuildCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := make([]rebuildCall, 0, len(r.calls))
	for _, call := range r.calls {
		if call.scopeID == scopeID {
			matched = append(matched, call)
		}
	}
	return matched
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

func (i *recordingInjector) sourceIDs() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	ids := make([]string, 0, len(i.requests))
	for _, request := range i.requests {
		ids = append(ids, request.SourceID)
	}
	return ids
}

func (i *recordingInjector) scopeAt(index int) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return session.ScopeFromContext(i.contexts[index])
}

func (r *recordingRebuilder) RebuildPageWiki(
	_ context.Context,
	scopeID string,
	processorName string,
	processorVersion string,
	since time.Time,
) error {
	r.mu.Lock()
	r.calls = append(r.calls, rebuildCall{
		scopeID:          scopeID,
		processorName:    processorName,
		processorVersion: processorVersion,
		since:            since,
	})
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
