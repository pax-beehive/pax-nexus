package sessionconsumer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/require"
)

type backoffStore struct {
	streams []Stream
	events  []session.SessionEvent
}

func (s *backoffStore) AutoInjectEnabled(context.Context, string) (bool, error)  { return true, nil }
func (s *backoffStore) SetAutoInjectEnabled(context.Context, string, bool) error { return nil }
func (s *backoffStore) PendingStreams(context.Context) ([]Stream, error) {
	return append([]Stream(nil), s.streams...), nil
}
func (s *backoffStore) StreamsBySessionID(
	_ context.Context, scopeID string, sessionID string,
) ([]Stream, error) {
	result := make([]Stream, 0)
	for _, stream := range s.streams {
		if stream.ScopeID == scopeID && stream.Actor.SessionID == sessionID {
			result = append(result, stream)
		}
	}
	return result, nil
}
func (s *backoffStore) SessionEvents(context.Context, Stream) ([]session.SessionEvent, error) {
	return append([]session.SessionEvent(nil), s.events...), nil
}
func (s *backoffStore) AdvanceCursor(context.Context, Stream) error { return nil }
func (s *backoffStore) Progress(context.Context, string) (Progress, error) {
	return Progress{}, nil
}

// flakyInjector's fields are guarded by mu: several backoff tests give two
// scopes the same injector instance, and Task 5's per-scope jobs can now
// call InjectSession concurrently from different goroutines.
type flakyInjector struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (i *flakyInjector) InjectSession(
	context.Context, pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	i.mu.Lock()
	i.calls++
	err := i.err
	i.mu.Unlock()
	if err != nil {
		return pagewiki.InjectResult{}, err
	}
	return pagewiki.InjectResult{
		Run: pagewiki.MaintenanceRun{Status: pagewiki.RunStatusSucceeded},
	}, nil
}

type noopRebuilder struct{}

func (noopRebuilder) RebuildPageWiki(context.Context, string, string, string, time.Time) error {
	return nil
}

// scanSync runs one dispatch tick and waits for every job to finish. Task 5
// replaced the old synchronous scan pass with async per-scope jobs; these
// pre-pool backoff tests want the old deterministic call-count semantics, so
// this helper restores them without touching the tests' assertions.
func (c *Controller) scanSync(ctx context.Context) {
	c.tick(ctx)
	c.jobs.Wait()
}

func newBackoffFixture(t *testing.T) (*backoffStore, *flakyInjector, *Controller, *time.Time) {
	t.Helper()
	store := &backoffStore{
		streams: []Stream{{
			ScopeID: "local-team",
			Actor:   session.Actor{AgentID: "agent-1", SessionID: "session-1"},
			Head:    2,
		}},
		events: []session.SessionEvent{
			{ID: "event-1", Sequence: 1, Type: "assistant", Content: "hello"},
		},
	}
	injector := &flakyInjector{err: errors.New("planner down")}
	controller, err := New(
		store,
		func(context.Context, string) (Injector, error) { return injector, nil },
		func(context.Context, string) (Rebuilder, error) { return noopRebuilder{}, nil },
		slog.New(slog.DiscardHandler), time.Second,
	)
	require.NoError(t, err)
	now := time.Unix(1_000_000, 0)
	controller.now = func() time.Time { return now }
	return store, injector, controller, &now
}

func TestScanSkipsFailingStreamUntilBackoffExpires(t *testing.T) {
	_, injector, controller, now := newBackoffFixture(t)
	ctx := context.Background()

	controller.scanSync(ctx) // attempt 1: delay = 1s << 1 = 2s
	require.Equal(t, 1, injector.calls)

	controller.scanSync(ctx) // inside the 2s window
	require.Equal(t, 1, injector.calls)

	*now = now.Add(3 * time.Second)
	controller.scanSync(ctx) // attempt 2: delay = 1s << 2 = 4s
	require.Equal(t, 2, injector.calls)

	*now = now.Add(3 * time.Second)
	controller.scanSync(ctx) // still inside the 4s window
	require.Equal(t, 2, injector.calls)

	*now = now.Add(2 * time.Second)
	controller.scanSync(ctx)
	require.Equal(t, 3, injector.calls)
}

