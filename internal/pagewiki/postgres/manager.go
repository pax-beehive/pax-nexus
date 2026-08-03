package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
)

// repositoryEntry is one scope's hydration slot: its mutex serializes that
// scope's hydration so different scopes never wait on each other.
type repositoryEntry struct {
	mu         sync.Mutex
	repository *Repository // nil until hydrated; hydration errors are not cached
}

// RepositoryManager hands out one hydrated Repository per scope. Each
// Repository carries a per-scope in-memory mirror hydrated at first use, so
// instances are cached for the process lifetime; eviction of idle scopes is
// deliberately deferred until the SaaS control plane exists.
//
// Hydration uses the same two-tier locking scheme as pagewiki.ServiceManager:
// the manager-wide mutex is held just long enough to look up or create a
// per-scope entry; each scope's hydration — minutes of Postgres replay on a
// cold scope — is serialized only by its own entry mutex. A cached scope, or
// any other scope, therefore never blocks behind another scope's cold
// hydration; same-scope concurrent first-touch still hydrates exactly once,
// and a failed hydration is not cached — the next call retries.
type RepositoryManager struct {
	pool    *pgxpool.Pool
	options []memory.Option
	// hydrate builds one scope's Repository. It defaults to NewRepository
	// over pool/options; tests override it to exercise the locking contract
	// without a database.
	hydrate func(ctx context.Context, scopeID string) (*Repository, error)

	mu      sync.Mutex
	entries map[string]*repositoryEntry
}

func NewRepositoryManager(pool *pgxpool.Pool, options ...memory.Option) (*RepositoryManager, error) {
	if pool == nil {
		return nil, fmt.Errorf("create pagewiki repository manager: pool is required")
	}
	manager := &RepositoryManager{
		pool: pool, options: options,
		entries: make(map[string]*repositoryEntry),
	}
	manager.hydrate = func(ctx context.Context, scopeID string) (*Repository, error) {
		return NewRepository(ctx, manager.pool, scopeID, manager.options...)
	}
	return manager, nil
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
