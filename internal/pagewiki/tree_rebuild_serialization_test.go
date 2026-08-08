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

// recordingTreeRepository wraps memory.Repository and records every
// ReplaceTopicTree call: "clear" for an empty tree, "write" for anything
// else. The sequence exposes whether two rebuilds interleaved.
type recordingTreeRepository struct {
	*memory.Repository
	mu       sync.Mutex
	replaces []string
}

func (r *recordingTreeRepository) ReplaceTopicTree(ctx context.Context, tree pagewiki.TopicTree) error {
	err := r.Repository.ReplaceTopicTree(ctx, tree)
	kind := "write"
	if len(tree.Topics) == 0 && len(tree.Placements) == 0 {
		kind = "clear"
	}
	r.mu.Lock()
	r.replaces = append(r.replaces, kind)
	r.mu.Unlock()
	return err
}

func (r *recordingTreeRepository) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.replaces...)
}

// gatedTreeNavigator blocks every ChoosePlacement until the test feeds a
// release token, so a rebuild can be held mid-placement deterministically.
// Placement always stays at the root; the split that follows the root's
// overflow is what writes the tree, splitting the pages into two halves.
type gatedTreeNavigator struct {
	entered chan struct{}
	release chan struct{}
}

func (n *gatedTreeNavigator) ChoosePlacement(
	_ context.Context,
	_ pagewiki.TreePlacementInput,
) (pagewiki.TreePlacementChoice, error) {
	n.entered <- struct{}{}
	<-n.release
	return pagewiki.TreePlacementChoice{Action: pagewiki.TreePlacementStay}, nil
}

func (n *gatedTreeNavigator) SplitTopic(
	_ context.Context,
	input pagewiki.TreeSplitInput,
) ([]pagewiki.TreeSplitGroup, error) {
	slugs := make([]string, 0, len(input.Pages))
	for _, page := range input.Pages {
		slugs = append(slugs, page.Slug)
	}
	half := len(slugs) / 2
	return []pagewiki.TreeSplitGroup{
		{Title: "First Half", Pages: slugs[:half]},
		{Title: "Second Half", Pages: slugs[half:]},
	}, nil
}

func waitSignal(t *testing.T, signals <-chan struct{}, step string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for " + step)
	}
}

// TestRebuildTopicTreeSerializesConcurrentRebuilds pins that a second
// whole-tree rebuild waits for the in-flight one instead of interleaving:
// an interleaved second clear would wipe placements whose queue tasks were
// already consumed, losing them until the next rebuild.
func TestRebuildTopicTreeSerializesConcurrentRebuilds(t *testing.T) {
	ctx := context.Background()
	repository := &recordingTreeRepository{Repository: memory.NewRepository()}
	// Eleven pages: the cleared root is over capacity from the first insert,
	// so each rebuild does one gated placement, then splits and writes.
	seedRootPages(t, repository.Repository, 11)
	navigator := &gatedTreeNavigator{
		entered: make(chan struct{}, 8),
		release: make(chan struct{}, 8),
	}
	service := newTreeService(repository, navigator)

	firstDone := make(chan error, 1)
	go func() { firstDone <- service.RebuildTopicTree(ctx) }()
	waitSignal(t, navigator.entered, "first rebuild's placement")

	secondDone := make(chan error, 1)
	go func() { secondDone <- service.RebuildTopicTree(ctx) }()

	// While the first rebuild is mid-placement the second must not have
	// touched the tree: in particular its clear must not run.
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, []string{"clear"}, repository.snapshot(),
		"second rebuild must not clear while the first is in flight")

	navigator.release <- struct{}{}
	require.NoError(t, <-firstDone)

	waitSignal(t, navigator.entered, "second rebuild's placement")
	navigator.release <- struct{}{}
	require.NoError(t, <-secondDone)

	require.Equal(t,
		[]string{"clear", "write", "clear", "write"},
		repository.snapshot(),
		"each rebuild's clear+split must run as one uninterleaved unit")
}
