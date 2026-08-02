package pagewiki_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

// fakeTreeNavigator scripts the navigator port: ChoosePlacement answers are
// consumed in order (falling back to "stay" once the script runs out) and
// SplitTopic answers likewise. Every call is recorded so tests can assert on
// what the service asked, and a mutex keeps the recording safe when the
// background worker and a Flush drive the queue concurrently.
type fakeTreeNavigator struct {
	mu sync.Mutex

	placements      []pagewiki.TreePlacementChoice
	placementErr    error
	placementInputs []pagewiki.TreePlacementInput

	splits      [][]pagewiki.TreeSplitGroup
	splitErr    error
	splitInputs []pagewiki.TreeSplitInput
}

func (n *fakeTreeNavigator) ChoosePlacement(
	_ context.Context,
	input pagewiki.TreePlacementInput,
) (pagewiki.TreePlacementChoice, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.placementInputs = append(n.placementInputs, input)
	if n.placementErr != nil {
		return pagewiki.TreePlacementChoice{}, n.placementErr
	}
	if len(n.placements) == 0 {
		return pagewiki.TreePlacementChoice{Action: pagewiki.TreePlacementStay}, nil
	}
	choice := n.placements[0]
	n.placements = n.placements[1:]
	return choice, nil
}

func (n *fakeTreeNavigator) SplitTopic(
	_ context.Context,
	input pagewiki.TreeSplitInput,
) ([]pagewiki.TreeSplitGroup, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.splitInputs = append(n.splitInputs, input)
	if n.splitErr != nil {
		return nil, n.splitErr
	}
	if len(n.splits) == 0 {
		return nil, errors.New("fake navigator: no scripted split")
	}
	groups := n.splits[0]
	n.splits = n.splits[1:]
	return groups, nil
}

func (n *fakeTreeNavigator) placementCalls() []pagewiki.TreePlacementInput {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]pagewiki.TreePlacementInput(nil), n.placementInputs...)
}

func (n *fakeTreeNavigator) splitCalls() []pagewiki.TreeSplitInput {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]pagewiki.TreeSplitInput(nil), n.splitInputs...)
}

func newTreeMaintenanceService(
	repository pagewiki.Repository,
	planner pagewiki.Planner,
	editor pagewiki.Editor,
	navigator pagewiki.TreeNavigator,
) *pagewiki.Service {
	return pagewiki.NewService(
		repository, planner, editor,
		pagewiki.WithTreeNavigator(pagewiki.TreeMaintenanceConfig{Navigator: navigator}),
	)
}

// newTreeService builds a service whose only interesting collaborator is the
// navigator: the planner and editor are never exercised by a tree task.
func newTreeService(
	repository pagewiki.Repository,
	navigator pagewiki.TreeNavigator,
) *pagewiki.Service {
	return newTreeMaintenanceService(
		repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{}, navigator,
	)
}

func seedTreePage(t *testing.T, repository *memory.Repository, slug, title string) pagewiki.Page {
	t.Helper()
	page := pagewiki.Page{
		ID:                "page-" + slug,
		Slug:              slug,
		Title:             title,
		CurrentRevisionID: "revision-" + slug,
	}
	revision := pagewiki.PageRevision{
		ID:       page.CurrentRevisionID,
		PageID:   page.ID,
		Title:    title,
		Summary:  title + " summary.",
		Markdown: "# " + title,
	}
	require.NoError(t, repository.PublishPage(context.Background(), pagewiki.PagePublication{
		Page: page, Revision: revision,
	}))
	return page
}

func loadTree(t *testing.T, repository *memory.Repository) pagewiki.TopicTree {
	t.Helper()
	tree, err := repository.TopicTree(context.Background())
	require.NoError(t, err)
	return tree
}

func placementFor(tree pagewiki.TopicTree, pageID string) (pagewiki.PagePlacement, bool) {
	for _, placement := range tree.Placements {
		if placement.PageID == pageID {
			return placement, true
		}
	}
	return pagewiki.PagePlacement{}, false
}

