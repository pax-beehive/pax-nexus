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

// serviceEntry is one scope's construction slot: its mutex serializes
// building that scope's Service (including the repository resolution, which
// may hydrate) so different scopes never wait on each other.
type serviceEntry struct {
	mu      sync.Mutex
	service *Service // nil until built; resolution errors are not cached
}

// ServiceManager hands out one Service per scope, built lazily over a
// RepositoryResolver on first use and cached for the process lifetime —
// mirroring postgres.RepositoryManager's shape and caching contract.
//
// Service construction uses a two-tier locking scheme: the manager-wide
// mutex is held just long enough to look up or create a per-scope entry;
// each scope's construction (including repository resolution) is serialized
// only by its own entry mutex. This ensures that different scopes never
// block each other, even during expensive repository resolution. Same-scope
// concurrent first-touch builds exactly once, and failed resolutions are not
// cached — the next call retries.
//
// Start records the maintenance root context. ForScope starts each Service's
// background tree/curation maintenance loops exactly once: immediately at
// creation if Start has already run, or when Start itself runs, for every
// scope resolved before that call.
type ServiceManager struct {
	repositories RepositoryResolver
	config       ServiceManagerConfig

	mu                sync.Mutex
	entries           map[string]*serviceEntry
	services          map[string]*Service // for Start: services built before it ran
	started           bool
	maintenanceCtx    context.Context
	cancelMaintenance context.CancelFunc
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
		entries:      make(map[string]*serviceEntry),
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
	entry, ok := m.entries[scopeID]
	if !ok {
		entry = &serviceEntry{}
		m.entries[scopeID] = entry
	}
	m.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.service != nil {
		return entry.service, nil
	}
	repository, err := m.repositories(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("resolve pagewiki repository for scope %s: %w", scopeID, err)
	}
	service := NewService(repository, m.config.Planner, m.config.Editor, m.config.Options...)

	// Register and snapshot the started flag under one mu hold: if Start has
	// not run yet it will start this service's maintenance (it is in
	// m.services); if it has, we start it here. Exactly one side fires.
	m.mu.Lock()
	m.services[scopeID] = service
	started, maintenanceCtx := m.started, m.maintenanceCtx
	m.mu.Unlock()
	if started {
		service.StartTreeMaintenance(maintenanceCtx)
		service.StartCurationMaintenance(maintenanceCtx)
	}
	entry.service = service
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
	ctx, cancel := context.WithCancel(ctx)
	m.started = true
	m.maintenanceCtx = ctx
	m.cancelMaintenance = cancel
	for _, service := range m.services {
		service.StartTreeMaintenance(ctx)
		service.StartCurationMaintenance(ctx)
	}
}

// Stop cancels the maintenance context recorded by Start, stopping every
// per-scope Service's background tree/curation loop at its next select. The
// loops are fire-and-forget by design — pending tree tasks are dropped on
// cancellation (see Service.StartTreeMaintenance) and in-flight work
// observes the same cancelled context — so Stop does not wait on them.
// Stopping a manager that was never started is a no-op.
func (m *ServiceManager) Stop() {
	m.mu.Lock()
	cancel := m.cancelMaintenance
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
