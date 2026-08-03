package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testManager(hydrate func(context.Context, string) (*Repository, error)) *RepositoryManager {
	return &RepositoryManager{entries: make(map[string]*repositoryEntry), hydrate: hydrate}
}

func TestForScopeColdHydrationDoesNotBlockOtherScopes(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	manager := testManager(func(_ context.Context, scopeID string) (*Repository, error) {
		if scopeID == "cold" {
			close(entered)
			<-release
		}
		return &Repository{scopeID: scopeID}, nil
	})
	go func() {
		if _, err := manager.ForScope(context.Background(), "cold"); err != nil {
			t.Error(err)
		}
	}()
	<-entered

	done := make(chan struct{})
	go func() {
		if _, err := manager.ForScope(context.Background(), "hot"); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hot scope blocked behind cold scope's hydration")
	}
	close(release)
}

func TestForScopeHydratesConcurrentFirstTouchExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	manager := testManager(func(_ context.Context, scopeID string) (*Repository, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		return &Repository{scopeID: scopeID}, nil
	})
	var wait sync.WaitGroup
	results := make([]*Repository, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			repository, err := manager.ForScope(context.Background(), "scope-a")
			if err != nil {
				t.Error(err)
				return
			}
			results[index] = repository
		}(i)
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("hydrate ran %d times, want 1", calls.Load())
	}
	if results[0] == nil || results[0] != results[1] {
		t.Fatal("concurrent first-touch must return the same cached instance")
	}
}

func TestForScopeRetriesAfterFailedHydration(t *testing.T) {
	var calls atomic.Int32
	manager := testManager(func(_ context.Context, scopeID string) (*Repository, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("database offline")
		}
		return &Repository{scopeID: scopeID}, nil
	})
	if _, err := manager.ForScope(context.Background(), "scope-a"); err == nil {
		t.Fatal("first hydration must surface its error")
	}
	repository, err := manager.ForScope(context.Background(), "scope-a")
	if err != nil {
		t.Fatalf("second hydration must retry, got %v", err)
	}
	if repository == nil {
		t.Fatal("second hydration must return the repository")
	}
}
