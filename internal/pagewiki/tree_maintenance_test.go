package pagewiki

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingTreeIndexer struct {
	calls atomic.Int32
}

func (c *countingTreeIndexer) Index(
	context.Context,
	TreeIndexInput,
) (TopicTree, error) {
	c.calls.Add(1)
	return TopicTree{}, nil
}

// stubTreeRepository implements only what reindexTree touches; the embedded
// nil interface panics loudly if anything else is called.
type stubTreeRepository struct {
	Repository
	replaced atomic.Int32
}

func (r *stubTreeRepository) PageCatalog(context.Context) (PageCatalog, error) {
	return PageCatalog{}, nil
}

func (r *stubTreeRepository) TopicTree(context.Context) (TopicTree, error) {
	return TopicTree{}, nil
}

func (r *stubTreeRepository) GenerationSettings(context.Context) (GenerationDirectives, error) {
	return GenerationDirectives{}, nil
}

func (r *stubTreeRepository) ReplaceTopicTree(context.Context, TopicTree) error {
	r.replaced.Add(1)
	return nil
}

func TestTreeMaintenanceCoalescesDirtyMarksIntoOneReindex(t *testing.T) {
	indexer := &countingTreeIndexer{}
	repository := &stubTreeRepository{}
	service := NewService(
		repository, ScriptedPlanner{}, ScriptedEditor{},
		WithTreeIndexer(indexer, nil),
	)
	service.treeQuiet = 20 * time.Millisecond
	service.treeMaxWait = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartTreeMaintenance(ctx)

	service.markTreeDirty()
	time.Sleep(5 * time.Millisecond)
	service.markTreeDirty()

	require.Eventually(t, func() bool {
		return indexer.calls.Load() == 1
	}, time.Second, 5*time.Millisecond, "debounced reindex never ran")

	// Past another full quiet window: no further marks, no further reindex.
	time.Sleep(60 * time.Millisecond)
	require.EqualValues(t, 1, indexer.calls.Load())
	require.EqualValues(t, 1, repository.replaced.Load())
}

func TestFlushTreeReindexRunsOnlyWhenDirty(t *testing.T) {
	indexer := &countingTreeIndexer{}
	repository := &stubTreeRepository{}
	service := NewService(
		repository, ScriptedPlanner{}, ScriptedEditor{},
		WithTreeIndexer(indexer, nil),
	)

	service.FlushTreeReindex(context.Background())
	require.EqualValues(t, 0, indexer.calls.Load())

	service.markTreeDirty()
	service.FlushTreeReindex(context.Background())
	require.EqualValues(t, 1, indexer.calls.Load())

	service.FlushTreeReindex(context.Background())
	require.EqualValues(t, 1, indexer.calls.Load())
}

// TestTreeMaintenanceFiresOnMaxWaitDeadlineEvenWithoutQuiet exercises the
// treeMaxWait branch of debounceThenReindex: marks keep arriving faster than
// the quiet window can expire, so the reindex must still fire once the max
// wait deadline elapses. It asserts "deadline fires while marks keep
// arriving," not an exact call count, to stay robust on a loaded machine.
func TestTreeMaintenanceFiresOnMaxWaitDeadlineEvenWithoutQuiet(t *testing.T) {
	indexer := &countingTreeIndexer{}
	repository := &stubTreeRepository{}
	service := NewService(
		repository, ScriptedPlanner{}, ScriptedEditor{},
		WithTreeIndexer(indexer, nil),
	)
	service.treeQuiet = 50 * time.Millisecond
	service.treeMaxWait = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartTreeMaintenance(ctx)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(400 * time.Millisecond)
		service.markTreeDirty()
		for {
			select {
			case <-stop:
				return
			case <-deadline:
				return
			case <-ticker.C:
				service.markTreeDirty()
			}
		}
	}()

	require.Eventually(t, func() bool {
		return indexer.calls.Load() >= 1
	}, 300*time.Millisecond, 5*time.Millisecond,
		"reindex never ran despite the max-wait deadline elapsing")

	close(stop)
	<-done
}

func TestStartTreeMaintenanceWithoutIndexerIsANoOp(t *testing.T) {
	service := NewService(&stubTreeRepository{}, ScriptedPlanner{}, ScriptedEditor{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartTreeMaintenance(ctx) // must not panic or spin
	service.FlushTreeReindex(ctx)     // must not panic
}