func TestHeadAdvanceResetsBackoff(t *testing.T) {
	store, injector, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scanSync(ctx)
	require.Equal(t, 1, injector.calls)

	store.streams[0].Head = 3 // new events arrived
	controller.scanSync(ctx)  // no clock movement needed
	require.Equal(t, 2, injector.calls)
}

func TestSuccessClearsBackoffAndAttemptsRestart(t *testing.T) {
	_, injector, controller, now := newBackoffFixture(t)
	ctx := context.Background()

	controller.scanSync(ctx)
	controller.scanSync(ctx)
	require.Equal(t, 1, injector.calls)

	injector.err = nil
	*now = now.Add(time.Hour)
	controller.scanSync(ctx)
	require.Equal(t, 2, injector.calls)
	require.Empty(t, controller.failures)

	injector.err = errors.New("planner down again")
	controller.scanSync(ctx)
	require.Equal(t, 3, injector.calls)
	record := controller.failures["local-team/agent-1/session-1"]
	require.Equal(t, 1, record.attempts, "attempts must restart after a success")
}

func TestManualInjectBypassesAndClearsBackoff(t *testing.T) {
	_, injector, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scanSync(ctx) // stream now backed off
	require.Equal(t, 1, injector.calls)

	injector.err = nil
	_, err := controller.InjectSession(ctx, "local-team", "session-1")
	require.NoError(t, err)
	require.Equal(t, 2, injector.calls, "manual inject must ignore the backoff window")
	require.Empty(t, controller.failures)
}

func TestRebuildClearsAllBackoff(t *testing.T) {
	_, _, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scanSync(ctx)
	require.NotEmpty(t, controller.failures)

	_, err := controller.Rebuild(ctx, "local-team", time.Time{})
	require.NoError(t, err)
	require.NotEmpty(t, controller.failures, "queueing alone must not clear backoff")

	controller.maybeRebuild(ctx)
	require.Empty(t, controller.failures)
}

// TestRebuildClearsOnlyItsOwnScopeBackoff pins that a rebuild is a per-scope
// event: it clears the scope it rebuilt and leaves every other tenant's
// retry window intact.
func TestRebuildClearsOnlyItsOwnScopeBackoff(t *testing.T) {
	store, _, controller, _ := newBackoffFixture(t)
	store.streams = append(store.streams, Stream{
		ScopeID: "other-team",
		Actor:   session.Actor{AgentID: "agent-2", SessionID: "session-2"},
		Head:    4,
	})
	ctx := context.Background()

	controller.scanSync(ctx)
	require.Contains(t, controller.failures, "local-team/agent-1/session-1")
	require.Contains(t, controller.failures, "other-team/agent-2/session-2")

	_, err := controller.Rebuild(ctx, "local-team", time.Time{})
	require.NoError(t, err)
	controller.maybeRebuild(ctx)

	require.NotContains(t, controller.failures, "local-team/agent-1/session-1",
		"the rebuilt scope's backoff must be cleared")
	require.Contains(t, controller.failures, "other-team/agent-2/session-2",
		"another scope's backoff must survive a rebuild it did not ask for")
}

// TestUnresolvableScopeBacksOffWithoutStoppingTheScan pins that a scope
// whose injector cannot be resolved fails like any other injection failure:
// it backs off, and the rest of the sweep still runs.
func TestUnresolvableScopeBacksOffWithoutStoppingTheScan(t *testing.T) {
	store := &backoffStore{
		streams: []Stream{
			{
				ScopeID: "broken-team",
				Actor:   session.Actor{AgentID: "agent-1", SessionID: "session-1"},
				Head:    2,
			},
			{
				ScopeID: "healthy-team",
				Actor:   session.Actor{AgentID: "agent-2", SessionID: "session-2"},
				Head:    3,
			},
		},
		events: []session.SessionEvent{
			{ID: "event-1", Sequence: 1, Type: "assistant", Content: "hello"},
		},
	}
	injector := &flakyInjector{}
	controller, err := New(
		store,
		func(_ context.Context, scopeID string) (Injector, error) {
			if scopeID == "broken-team" {
				return nil, errors.New("scope is not provisioned")
			}
			return injector, nil
		},
		func(context.Context, string) (Rebuilder, error) { return noopRebuilder{}, nil },
		slog.New(slog.DiscardHandler), time.Second,
	)
	require.NoError(t, err)
	ctx := context.Background()

	controller.scanSync(ctx)

	require.Equal(t, 1, injector.calls, "the resolvable scope must still be injected")
	require.Contains(t, controller.failures, "broken-team/agent-1/session-1")
	require.NotContains(t, controller.failures, "healthy-team/agent-2/session-2")
	require.Equal(t, 1, controller.failures["broken-team/agent-1/session-1"].attempts)
}

