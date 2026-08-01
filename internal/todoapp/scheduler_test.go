package todoapp_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/stretchr/testify/require"
)

// countingNotes is a NoteDirectory that counts calls to ListOpenActionItems,
// safe for concurrent use by the scheduler goroutine and the test.
type countingNotes struct {
	mu    sync.Mutex
	calls int
}

func (n *countingNotes) ListOpenActionItems(context.Context, int) ([]todoapp.ActionItem, error) {
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

	stop := todoapp.StartSuggestionRefresh(context.Background(), service, 10*time.Millisecond, logger)

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

	// interval <= 0 must not panic (ticker with non-positive duration would)
	// and must still run once immediately.
	stop := todoapp.StartSuggestionRefresh(context.Background(), service, 0, logger)
	require.Eventually(t, func() bool {
		return notes.count() >= 1
	}, time.Second, 5*time.Millisecond, "expected the immediate refresh to run")
	stop()
}
