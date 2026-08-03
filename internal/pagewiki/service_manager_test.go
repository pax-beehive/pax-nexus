package pagewiki_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

// signalingRepository wraps memory.Repository, signaling on curationRuns
// every time SaveCurationRun is called. It lets tests observe a curation
// maintenance tick without depending on the package's internal run-ID
// hashing (catalogFingerprint/stableID are unexported).
type signalingRepository struct {
	*memory.Repository
	curationRuns chan struct{}
}

func newSignalingRepository() *signalingRepository {
	return &signalingRepository{Repository: memory.NewRepository(), curationRuns: make(chan struct{}, 16)}
}

func (r *signalingRepository) SaveCurationRun(ctx context.Context, run pagewiki.CurationRun) error {
	err := r.Repository.SaveCurationRun(ctx, run)
	select {
	case r.curationRuns <- struct{}{}:
	default:
	}
	return err
}

// fakeManagerRepositoryResolver hands out one signalingRepository per scope,
// caching it for the process lifetime and counting how many times each scope
// was resolved: a ServiceManager that only builds a Service on first use
// must never resolve the same scope's repository twice.
type fakeManagerRepositoryResolver struct {
	mu    sync.Mutex
	repos map[string]*signalingRepository
	calls map[string]int
}

func newFakeManagerRepositoryResolver() *fakeManagerRepositoryResolver {
	return &fakeManagerRepositoryResolver{
		repos: make(map[string]*signalingRepository),
		calls: make(map[string]int),
	}
}

func (f *fakeManagerRepositoryResolver) resolve(_ context.Context, scopeID string) (pagewiki.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[scopeID]++
	repository, ok := f.repos[scopeID]
	if !ok {
		repository = newSignalingRepository()
		f.repos[scopeID] = repository
	}
	return repository, nil
}

func (f *fakeManagerRepositoryResolver) callCount(scopeID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[scopeID]
}

func (f *fakeManagerRepositoryResolver) signaling(scopeID string) *signalingRepository {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repos[scopeID]
}

func newTestServiceManager(
	t *testing.T,
	resolver *fakeManagerRepositoryResolver,
	options ...pagewiki.ServiceOption,
) *pagewiki.ServiceManager {
	t.Helper()
	manager, err := pagewiki.NewServiceManager(resolver.resolve, pagewiki.ServiceManagerConfig{
		Planner: pagewiki.ScriptedPlanner{}, Editor: pagewiki.ScriptedEditor{}, Options: options,
	})
	require.NoError(t, err)
	return manager
}

func TestNewServiceManagerRequiresRepositoryResolver(t *testing.T) {
	_, err := pagewiki.NewServiceManager(nil, pagewiki.ServiceManagerConfig{
		Planner: pagewiki.ScriptedPlanner{}, Editor: pagewiki.ScriptedEditor{},
	})
	require.Error(t, err)
}

func TestServiceManagerForScopeReturnsSameInstanceForSameScope(t *testing.T) {
	resolver := newFakeManagerRepositoryResolver()
	manager := newTestServiceManager(t, resolver)

	first, err := manager.ForScope(context.Background(), "scope-a")
	require.NoError(t, err)
	second, err := manager.ForScope(context.Background(), "scope-a")
	require.NoError(t, err)

	require.Same(t, first, second)
	require.Equal(t, 1, resolver.callCount("scope-a"))
}

func TestServiceManagerForScopeReturnsDifferentInstancePerScope(t *testing.T) {
	resolver := newFakeManagerRepositoryResolver()
	manager := newTestServiceManager(t, resolver)

	a, err := manager.ForScope(context.Background(), "scope-a")
	require.NoError(t, err)
	b, err := manager.ForScope(context.Background(), "scope-b")
	require.NoError(t, err)

	require.NotSame(t, a, b)
}

func TestServiceManagerForScopeRejectsBlankScope(t *testing.T) {
	resolver := newFakeManagerRepositoryResolver()
	manager := newTestServiceManager(t, resolver)

	_, err := manager.ForScope(context.Background(), "")
	require.Error(t, err)
}

// TestServiceManagerStartStartsMaintenanceForServiceCreatedBeforeStart covers
// the on-prem boot order: the manager is created, LocalScopeID's Service is
// resolved (queuing tree work before any worker exists to drain it), and only
// then is Start called. Maintenance must stay idle until Start runs, and run
// afterward.
func TestServiceManagerStartStartsMaintenanceForServiceCreatedBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := newFakeManagerRepositoryResolver()
	navigator := &fakeTreeNavigator{}
	manager := newTestServiceManager(t, resolver,
		pagewiki.WithTreeNavigator(pagewiki.TreeMaintenanceConfig{Navigator: navigator}),
	)

	service, err := manager.ForScope(ctx, "scope-a")
	require.NoError(t, err)
	service.EnqueueTreeInsertForTest("page-not-in-catalog")
	require.Equal(t, 1, service.PendingTreeTasksForTest())

	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 1, service.PendingTreeTasksForTest(), "maintenance must not run before Start")

	manager.Start(ctx)

	require.Eventually(t, func() bool {
		return service.PendingTreeTasksForTest() == 0
	}, time.Second, 5*time.Millisecond, "tree maintenance never started")
}

// TestServiceManagerForScopeAfterStartStartsMaintenanceImmediately covers a
// scope resolved lazily, after boot: Start has already run, so the newly
// created Service's maintenance must start at creation, not wait for another
// Start call.
func TestServiceManagerForScopeAfterStartStartsMaintenanceImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := newFakeManagerRepositoryResolver()
	navigator := &fakeTreeNavigator{}
	manager := newTestServiceManager(t, resolver,
		pagewiki.WithTreeNavigator(pagewiki.TreeMaintenanceConfig{Navigator: navigator}),
	)
	manager.Start(ctx)

	service, err := manager.ForScope(ctx, "scope-b")
	require.NoError(t, err)
	service.EnqueueTreeInsertForTest("page-not-in-catalog")

	require.Eventually(t, func() bool {
		return service.PendingTreeTasksForTest() == 0
	}, time.Second, 5*time.Millisecond, "tree maintenance never started for a scope created after Start")
}

// TestServiceManagerStartsCurationMaintenanceExactlyOncePerScope asserts both
// that curation maintenance actually starts (a run lands on the fake
// repository) and that repeated ForScope calls for an already-running scope
// never re-resolve its repository — the only path that would start a second
// background loop.
func TestServiceManagerStartsCurationMaintenanceExactlyOncePerScope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := newFakeManagerRepositoryResolver()
	manager := newTestServiceManager(t, resolver,
		pagewiki.WithCurator(
			pagewiki.ScriptedCurator{}, pagewiki.ScriptedEmbedder{},
			pagewiki.CurationConfig{Interval: 10 * time.Millisecond}, nil,
		),
	)

	_, err := manager.ForScope(ctx, "scope-a")
	require.NoError(t, err)
	manager.Start(ctx)

	repository := resolver.signaling("scope-a")
	require.Eventually(t, func() bool {
		select {
		case <-repository.curationRuns:
			return true
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond, "curation maintenance never started")

	for i := 0; i < 5; i++ {
		_, err := manager.ForScope(ctx, "scope-a")
		require.NoError(t, err)
	}
	require.Equal(t, 1, resolver.callCount("scope-a"),
		"a cached scope must never re-resolve its repository, which is the only place maintenance starts")
}
