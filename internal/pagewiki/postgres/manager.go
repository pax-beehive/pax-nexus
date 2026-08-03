package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
)

// repositoryEntry is one scope's hydration slot. Its mutex serializes
// hydration of that scope only; the manager-wide mutex is held just long
// enough to look the entry up, so a cold scope's (expensive, full-mirror)
// hydration never blocks any other scope.
type repositoryEntry struct {
	mu         sync.Mutex
	repository *Repository // nil until hydrated; errors are not cached
}

// RepositoryManager hands out one hydrated Repository per scope. Each
// Repository carries a per-scope in-memory mirror hydrated at first use, so
// instances are cached for the process lifetime; eviction of idle scopes is
// deliberately deferred until the SaaS control plane exists.
//
// Hydration uses a two-tier locking scheme: the manager-wide mutex is held
// just long enough to look up or create a per-scope entry; each scope's
// hydration is serialized only by its own entry mutex. This ensures that
// different scopes never block each other, even during expensive full-mirror
// hydration. Same-scope concurrent first-touch hydrates exactly once, and
// failed hydrations are not cached — the next call retries.
type RepositoryManager struct {
	mu      sync.Mutex
	entries map[string]*repositoryEntry
	hydrate func(ctx context.Context, scopeID string) (*Repository, error)
}

func NewRepositoryManager(pool *pgxpool.Pool, options ...memory.Option) (*RepositoryManager, error) {
	if pool == nil {
		return nil, fmt.Errorf("create pagewiki repository manager: pool is required")
	}
	return &RepositoryManager{
		entries: make(map[string]*repositoryEntry),
		hydrate: func(ctx context.Context, scopeID string) (*Repository, error) {
			return NewRepository(ctx, pool, scopeID, options...)
		},
	}, nil
}

func (m *RepositoryManager) ForScope(ctx context.Context, scopeID string) (*Repository, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("resolve pagewiki repository: scope ID is required")
	}
	m.mu.Lock()
	entry, ok := m.entries[scopeID]
	if !ok {
		entry = &repositoryEntry{}
		m.entries[scopeID] = entry
	}
	m.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.repository != nil {
		return entry.repository, nil
	}
	repository, err := m.hydrate(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("hydrate pagewiki repository for scope %s: %w", scopeID, err)
	}
	entry.repository = repository
	return repository, nil
}