func TestTreeMaintenanceInsertStayAtRootLeavesPageUnplaced(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	directives := pagewiki.GenerationDirectives{Language: "Chinese"}
	require.NoError(t, repository.SetGenerationSettings(ctx, directives))
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	navigator := &fakeTreeNavigator{
		placements: []pagewiki.TreePlacementChoice{{Action: pagewiki.TreePlacementStay}},
	}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	calls := navigator.placementCalls()
	require.Len(t, calls, 1)
	require.Equal(t, page.ID, calls[0].Page.ID)
	require.Empty(t, calls[0].Path)
	require.Empty(t, calls[0].Children)
	require.True(t, calls[0].AllowCreate)
	require.Equal(t, directives, calls[0].Directives)

	tree := loadTree(t, repository)
	require.Empty(t, tree.Topics)
	require.Empty(t, tree.Placements)
}

func TestTreeMaintenanceInsertDescendsTwoLevelsAndRanksAfterExistingPages(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	resident := seedTreePage(t, repository, "postgres", "Postgres")
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	require.NoError(t, repository.ReplaceTopicTree(ctx, pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-engineering", Slug: "engineering", Title: "Engineering"},
			{ID: "topic-databases", ParentID: "topic-engineering", Slug: "databases", Title: "Databases"},
		},
		Placements: []pagewiki.PagePlacement{
			{PageID: resident.ID, TopicID: "topic-databases", Rank: 0},
		},
	}))
	navigator := &fakeTreeNavigator{placements: []pagewiki.TreePlacementChoice{
		{Action: pagewiki.TreePlacementEnter, Slug: "engineering"},
		{Action: pagewiki.TreePlacementEnter, Slug: "databases"},
		{Action: pagewiki.TreePlacementStay},
	}}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	calls := navigator.placementCalls()
	require.Len(t, calls, 3)
	require.Empty(t, calls[0].Path)
	require.Equal(t, []string{"Engineering"}, calls[1].Path)
	require.Equal(t, []string{"Engineering", "Databases"}, calls[2].Path)
	require.Equal(t,
		[]pagewiki.TreeChildTopic{{Slug: "databases", Title: "Databases", Pages: 1}},
		calls[1].Children,
	)

	tree := loadTree(t, repository)
	placement, found := placementFor(tree, page.ID)
	require.True(t, found)
	require.Equal(t, "topic-databases", placement.TopicID)
	require.Equal(t, 1, placement.Rank)
}

func TestTreeMaintenanceInsertCreatesTopicWithStableID(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	navigator := &fakeTreeNavigator{placements: []pagewiki.TreePlacementChoice{
		{Action: pagewiki.TreePlacementCreate, Title: "Databases"},
	}}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	tree := loadTree(t, repository)
	require.Len(t, tree.Topics, 1)
	expectedID := pagewiki.TopicIDForTest("", "databases")
	require.Equal(t, expectedID, tree.Topics[0].ID)
	require.Equal(t, "databases", tree.Topics[0].Slug)
	require.Equal(t, "Databases", tree.Topics[0].Title)
	require.Empty(t, tree.Topics[0].ParentID)
	placement, found := placementFor(tree, page.ID)
	require.True(t, found)
	require.Equal(t, expectedID, placement.TopicID)
	require.Equal(t, 0, placement.Rank)
	// Creating a topic ends the descent: no second question is asked inside
	// the brand-new (necessarily empty) topic.
	require.Len(t, navigator.placementCalls(), 1)
}

func TestTreeMaintenanceInsertCreateWithCollidingSlugStaysPut(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	seeded := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-databases", Slug: "databases", Title: "Databases"},
		},
	}
	require.NoError(t, repository.ReplaceTopicTree(ctx, seeded))
	navigator := &fakeTreeNavigator{placements: []pagewiki.TreePlacementChoice{
		{Action: pagewiki.TreePlacementCreate, Title: "Databases"},
	}}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	tree := loadTree(t, repository)
	require.Equal(t, seeded.Topics, tree.Topics)
	require.Empty(t, tree.Placements, "a colliding create degrades to stay, which at the root means no placement")
}

