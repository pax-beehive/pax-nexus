package postgres

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// These tests live in-package so they can override the manager's hydrate
// seam: hydration normally runs against Postgres, and the locking contract —
// a cached scope never waits behind another scope's cold hydration, while
// same-scope first-touch still hydrates exactly once — must be testable
// without a database. They mirror the ServiceManager's single-flight tests
// (service_manager_test.go).

func newFakeHydrationManager(
	t *testing.T,
	hydrate func(ctx context.Context, scopeID string) (*Repository, error),
) *RepositoryManager {
	t.Helper()
	// The pool satisfies the constructor's nil check only; the overridden
	// hydrate never touches it.
	manager, err := NewRepositoryManager(&pgxpool.Pool{})
	require.NoError(t, err)
	manager.hydrate = hydrate
	return manager
}

func TestForScopeCachedScopeDoesNotBlockBehindColdHydration(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	manager := newFakeHydrationManager(t, func(_ context.Context, scopeID string) (*Repository, error) {
		if scopeID == "cold" {
			close(entered)
			<-release
		}
		return &Repository{scopeID: scopeID}, nil
	})
	hot, err := manager.ForScope(context.Background(), "hot")
	require.NoError(t, err)

	coldResult := make(chan error, 1)
	go func() {
		_, coldErr := manager.ForScope(context.Background(), "cold")
		coldResult <- coldErr
	}()
	<-entered

	done := make(chan *Repository, 1)
	go func() {
		cached, cachedErr := manager.ForScope(context.Background(), "hot")
		if cachedErr != nil {
			t.Error(cachedErr)
		}
		done <- cached
	}()
	select {
	case cached := <-done:
		require.Same(t, hot, cached)
	case <-time.After(2 * time.Second):
		t.Fatal("cached scope blocked behind another scope's cold hydration")
	}
	close(release)
	require.NoError(t, <-coldResult)
}

func TestForScopeConcurrentFirstTouchHydratesExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	manager := newFakeHydrationManager(t, func(_ context.Context, scopeID string) (*Repository, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return &Repository{scopeID: scopeID}, nil
	})
	var wait sync.WaitGroup
	repositories := make([]*Repository, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			repository, err := manager.ForScope(context.Background(), "scope-a")
			if err != nil {
				t.Error(err)
				return
			}
			repositories[slot] = repository
		}(index)
	}
	wait.Wait()
	require.EqualValues(t, 1, calls.Load(), "concurrent first-touch must hydrate exactly once")
	require.NotNil(t, repositories[0])
	require.Same(t, repositories[0], repositories[1])
}

func TestForScopeHydrationFailureIsNotCached(t *testing.T) {
	var calls atomic.Int32
	manager := newFakeHydrationManager(t, func(_ context.Context, scopeID string) (*Repository, error) {
		if calls.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return &Repository{scopeID: scopeID}, nil
	})

	_, err := manager.ForScope(context.Background(), "scope-a")
	require.Error(t, err)
	repository, err := manager.ForScope(context.Background(), "scope-a")
	require.NoError(t, err)
	require.NotNil(t, repository)
	require.EqualValues(t, 2, calls.Load())
}
