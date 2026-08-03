package sessionconsumer

import (
	"context"
	"errors"
	"log/slog"
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

type flakyInjector struct {
	err   error
	calls int
}

func (i *flakyInjector) InjectSession(
	context.Context, pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	i.calls++
	if i.err != nil {
		return pagewiki.InjectResult{}, i.err
	}
	return pagewiki.InjectResult{
		Run: pagewiki.MaintenanceRun{Status: pagewiki.RunStatusSucceeded},
	}, nil
}

type noopRebuilder struct{}

func (noopRebuilder) RebuildPageWiki(context.Context, string, string, string, time.Time) error {
	return nil
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
	controller, err := New(store, injector, noopRebuilder{}, slog.New(slog.DiscardHandler), time.Second)
	require.NoError(t, err)
	now := time.Unix(1_000_000, 0)
	controller.now = func() time.Time { return now }
	return store, injector, controller, &now
}

func TestScanSkipsFailingStreamUntilBackoffExpires(t *testing.T) {
	_, injector, controller, now := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx) // attempt 1: delay = 1s << 1 = 2s
	require.Equal(t, 1, injector.calls)

	controller.scan(ctx) // inside the 2s window
	require.Equal(t, 1, injector.calls)

	*now = now.Add(3 * time.Second)
	controller.scan(ctx) // attempt 2: delay = 1s << 2 = 4s
	require.Equal(t, 2, injector.calls)

	*now = now.Add(3 * time.Second)
	controller.scan(ctx) // still inside the 4s window
	require.Equal(t, 2, injector.calls)

	*now = now.Add(2 * time.Second)
	controller.scan(ctx)
	require.Equal(t, 3, injector.calls)
}

func TestHeadAdvanceResetsBackoff(t *testing.T) {
	store, injector, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx)
	require.Equal(t, 1, injector.calls)

	store.streams[0].Head = 3 // new events arrived
	controller.scan(ctx)      // no clock movement needed
	require.Equal(t, 2, injector.calls)
}

func TestSuccessClearsBackoffAndAttemptsRestart(t *testing.T) {
	_, injector, controller, now := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx)
	controller.scan(ctx)
	require.Equal(t, 1, injector.calls)

	injector.err = nil
	*now = now.Add(time.Hour)
	controller.scan(ctx)
	require.Equal(t, 2, injector.calls)
	require.Empty(t, controller.failures)

	injector.err = errors.New("planner down again")
	controller.scan(ctx)
	require.Equal(t, 3, injector.calls)
	record := controller.failures["local-team/agent-1/session-1"]
	require.Equal(t, 1, record.attempts, "attempts must restart after a success")
}

func TestManualInjectBypassesAndClearsBackoff(t *testing.T) {
	_, injector, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx) // stream now backed off
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

	controller.scan(ctx)
	require.NotEmpty(t, controller.failures)

	_, err := controller.Rebuild(ctx, "local-team", time.Time{})
	require.NoError(t, err)
	require.NotEmpty(t, controller.failures, "queueing alone must not clear backoff")

	controller.maybeRebuild(ctx)
	require.Empty(t, controller.failures)
}

func TestBackoffDelayIsCapped(t *testing.T) {
	controller := &Controller{interval: 2 * time.Second}
	require.Equal(t, 4*time.Second, controller.backoffDelay(1))
	require.Equal(t, 8*time.Second, controller.backoffDelay(2))
	require.Equal(t, failureBackoffCap, controller.backoffDelay(10))
	require.Equal(t, failureBackoffCap, controller.backoffDelay(64))
}