func TestTreeMaintenanceInsertIsIdempotentForAlreadyPlacedPages(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	require.NoError(t, repository.ReplaceTopicTree(ctx, pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-databases", Slug: "databases", Title: "Databases"},
		},
		Placements: []pagewiki.PagePlacement{
			{PageID: page.ID, TopicID: "topic-databases", Rank: 0},
		},
	}))
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	require.Empty(t, navigator.placementCalls())
	tree := loadTree(t, repository)
	require.Len(t, tree.Placements, 1)
}

func TestTreeMaintenanceInsertSkipsRetiredPages(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	require.NoError(t, repository.RetirePage(ctx, pagewiki.RetireRequest{
		PageID:                 page.ID,
		ExpectedBaseRevisionID: page.CurrentRevisionID,
		RunID:                  "run-retire",
	}))
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	require.Empty(t, navigator.placementCalls())
	require.Empty(t, loadTree(t, repository).Placements)
}

// seedFullTopic seeds count pages directly under one root topic, which is
// exactly the overflow threshold the eleventh page trips.
func seedFullTopic(t *testing.T, repository *memory.Repository, count int) pagewiki.TopicTree {
	t.Helper()
	tree := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "topic-notes", Slug: "notes", Title: "Notes"}},
	}
	for index := 0; index < count; index++ {
		page := seedTreePage(t, repository, fmt.Sprintf("note-%02d", index), fmt.Sprintf("Note %02d", index))
		tree.Placements = append(tree.Placements, pagewiki.PagePlacement{
			PageID: page.ID, TopicID: "topic-notes", Rank: index,
		})
	}
	require.NoError(t, repository.ReplaceTopicTree(context.Background(), tree))
	return tree
}

func TestTreeMaintenanceInsertOverflowEnqueuesSplit(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	seedFullTopic(t, repository, 10)
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	navigator := &fakeTreeNavigator{
		placements: []pagewiki.TreePlacementChoice{
			{Action: pagewiki.TreePlacementEnter, Slug: "notes"},
			{Action: pagewiki.TreePlacementStay},
		},
		splitErr: errors.New("boom"),
	}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	splits := navigator.splitCalls()
	require.Len(t, splits, 1, "the eleventh direct page must enqueue a split of that topic")
	require.Equal(t, []string{"Notes"}, splits[0].Path)
	require.Len(t, splits[0].Pages, 11)

	// The failed split leaves the tree exactly as the insert left it.
	tree := loadTree(t, repository)
	require.Len(t, tree.Topics, 1)
	require.Len(t, tree.Placements, 11)
	placement, found := placementFor(tree, page.ID)
	require.True(t, found)
	require.Equal(t, "topic-notes", placement.TopicID)
	require.Equal(t, 10, placement.Rank)
}

