package pagewiki

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// stubCurationRepository implements only what RunCurationRound touches for
// an empty Page catalog; the embedded nil Repository panics loudly if
// anything else is called. It is a package-internal stand-in for
// memory.Repository, needed because this test file lives in package
// pagewiki (to reach unexported catalogFingerprint) and importing
// the memory package from here would create an import cycle
// (memory imports pagewiki).
type stubCurationRepository struct {
	Repository
	mu   sync.Mutex
	runs map[string]CurationRun
}

func newStubCurationRepository() *stubCurationRepository {
	return &stubCurationRepository{runs: make(map[string]CurationRun)}
}

func (r *stubCurationRepository) PageCatalog(context.Context) (PageCatalog, error) {
	return PageCatalog{}, nil
}

func (r *stubCurationRepository) GenerationSettings(context.Context) (GenerationDirectives, error) {
	return GenerationDirectives{}, nil
}

func (r *stubCurationRepository) TopicTree(context.Context) (TopicTree, error) {
	return TopicTree{}, nil
}

func (r *stubCurationRepository) SourceRevisionOrdinals(context.Context) (map[string]int, error) {
	return map[string]int{}, nil
}

func (r *stubCurationRepository) PageEmbeddings(context.Context) ([]PageEmbedding, error) {
	return nil, nil
}

func (r *stubCurationRepository) SaveCurationRun(_ context.Context, run CurationRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = run
	return nil
}

func (r *stubCurationRepository) CurationRun(_ context.Context, id string) (CurationRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, found := r.runs[id]
	if !found {
		return CurationRun{}, ErrNotFound
	}
	return run, nil
}

// curationMaintenanceSuite exercises StartCurationMaintenance: the
// background loop that ticks RunCurationRound on CurationConfig.Interval.
type curationMaintenanceSuite struct {
	suite.Suite
	repository *stubCurationRepository
}

func TestCurationMaintenanceSuite(t *testing.T) {
	suite.Run(t, new(curationMaintenanceSuite))
}

func (s *curationMaintenanceSuite) SetupTest() {
	s.repository = newStubCurationRepository()
}

// emptyCatalogRunID mirrors RunCurationRound's own run-ID derivation for an
// empty Page catalog, so the test can poll the repository for the exact run
// a tick is expected to save.
func emptyCatalogRunID() string {
	return StableID("curation-run", catalogFingerprint(PageCatalog{}))
}

func (s *curationMaintenanceSuite) TestGivenPositiveIntervalWhenMaintenanceStartsThenARoundEventuallyRuns() {
	judgeCalls := 0
	curator := ScriptedCurator{JudgeCalls: &judgeCalls}
	embedder := ScriptedEmbedder{}
	service := NewService(
		s.repository, ScriptedPlanner{}, ScriptedEditor{},
		WithCurator(curator, embedder, CurationConfig{Interval: 10 * time.Millisecond}, nil),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartCurationMaintenance(ctx)

	runID := emptyCatalogRunID()
	s.Require().Eventually(func() bool {
		run, err := s.repository.CurationRun(context.Background(), runID)
		return err == nil && run.ID == runID
	}, time.Second, 5*time.Millisecond, "curation round never ran")
}

func (s *curationMaintenanceSuite) TestGivenZeroIntervalWhenMaintenanceStartsThenNoRoundEverRuns() {
	judgeCalls := 0
	curator := ScriptedCurator{JudgeCalls: &judgeCalls}
	embedder := ScriptedEmbedder{}
	service := NewService(
		s.repository, ScriptedPlanner{}, ScriptedEditor{},
		WithCurator(curator, embedder, CurationConfig{Interval: 0}, nil),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartCurationMaintenance(ctx)

	time.Sleep(50 * time.Millisecond)

	runID := emptyCatalogRunID()
	_, err := s.repository.CurationRun(context.Background(), runID)
	s.Require().ErrorIs(err, ErrNotFound)
	s.Require().Equal(0, judgeCalls)
}

func (s *curationMaintenanceSuite) TestGivenNoCuratorWhenMaintenanceStartsThenItIsANoOp() {
	service := NewService(s.repository, ScriptedPlanner{}, ScriptedEditor{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartCurationMaintenance(ctx) // must not panic or spin

	time.Sleep(20 * time.Millisecond)

	runID := emptyCatalogRunID()
	_, err := s.repository.CurationRun(context.Background(), runID)
	s.Require().ErrorIs(err, ErrNotFound)
}
