package pagewiki

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// RepositoryResolver hydrates or looks up the Repository for one scope. It
// mirrors postgres.RepositoryManager.ForScope's signature so a ServiceManager
// can be built directly over it without an adapter.
type RepositoryResolver func(ctx context.Context, scopeID string) (Repository, error)

// ServiceManagerConfig carries the scope-independent collaborators every
// per-scope Service is built with: the Planner and Editor, plus whatever
// ServiceOptions (tree navigator, curator, ...) wiring configures today for
// the single on-prem Service.
type ServiceManagerConfig struct {
	Planner Planner
	Editor  Editor
	Options []ServiceOption
}

// ServiceManager hands out one Service per scope, built lazily over a
// RepositoryResolver on first use and cached for the process lifetime —
// mirroring postgres.RepositoryManager's shape and caching contract.
//
// Start records the maintenance root context. ForScope starts each Service's
// background tree/curation maintenance loops exactly once: immediately at
// creation if Start has already run, or when Start itself runs, for every
// scope resolved before that call. Holding mu across Service construction
// gives single-flight creation per process, matching RepositoryManager.
type ServiceManager struct {
	repositories RepositoryResolver
	config       ServiceManagerConfig

	mu             sync.Mutex
	services       map[string]*Service
	started        bool
	maintenanceCtx context.Context
}

// NewServiceManager builds a ServiceManager over repositories. repositories
// is required; config's Planner/Editor/Options are passed through to every
// per-scope Service unchanged.
func NewServiceManager(repositories RepositoryResolver, config ServiceManagerConfig) (*ServiceManager, error) {
	if repositories == nil {
		return nil, fmt.Errorf("create pagewiki service manager: repository resolver is required")
	}
	return &ServiceManager{
		repositories: repositories,
		config:       config,
		services:     make(map[string]*Service),
	}, nil
}

// ForScope returns scopeID's Service, creating and caching it on first use.
// When Start has already run, a newly created Service's maintenance loops
// start immediately; otherwise they wait for the eventual Start call.
func (m *ServiceManager) ForScope(ctx context.Context, scopeID string) (*Service, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("resolve pagewiki service: scope ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if service, ok := m.services[scopeID]; ok {
		return service, nil
	}
	repository, err := m.repositories(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("resolve pagewiki repository for scope %s: %w", scopeID, err)
	}
	service := NewService(repository, m.config.Planner, m.config.Editor, m.config.Options...)
	m.services[scopeID] = service
	if m.started {
		service.StartTreeMaintenance(m.maintenanceCtx)
		service.StartCurationMaintenance(m.maintenanceCtx)
	}
	return service, nil
}

// Start records ctx as the maintenance root context and starts background
// tree/curation maintenance for every Service already created, so on-prem
// boot's "manager created, LocalScopeID resolved, Start called" order starts
// maintenance for that Service exactly once. Any Service ForScope creates
// afterward starts its own maintenance immediately, at creation.
func (m *ServiceManager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.maintenanceCtx = ctx
	for _, service := range m.services {
		service.StartTreeMaintenance(ctx)
		service.StartCurationMaintenance(ctx)
	}
}