func TestTreeMaintenanceSplitMovesPagesIntoNewChildTopics(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	first := seedTreePage(t, repository, "sqlite", "SQLite")
	second := seedTreePage(t, repository, "postgres", "Postgres")
	third := seedTreePage(t, repository, "grafana", "Grafana")
	fourth := seedTreePage(t, repository, "prometheus", "Prometheus")
	require.NoError(t, repository.ReplaceTopicTree(ctx, pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "topic-notes", Slug: "notes", Title: "Notes"}},
		Placements: []pagewiki.PagePlacement{
			{PageID: first.ID, TopicID: "topic-notes", Rank: 0},
			{PageID: second.ID, TopicID: "topic-notes", Rank: 1},
			{PageID: third.ID, TopicID: "topic-notes", Rank: 2},
			{PageID: fourth.ID, TopicID: "topic-notes", Rank: 3},
		},
	}))
	navigator := &fakeTreeNavigator{splits: [][]pagewiki.TreeSplitGroup{{
		{Title: "Storage", Pages: []string{"sqlite", "postgres"}},
		{Title: "Observability", Pages: []string{"grafana", "prometheus"}},
	}}}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeSplitForTest("topic-notes")
	service.FlushTreeReindex(ctx)

	splits := navigator.splitCalls()
	require.Len(t, splits, 1)
	require.Equal(t, []string{"Notes"}, splits[0].Path)
	require.Len(t, splits[0].Pages, 4)
	require.Empty(t, splits[0].Forbidden)

	tree := loadTree(t, repository)
	storageID := pagewiki.TopicIDForTest("topic-notes", "storage")
	observabilityID := pagewiki.TopicIDForTest("topic-notes", "observability")
	require.Len(t, tree.Topics, 3)
	byID := make(map[string]pagewiki.Topic, len(tree.Topics))
	for _, topic := range tree.Topics {
		byID[topic.ID] = topic
	}
	require.Equal(t, "topic-notes", byID[storageID].ParentID)
	require.Equal(t, "Observability", byID[observabilityID].Title)

	require.Len(t, tree.Placements, 4)
	expected := map[string]pagewiki.PagePlacement{
		first.ID:  {PageID: first.ID, TopicID: storageID, Rank: 0},
		second.ID: {PageID: second.ID, TopicID: storageID, Rank: 1},
		third.ID:  {PageID: third.ID, TopicID: observabilityID, Rank: 0},
		fourth.ID: {PageID: fourth.ID, TopicID: observabilityID, Rank: 1},
	}
	for pageID, want := range expected {
		got, found := placementFor(tree, pageID)
		require.True(t, found, pageID)
		require.Equal(t, want, got)
	}
}

func TestTreeMaintenanceSplitAtRootPlacesPreviouslyUnplacedPages(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	first := seedTreePage(t, repository, "sqlite", "SQLite")
	second := seedTreePage(t, repository, "postgres", "Postgres")
	navigator := &fakeTreeNavigator{splits: [][]pagewiki.TreeSplitGroup{{
		{Title: "Storage", Pages: []string{"sqlite"}},
		{Title: "Relational", Pages: []string{"postgres"}},
	}}}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeSplitForTest("")
	service.FlushTreeReindex(ctx)

	splits := navigator.splitCalls()
	require.Len(t, splits, 1)
	require.Empty(t, splits[0].Path, "an empty path means the root is being split")
	require.Len(t, splits[0].Pages, 2)

	tree := loadTree(t, repository)
	require.Len(t, tree.Topics, 2)
	storageID := pagewiki.TopicIDForTest("", "storage")
	placement, found := placementFor(tree, first.ID)
	require.True(t, found)
	require.Equal(t, storageID, placement.TopicID)
	_, found = placementFor(tree, second.ID)
	require.True(t, found)
}

func TestTreeMaintenanceSplitFailureKeepsTreeUnchanged(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	seeded := seedFullTopic(t, repository, 4)
	navigator := &fakeTreeNavigator{splitErr: errors.New("boom")}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeSplitForTest("topic-notes")
	service.FlushTreeReindex(ctx)

	require.Len(t, navigator.splitCalls(), 1)
	require.Equal(t, seeded, loadTree(t, repository))
}

func TestTreeMaintenanceSplitSkipsTopicsWithFewerThanTwoPages(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	seedFullTopic(t, repository, 1)
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeSplitForTest("topic-notes")
	service.FlushTreeReindex(ctx)

	require.Empty(t, navigator.splitCalls())
}

func TestTreeMaintenanceQueueDeduplicatesByTaskKey(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.EnqueueTreeInsertForTest(page.ID)
	require.Equal(t, 1, service.PendingTreeTasksForTest())

	service.FlushTreeReindex(ctx)
	require.Len(t, navigator.placementCalls(), 1)
	require.Equal(t, 0, service.PendingTreeTasksForTest())
}

