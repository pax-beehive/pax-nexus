package todoapp_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/stretchr/testify/require"
)

// fakeScopeLister is a ScopeLister that returns a fixed set of scopes.
type fakeScopeLister struct{ scopes []string }

func (f *fakeScopeLister) ListScopes(context.Context) ([]string, error) {
	return f.scopes, nil
}

// countingNotes is a NoteDirectory that counts calls to ListOpenActionItems,
// safe for concurrent use by the scheduler goroutine and the test.
type countingNotes struct {
	mu    sync.Mutex
	calls int
}

func (n *countingNotes) ListOpenActionItems(context.Context, string, int) ([]todoapp.ActionItem, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	return nil, nil
}

func (n *countingNotes) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// sweepNotifyingNotes is a NoteDirectory that reports each scope it is
// queried for on a channel the instant the call happens, so a test can
// synchronize on sweep progress without sleeping. It fails (returns an
// error) for a single configured scope, letting a test prove that scope's
// failure does not stop the rest of the sweep.
type sweepNotifyingNotes struct {
	failScope string
	notify    chan string
}

func (n *sweepNotifyingNotes) ListOpenActionItems(_ context.Context, scopeID string, _ int) ([]todoapp.ActionItem, error) {
	n.notify <- scopeID
	if scopeID == n.failScope {
		return nil, errors.New("sweepNotifyingNotes: simulated failure for " + scopeID)
	}
	return nil, nil
}

func newSchedulerTestService(t *testing.T, notes todoapp.NoteDirectory) *todoapp.Service {
	t.Helper()
	repo := &fakeRepository{
		todos:       make(map[string]todoapp.Todo),
		suggestions: make(map[string]todoapp.Suggestion),
	}
	reporter := &fakeReporter{events: []todoapp.ReportEvent{}}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: repo,
		Notes:      notes,
		Reporter:   reporter,
		Logger:     observability.DiscardLogger(),
	})
	require.NoError(t, err)
	return service
}

func TestStartSuggestionRefresh_RunsOnIntervalUntilStopped(t *testing.T) {
	notes := &countingNotes{}
	service := newSchedulerTestService(t, notes)
	logger := observability.DiscardLogger()
	lister := &fakeScopeLister{scopes: []string{"local-team"}}

	stop := todoapp.StartSuggestionRefresh(context.Background(), service, lister, 10*time.Millisecond, logger)

	require.Eventually(t, func() bool {
		return notes.count() >= 2
	}, time.Second, 5*time.Millisecond, "expected at least 2 refreshes (immediate + at least one tick)")

	stop()

	frozen := notes.count()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, frozen, notes.count(), "call count must not change after stop()")
}

func TestStartSuggestionRefresh_ZeroIntervalDefaultsToHour(t *testing.T) {
	notes := &countingNotes{}
	service := newSchedulerTestService(t, notes)
	logger := observability.DiscardLogger()
	lister := &fakeScopeLister{scopes: []string{"local-team"}}

	// interval <= 0 must not panic (ticker with non-positive duration would)
	// and must still run once immediately.
	stop := todoapp.StartSuggestionRefresh(context.Background(), service, lister, 0, logger)
	require.Eventually(t, func() bool {
		return notes.count() >= 1
	}, time.Second, 5*time.Millisecond, "expected the immediate refresh to run")
	stop()
}

// TestStartSuggestionRefresh_SweepsAllListedScopesAndSkipsErrors proves each
// sweep visits every scope the ScopeLister returns, in sorted order, and
// that one scope's error does not stop the rest of that same sweep.
func TestStartSuggestionRefresh_SweepsAllListedScopesAndSkipsErrors(t *testing.T) {
	lister := &fakeScopeLister{scopes: []string{"scope-a", "scope-b"}}
	notes := &sweepNotifyingNotes{failScope: "scope-a", notify: make(chan string, 4)}
	service := newSchedulerTestService(t, notes)
	logger := observability.DiscardLogger()

	stop := todoapp.StartSuggestionRefresh(context.Background(), service, lister, time.Hour, logger)
	defer stop()

	// The first sweep runs immediately (before the ticker ever fires), so
	// the first two notifications are that sweep's two scopes, in the
	// sorted order the lister returned them.
	select {
	case got := <-notes.notify:
		require.Equal(t, "scope-a", got, "scope-a must be refreshed first (sorted order)")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scope-a to be swept")
	}

	select {
	case got := <-notes.notify:
		require.Equal(t, "scope-b", got, "scope-b must still be refreshed even though scope-a errored")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scope-b to be swept despite scope-a's error")
	}
}
