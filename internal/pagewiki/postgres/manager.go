package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
)

// RepositoryManager hands out one hydrated Repository per scope. Each
// Repository carries a per-scope in-memory mirror hydrated at first use, so
// instances are cached for the process lifetime; eviction of idle scopes is
// deliberately deferred until the SaaS control plane exists.
//
// Holding mu across hydration is intentional: it gives single-flight
// hydration per process; hydration is a startup-class cost and concurrent
// first-touch of two different scopes is rare until Phase 3.
type RepositoryManager struct {
	pool    *pgxpool.Pool
	options []memory.Option

	mu           sync.Mutex
	repositories map[string]*Repository
}

func NewRepositoryManager(pool *pgxpool.Pool, options ...memory.Option) (*RepositoryManager, error) {
	if pool == nil {
		return nil, fmt.Errorf("create pagewiki repository manager: pool is required")
	}
	return &RepositoryManager{pool: pool, options: options, repositories: make(map[string]*Repository)}, nil
}

func (m *RepositoryManager) ForScope(ctx context.Context, scopeID string) (*Repository, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("resolve pagewiki repository: scope ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if repository, ok := m.repositories[scopeID]; ok {
		return repository, nil
	}
	repository, err := NewRepository(ctx, m.pool, scopeID, m.options...)
	if err != nil {
		return nil, fmt.Errorf("hydrate pagewiki repository for scope %s: %w", scopeID, err)
	}
	m.repositories[scopeID] = repository
	return repository, nil
}