func TestTreeMaintenanceFullQueueDropsTasksWithoutPanic(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	first := seedTreePage(t, repository, "sqlite", "SQLite")
	second := seedTreePage(t, repository, "postgres", "Postgres")
	third := seedTreePage(t, repository, "grafana", "Grafana")
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)
	service.ShrinkTreeQueueForTest(1)

	service.EnqueueTreeInsertForTest(first.ID)
	service.EnqueueTreeInsertForTest(second.ID)
	service.EnqueueTreeInsertForTest(third.ID)
	require.Equal(t, 1, service.PendingTreeTasksForTest())

	service.FlushTreeReindex(ctx)
	require.Len(t, navigator.placementCalls(), 1)
}

func TestTreeMaintenanceEnqueueUnplacedInsertsQueuesEveryUnplacedPage(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	placed := seedTreePage(t, repository, "sqlite", "SQLite")
	seedTreePage(t, repository, "postgres", "Postgres")
	seedTreePage(t, repository, "grafana", "Grafana")
	require.NoError(t, repository.ReplaceTopicTree(ctx, pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "topic-notes", Slug: "notes", Title: "Notes"}},
		Placements: []pagewiki.PagePlacement{
			{PageID: placed.ID, TopicID: "topic-notes", Rank: 0},
		},
	}))
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)

	service.EnqueueUnplacedInsertsForTest(ctx)

	require.Equal(t, 2, service.PendingTreeTasksForTest())
}

func TestTreeMaintenanceWithoutNavigatorIsANoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repository := memory.NewRepository()
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	service := pagewiki.NewService(repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{})

	service.StartTreeMaintenance(ctx) // must not start a worker or panic
	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx) // must drain harmlessly

	require.Empty(t, loadTree(t, repository).Placements)
}

// TestTreeMaintenanceWorkerAndFlushShareOneQueue is the -race guard: the
// background worker and a synchronous Flush drain the same channel and
// execute under the same mutex, so no task is ever processed twice
// concurrently.
func TestTreeMaintenanceWorkerAndFlushShareOneQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repository := memory.NewRepository()
	pages := make([]pagewiki.Page, 0, 6)
	for index := 0; index < 6; index++ {
		pages = append(pages, seedTreePage(
			t, repository, fmt.Sprintf("page-%02d", index), fmt.Sprintf("Page %02d", index),
		))
	}
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)
	service.StartTreeMaintenance(ctx)

	var waiter sync.WaitGroup
	waiter.Add(1)
	go func() {
		defer waiter.Done()
		for _, page := range pages {
			service.EnqueueTreeInsertForTest(page.ID)
		}
	}()
	waiter.Wait()
	require.Eventually(t, func() bool {
		service.FlushTreeReindex(ctx)
		return len(navigator.placementCalls()) == len(pages)
	}, 2*time.Second, 10*time.Millisecond)
}

func TestTreeMaintenanceRebuildTopicTreeReplacesEveryPlacement(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	// The in-memory catalog is served in slug order, so these three land
	// in the navigator in exactly this order: alpha, bravo, charlie.
	first := seedTreePage(t, repository, "alpha", "Alpha")
	second := seedTreePage(t, repository, "bravo", "Bravo")
	third := seedTreePage(t, repository, "charlie", "Charlie")
	// The stale tree shuffles the three pages under one topic, in an order
	// that matches neither their catalog order nor their reverse.
	require.NoError(t, repository.ReplaceTopicTree(ctx, pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "topic-stale", Slug: "stale", Title: "Stale"}},
		Placements: []pagewiki.PagePlacement{
			{PageID: third.ID, TopicID: "topic-stale", Rank: 0},
			{PageID: first.ID, TopicID: "topic-stale", Rank: 1},
			{PageID: second.ID, TopicID: "topic-stale", Rank: 2},
		},
	}))
	navigator := &fakeTreeNavigator{placements: []pagewiki.TreePlacementChoice{
		// alpha: creates a new "Group" topic and lands there.
		{Action: pagewiki.TreePlacementCreate, Title: "Group"},
		// bravo: enters the "Group" topic alpha just created.
		{Action: pagewiki.TreePlacementEnter, Slug: "group"},
		// charlie: stays at the root, so it gets no placement row at all.
		{Action: pagewiki.TreePlacementStay},
	}}
	service := newTreeService(repository, navigator)

	require.NoError(t, service.RebuildTopicTree(ctx))

	tree := loadTree(t, repository)
	groupID := pagewiki.TopicIDForTest("", "group")
	require.Len(t, tree.Topics, 1, "the stale topic is discarded, not carried over")
	require.Equal(t, groupID, tree.Topics[0].ID)
	require.Len(t, tree.Placements, 2, "only alpha and bravo landed in a topic")
	for _, page := range []pagewiki.Page{first, second} {
		placement, found := placementFor(tree, page.ID)
		require.True(t, found, page.ID)
		require.Equal(t, groupID, placement.TopicID)
	}
	_, charliePlaced := placementFor(tree, third.ID)
	require.False(t, charliePlaced, "charlie stayed at the root per the script, so it has no placement row")
}