// TestManualInjectSurfacesResolutionFailure pins that a manual inject for an
// unresolvable scope returns the error instead of panicking on a nil
// injector.
func TestManualInjectSurfacesResolutionFailure(t *testing.T) {
	store := &backoffStore{
		streams: []Stream{{
			ScopeID: "broken-team",
			Actor:   session.Actor{AgentID: "agent-1", SessionID: "session-1"},
			Head:    2,
		}},
		events: []session.SessionEvent{
			{ID: "event-1", Sequence: 1, Type: "assistant", Content: "hello"},
		},
	}
	controller, err := New(
		store,
		func(context.Context, string) (Injector, error) {
			return nil, errors.New("scope is not provisioned")
		},
		func(context.Context, string) (Rebuilder, error) { return noopRebuilder{}, nil },
		slog.New(slog.DiscardHandler), time.Second,
	)
	require.NoError(t, err)

	_, err = controller.InjectSession(context.Background(), "broken-team", "session-1")

	require.ErrorContains(t, err, "scope is not provisioned")
}

// TestUnresolvableRebuilderFailsOnlyThatScope pins that a rebuild whose
// rebuilder cannot be resolved lands in that scope's failed state without
// disturbing another scope's queued rebuild.
func TestUnresolvableRebuilderFailsOnlyThatScope(t *testing.T) {
	store := &backoffStore{}
	rebuilt := make([]string, 0, 1)
	controller, err := New(
		store,
		func(context.Context, string) (Injector, error) { return &flakyInjector{}, nil },
		func(_ context.Context, scopeID string) (Rebuilder, error) {
			if scopeID == "broken-team" {
				return nil, errors.New("scope is not provisioned")
			}
			return recordingNoopRebuilder{recorded: &rebuilt}, nil
		},
		slog.New(slog.DiscardHandler), time.Second,
	)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = controller.Rebuild(ctx, "broken-team", time.Time{})
	require.NoError(t, err)
	_, err = controller.Rebuild(ctx, "healthy-team", time.Time{})
	require.NoError(t, err)

	controller.maybeRebuild(ctx)

	broken, err := controller.Status(ctx, "broken-team")
	require.NoError(t, err)
	require.Equal(t, RebuildFailed, broken.Rebuild.State)
	require.Contains(t, broken.Rebuild.Error, "scope is not provisioned")
	healthy, err := controller.Status(ctx, "healthy-team")
	require.NoError(t, err)
	require.Equal(t, RebuildIdle, healthy.Rebuild.State)
	require.NotNil(t, healthy.Rebuild.FinishedAt)
	require.Equal(t, []string{"healthy-team"}, rebuilt)
}

type recordingNoopRebuilder struct {
	recorded *[]string
}

func (r recordingNoopRebuilder) RebuildPageWiki(
	_ context.Context, scopeID string, _ string, _ string, _ time.Time,
) error {
	*r.recorded = append(*r.recorded, scopeID)
	return nil
}

func TestBackoffDelayIsCapped(t *testing.T) {
	controller := &Controller{interval: 2 * time.Second}
	require.Equal(t, 4*time.Second, controller.backoffDelay(1))
	require.Equal(t, 8*time.Second, controller.backoffDelay(2))
	require.Equal(t, failureBackoffCap, controller.backoffDelay(10))
	require.Equal(t, failureBackoffCap, controller.backoffDelay(64))
}