func TestTreeMaintenanceRebuildTopicTreeWithoutNavigatorFails(t *testing.T) {
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{})

	err := service.RebuildTopicTree(context.Background())

	require.ErrorIs(t, err, pagewiki.ErrUnavailable)
}

// seedRootPages seeds count pages with no placement at all, which is what
// "sitting directly at the root" means to the tree.
func seedRootPages(t *testing.T, repository *memory.Repository, count int) []pagewiki.Page {
	t.Helper()
	pages := make([]pagewiki.Page, 0, count)
	for index := 0; index < count; index++ {
		pages = append(pages, seedTreePage(
			t, repository, fmt.Sprintf("root-%02d", index), fmt.Sprintf("Root %02d", index),
		))
	}
	return pages
}

// TestTreeMaintenanceRootOverflowEnqueuesRootSplit is the cold-start case: an
// empty tree gives the navigator nothing to enter, so pages pile up at the
// root until the root itself overflows and must split. The insert writes
// nothing (a page at the root has no placement), so the overflow check has to
// run on that no-write path too.
func TestTreeMaintenanceRootOverflowEnqueuesRootSplit(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	pages := seedRootPages(t, repository, 11)
	navigator := &fakeTreeNavigator{splitErr: errors.New("boom")}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(pages[0].ID)
	service.FlushTreeReindex(ctx)

	splits := navigator.splitCalls()
	require.Len(t, splits, 1, "an overflowing root must queue a split of the root itself")
	require.Empty(t, splits[0].Path, "an empty path means the root is being split")
	require.Len(t, splits[0].Pages, 11)
}

func TestTreeMaintenanceRootAtThresholdDoesNotSplit(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	pages := seedRootPages(t, repository, 10)
	navigator := &fakeTreeNavigator{}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(pages[0].ID)
	service.FlushTreeReindex(ctx)

	require.Empty(t, navigator.splitCalls(), "ten direct pages is the target, not an overflow")
}

// TestTreeMaintenanceSplitRechecksNewChildTopics covers the recursive half of
// splitting: a group that is itself still too large is split again, without
// anyone re-triggering the work from outside.
func TestTreeMaintenanceSplitRechecksNewChildTopics(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	seedFullTopic(t, repository, 12)
	crowded := make([]string, 0, 11)
	for index := 0; index < 11; index++ {
		crowded = append(crowded, fmt.Sprintf("note-%02d", index))
	}
	navigator := &fakeTreeNavigator{splits: [][]pagewiki.TreeSplitGroup{
		{
			{Title: "Storage", Pages: crowded},
			{Title: "Observability", Pages: []string{"note-11"}},
		},
		{
			{Title: "Rows", Pages: crowded[:6]},
			{Title: "Columns", Pages: crowded[6:]},
		},
	}}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeSplitForTest("topic-notes")
	service.FlushTreeReindex(ctx)

	splits := navigator.splitCalls()
	require.Len(t, splits, 2, "a child topic that is still overflowing must be split again")
	require.Equal(t, []string{"Notes", "Storage"}, splits[1].Path)
	require.Len(t, splits[1].Pages, 11)

	tree := loadTree(t, repository)
	storageID := pagewiki.TopicIDForTest("topic-notes", "storage")
	rowsID := pagewiki.TopicIDForTest(storageID, "rows")
	columnsID := pagewiki.TopicIDForTest(storageID, "columns")
	require.Len(t, tree.Topics, 5)
	placement, found := placementFor(tree, "page-note-00")
	require.True(t, found)
	require.Equal(t, rowsID, placement.TopicID)
	placement, found = placementFor(tree, "page-note-10")
	require.True(t, found)
	require.Equal(t, columnsID, placement.TopicID)
	require.Equal(t, 4, placement.Rank, "rank is the page's index inside its group")
}

// seedTopicChain seeds a root-to-leaf chain of depth topics and returns the
// deepest topic's ID; every level but the last is a single-child link.
func seedTopicChain(t *testing.T, repository *memory.Repository, depth int) (pagewiki.TopicTree, string) {
	t.Helper()
	tree := pagewiki.TopicTree{}
	parentID := ""
	for level := 1; level <= depth; level++ {
		id := fmt.Sprintf("topic-level-%d", level)
		tree.Topics = append(tree.Topics, pagewiki.Topic{
			ID:       id,
			ParentID: parentID,
			Slug:     fmt.Sprintf("level-%d", level),
			Title:    fmt.Sprintf("Level %d", level),
		})
		parentID = id
	}
	return tree, parentID
}

// TestTreeMaintenanceDeepestTopicNeitherSplitsNorCreates asserts both depth
// guards at once: the navigator is told it may not create another level once
// it is looking at a MaxDepth topic, and an overflowing MaxDepth topic is not
// queued for a split it could never apply.
func TestTreeMaintenanceDeepestTopicNeitherSplitsNorCreates(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()
	tree, deepestID := seedTopicChain(t, repository, pagewiki.DefaultTopicTreeMaxDepth)
	for index := 0; index < 11; index++ {
		page := seedTreePage(t, repository, fmt.Sprintf("deep-%02d", index), fmt.Sprintf("Deep %02d", index))
		tree.Placements = append(tree.Placements, pagewiki.PagePlacement{
			PageID: page.ID, TopicID: deepestID, Rank: index,
		})
	}
	require.NoError(t, repository.ReplaceTopicTree(ctx, tree))
	page := seedTreePage(t, repository, "sqlite", "SQLite")
	choices := make([]pagewiki.TreePlacementChoice, 0, pagewiki.DefaultTopicTreeMaxDepth)
	for level := 1; level <= pagewiki.DefaultTopicTreeMaxDepth; level++ {
		choices = append(choices, pagewiki.TreePlacementChoice{
			Action: pagewiki.TreePlacementEnter, Slug: fmt.Sprintf("level-%d", level),
		})
	}
	navigator := &fakeTreeNavigator{placements: choices}
	service := newTreeService(repository, navigator)

	service.EnqueueTreeInsertForTest(page.ID)
	service.FlushTreeReindex(ctx)

	calls := navigator.placementCalls()
	require.Len(t, calls, pagewiki.DefaultTopicTreeMaxDepth+1)
	require.True(t, calls[pagewiki.DefaultTopicTreeMaxDepth-1].AllowCreate,
		"one level above the deepest, another topic may still be created")
	require.False(t, calls[pagewiki.DefaultTopicTreeMaxDepth].AllowCreate,
		"the deepest topic may not create another level")

	stored := loadTree(t, repository)
	placement, found := placementFor(stored, page.ID)
	require.True(t, found)
	require.Equal(t, deepestID, placement.TopicID)
	require.Empty(t, navigator.splitCalls(),
		"a topic at MaxDepth cannot host child topics, so it is never queued for a split")
}
