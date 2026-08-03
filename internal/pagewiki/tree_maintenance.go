package pagewiki

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

const (
	// treeTaskInsert places one page into the tree; treeTaskSplit breaks one
	// overflowing topic (an empty id means the root) into child topics.
	treeTaskInsert = "insert"
	treeTaskSplit  = "split"
	// treeTaskQueueSize bounds the pending maintenance work. Tasks are keyed
	// and deduplicated, so the queue only ever holds distinct pages/topics;
	// overflowing it drops work rather than blocking ingestion, and the next
	// catalog change (or a rebuild) re-queues whatever was dropped.
	treeTaskQueueSize = 256
	// treeMaxDirectPages caps how many direct entries (pages plus child
	// topics) a topic — or the root — can hold before an overflow split is
	// queued.
	treeMaxDirectPages = 10
)

// treeTask is one unit of incremental topic-tree maintenance. Unlike the
// whole-tree reindex it replaced, each task touches one page or one topic,
// so the LLM only ever sees the neighbourhood it is deciding about.
type treeTask struct {
	kind string
	id   string
}

func (t treeTask) key() string { return t.kind + ":" + t.id }

// TreeMaintenanceConfig configures WithTreeNavigator.
type TreeMaintenanceConfig struct {
	Navigator TreeNavigator
	MaxDepth  int // 0 → DefaultTopicTreeMaxDepth
	Logger    *slog.Logger
}

// enqueueTreeTask queues one task, deduplicated by key: a page already
// waiting to be placed (or a topic already waiting to be split) is not queued
// twice, because the task carries no payload — it re-reads the catalog and
// tree when it runs. A full queue drops the task with a warning instead of
// blocking the caller, which is always an ingest or curation path.
func (s *Service) enqueueTreeTask(task treeTask) {
	key := task.key()
	s.treeTaskMu.Lock()
	if _, pending := s.treeTaskKeys[key]; pending {
		s.treeTaskMu.Unlock()
		return
	}
	s.treeTaskKeys[key] = struct{}{}
	s.treeTaskMu.Unlock()
	select {
	case s.treeTasks <- task:
	default:
		s.treeTaskMu.Lock()
		delete(s.treeTaskKeys, key)
		s.treeTaskMu.Unlock()
		s.logger.Warn("Page Wiki tree task queue full", "kind", task.kind, "id", task.id)
	}
}

// enqueueUnplacedInserts queues an insert for every active page that has no
// placement. Curation calls it after a round changed the catalog: merges and
// rewrites republish pages that may never have been placed, and a retired
// page's stale placement is filtered out by the repository's Navigation, so
// nothing needs cleaning up here.
func (s *Service) enqueueUnplacedInserts(ctx context.Context) {
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree enqueue skipped", "step", "load catalog", "error", err)
		return
	}
	tree, err := s.repository.TopicTree(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree enqueue skipped", "step", "load tree", "error", err)
		return
	}
	placed := placementsByPage(tree)
	for _, entry := range catalog {
		if _, found := placed[entry.ID]; found {
			continue
		}
		s.enqueueTreeTask(treeTask{kind: treeTaskInsert, id: entry.ID})
	}
}

// StartTreeMaintenance drains the task queue in the background, keeping every
// LLM call off the ingest critical path. Tasks pending when ctx is cancelled
// are dropped: the tree is a derived view, and the next catalog change (or an
// explicit rebuild) re-queues the work.
func (s *Service) StartTreeMaintenance(ctx context.Context) {
	if s.treeNavigator == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case task := <-s.treeTasks:
				s.processTreeTask(ctx, task)
			}
		}
	}()
}

// FlushTreeReindex drains the queue synchronously and returns once it is
// empty. Tests and the rebuild flow use it to make the async contract
// synchronous; it is safe to call while the background worker is running,
// since both drain the same channel and execute under the same mutex.
// Note: tasks already claimed by the background worker and executing
// concurrently are not awaited by this method, so the tree is eventually
// consistent rather than strictly fresh when this call returns.
func (s *Service) FlushTreeReindex(ctx context.Context) {
	for {
		select {
		case task := <-s.treeTasks:
			s.processTreeTask(ctx, task)
		default:
			return
		}
	}
}

// processTreeTask runs one task. The dedup key is released before the work
// starts, so a change arriving mid-task queues a fresh task instead of being
// swallowed. Every failure is logged and swallowed: tree maintenance is
// best-effort and must never fail ingestion, and the stored tree simply stays
// as it was.
func (s *Service) processTreeTask(ctx context.Context, task treeTask) {
	s.treeTaskMu.Lock()
	delete(s.treeTaskKeys, task.key())
	s.treeTaskMu.Unlock()
	if s.treeNavigator == nil {
		return
	}
	s.treeReindexMu.Lock()
	defer s.treeReindexMu.Unlock()
	switch task.kind {
	case treeTaskInsert:
		s.insertPage(ctx, task.id)
	case treeTaskSplit:
		s.splitTopic(ctx, task.id)
	}
}

// RebuildTopicTree discards the stored topic tree and re-places every active
// page from scratch, synchronously. It is the escape hatch for a tree that
// incremental maintenance has drifted into a shape the team dislikes; normal
// operation never needs it.
//
// Whole rebuilds are serialized on treeRebuildMu: a concurrent call waits for
// the in-flight rebuild to finish and then rebuilds again from scratch.
// Without this, the second rebuild's clear could wipe placements the first
// had already committed — their queue tasks were consumed, so nothing would
// ever re-place those pages. The mutex is dedicated to whole rebuilds:
// per-task work only takes treeReindexMu, so ingestion and incremental
// maintenance never stall behind a running rebuild's LLM calls.
func (s *Service) RebuildTopicTree(ctx context.Context) error {
	if s.treeNavigator == nil {
		return fmt.Errorf("rebuild Page Wiki topic tree: %w: no tree navigator is configured", ErrUnavailable)
	}
	s.treeRebuildMu.Lock()
	defer s.treeRebuildMu.Unlock()
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		return fmt.Errorf("rebuild Page Wiki topic tree: load Page catalog: %w", err)
	}
	s.treeReindexMu.Lock()
	err = s.repository.ReplaceTopicTree(ctx, TopicTree{})
	s.treeReindexMu.Unlock()
	if err != nil {
		return fmt.Errorf("rebuild Page Wiki topic tree: clear tree: %w", err)
	}
	// One page at a time: each insert (plus any split it triggers) is drained
	// before the next is queued, so a catalog larger than the queue can never
	// overflow it.
	for _, entry := range catalog {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("rebuild Page Wiki topic tree: %w", err)
		}
		s.enqueueTreeTask(treeTask{kind: treeTaskInsert, id: entry.ID})
		s.FlushTreeReindex(ctx)
	}
	return nil
}

// treeState is the read-only snapshot every task starts from.
type treeState struct {
	catalog    PageCatalog
	byID       map[string]PageCatalogEntry
	tree       TopicTree
	directives GenerationDirectives
}

// loadTreeState loads catalog, tree, and directives. Catalog or tree failures
// abort the task; a directives failure only degrades the prompt, so it warns
// and continues with the zero value.
func (s *Service) loadTreeState(ctx context.Context, task string) (treeState, bool) {
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree task skipped", "task", task, "step", "load catalog", "error", err)
		return treeState{}, false
	}
	tree, err := s.repository.TopicTree(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree task skipped", "task", task, "step", "load tree", "error", err)
		return treeState{}, false
	}
	directives, err := s.repository.GenerationSettings(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree task degraded", "task", task, "step", "load settings", "error", err)
		directives = GenerationDirectives{}
	}
	byID := make(map[string]PageCatalogEntry, len(catalog))
	for _, entry := range catalog {
		byID[entry.ID] = entry
	}
	return treeState{catalog: catalog, byID: byID, tree: tree, directives: directives}, true
}

// treeLanding is where a descent ended: the topic the page belongs to (empty
// = root), its depth, whether the descent itself changed the tree (by
// creating a topic), and whether the navigator failed outright.
type treeLanding struct {
	tree    TopicTree
	topicID string
	depth   int
	changed bool
	aborted bool
}

// insertPage places one page. It is idempotent by construction: a page that
// already has a placement, or that has left the catalog (retired), is
// skipped without ever asking the navigator.
func (s *Service) insertPage(ctx context.Context, pageID string) {
	state, loaded := s.loadTreeState(ctx, treeTaskInsert)
	if !loaded {
		return
	}
	entry, listed := state.byID[pageID]
	if !listed {
		return
	}
	if _, placed := placementsByPage(state.tree)[pageID]; placed {
		return
	}
	landing := s.descend(ctx, state, entry)
	if landing.aborted {
		return
	}
	tree := landing.tree
	if landing.topicID != "" {
		tree.Placements = append(tree.Placements, PagePlacement{
			PageID:  pageID,
			TopicID: landing.topicID,
			Rank:    len(directPlacements(tree, landing.topicID)),
		})
		landing.changed = true
	}
	if landing.changed {
		if err := s.repository.ReplaceTopicTree(ctx, tree); err != nil {
			s.logger.Warn("Page Wiki page placement skipped", "page", pageID, "step", "replace tree", "error", err)
			return
		}
	}
	// The overflow check runs even when nothing was written: a page that
	// stayed at the root changes no placement, yet it is exactly how the root
	// fills up on a cold start, and the root is the one topic that always has
	// room to split.
	s.checkOverflow(tree, state.byID, landing.topicID, landing.depth)
}

// descend walks the tree from the root, asking the navigator one level at a
// time where the page belongs. "enter" moves down, "create" adds a child
// topic and lands there, and anything else — including an unusable create —
// lands at the current level.
func (s *Service) descend(ctx context.Context, state treeState, entry PageCatalogEntry) treeLanding {
	landing := treeLanding{tree: state.tree}
	path := make([]string, 0, s.treeMaxDepth)
	for step := 0; step <= s.treeMaxDepth; step++ {
		children := childTopics(landing.tree, landing.topicID)
		choice, err := s.treeNavigator.ChoosePlacement(ctx, TreePlacementInput{
			Page:        entry,
			Path:        path,
			Children:    childViews(landing.tree, state.byID, children),
			AllowCreate: landing.depth < s.treeMaxDepth,
			Directives:  state.directives,
		})
		if err != nil {
			s.logger.Warn("Page Wiki page placement skipped", "page", entry.ID, "step", "choose", "error", err)
			landing.aborted = true
			return landing
		}
		switch choice.Action {
		case TreePlacementEnter:
			child, found := childBySlug(children, choice.Slug)
			if !found {
				return landing
			}
			landing.topicID, landing.depth = child.ID, landing.depth+1
			path = append(path, child.Title)
		case TreePlacementCreate:
			topic, usable := newChildTopic(landing.topicID, choice.Title, children)
			if !usable {
				return landing
			}
			landing.tree.Topics = append(landing.tree.Topics, topic)
			landing.topicID, landing.depth, landing.changed = topic.ID, landing.depth+1, true
			return landing
		default:
			return landing
		}
	}
	return landing
}

// checkOverflow queues a split when a topic now holds more direct entries —
// pages plus child topics — than a reader can scan, and the tree still has
// room for another level. The root counts as depth 0, so it can always split.
func (s *Service) checkOverflow(
	tree TopicTree,
	catalog map[string]PageCatalogEntry,
	topicID string,
	depth int,
) {
	if depth >= s.treeMaxDepth {
		return
	}
	entries := len(directPages(tree, catalog, topicID)) + len(childTopics(tree, topicID))
	if entries > treeMaxDirectPages {
		s.enqueueTreeTask(treeTask{kind: treeTaskSplit, id: topicID})
	}
}

// splitTopic regroups one overflowing topic's direct pages into new child
// topics. It is a single tree write: the child topics and the moved
// placements land together, so a reader never sees a half-applied split.
func (s *Service) splitTopic(ctx context.Context, topicID string) {
	state, loaded := s.loadTreeState(ctx, treeTaskSplit)
	if !loaded {
		return
	}
	if _, found := findTopic(state.tree, topicID); topicID != "" && !found {
		return
	}
	pages := directPages(state.tree, state.byID, topicID)
	if len(pages) < 2 {
		return
	}
	children := childTopics(state.tree, topicID)
	groups, err := s.treeNavigator.SplitTopic(ctx, TreeSplitInput{
		Path:       topicPath(state.tree, topicID),
		Pages:      splitPageViews(pages),
		Forbidden:  topicSlugs(children),
		Directives: state.directives,
	})
	if err != nil {
		s.logger.Warn("Page Wiki topic split skipped", "topic", topicID, "step", "split", "error", err)
		return
	}
	tree, created, usable := applySplit(state.tree, topicID, pages, children, groups)
	if !usable {
		s.logger.Warn("Page Wiki topic split abandoned", "topic", topicID, "reason", "unusable group titles")
		return
	}
	if len(created) == 0 {
		return
	}
	if err := s.repository.ReplaceTopicTree(ctx, tree); err != nil {
		s.logger.Warn("Page Wiki topic split skipped", "topic", topicID, "step", "replace tree", "error", err)
		return
	}
	depth := topicDepth(tree, topicID) + 1
	for _, child := range created {
		s.checkOverflow(tree, state.byID, child.ID, depth)
	}
}

// applySplit turns the navigator's groups into child topics plus relocated
// placements. A group title that yields no slug, or one that collides with an
// existing sibling or another group, abandons the whole split: a partial
// regrouping is worse than none.
func applySplit(
	tree TopicTree,
	parentID string,
	pages []PageCatalogEntry,
	siblings []Topic,
	groups []TreeSplitGroup,
) (TopicTree, []Topic, bool) {
	pageIDBySlug := make(map[string]string, len(pages))
	for _, page := range pages {
		pageIDBySlug[page.Slug] = page.ID
	}
	taken := make(map[string]struct{}, len(siblings)+len(groups))
	for _, sibling := range siblings {
		taken[sibling.Slug] = struct{}{}
	}
	created := make([]Topic, 0, len(groups))
	moved := make([]PagePlacement, 0, len(pages))
	movedPages := make(map[string]struct{}, len(pages))
	for _, group := range groups {
		topic, usable := newChildTopic(parentID, group.Title, nil)
		if !usable {
			return TopicTree{}, nil, false
		}
		if _, clash := taken[topic.Slug]; clash {
			return TopicTree{}, nil, false
		}
		taken[topic.Slug] = struct{}{}
		created = append(created, topic)
		for rank, slug := range group.Pages {
			pageID, known := pageIDBySlug[slug]
			if !known {
				continue
			}
			moved = append(moved, PagePlacement{PageID: pageID, TopicID: topic.ID, Rank: rank})
			movedPages[pageID] = struct{}{}
		}
	}
	next := TopicTree{
		Topics:     append(append(make([]Topic, 0, len(tree.Topics)+len(created)), tree.Topics...), created...),
		Placements: make([]PagePlacement, 0, len(tree.Placements)+len(moved)),
	}
	for _, placement := range tree.Placements {
		if _, relocated := movedPages[placement.PageID]; relocated {
			continue
		}
		next.Placements = append(next.Placements, placement)
	}
	next.Placements = append(next.Placements, moved...)
	return next, created, true
}

// newChildTopic derives a child topic from a navigator-proposed title,
// rejecting an empty slug or one that collides with an existing sibling.
func newChildTopic(parentID, title string, siblings []Topic) (Topic, bool) {
	trimmed := strings.TrimSpace(title)
	slug := topicSlug(trimmed)
	if slug == "" {
		return Topic{}, false
	}
	for _, sibling := range siblings {
		if sibling.Slug == slug {
			return Topic{}, false
		}
	}
	return Topic{
		ID:       StableID("topic", parentID, slug),
		ParentID: parentID,
		Slug:     slug,
		Title:    trimmed,
	}, true
}

func placementsByPage(tree TopicTree) map[string]PagePlacement {
	placements := make(map[string]PagePlacement, len(tree.Placements))
	for _, placement := range tree.Placements {
		placements[placement.PageID] = placement
	}
	return placements
}

func findTopic(tree TopicTree, topicID string) (Topic, bool) {
	for _, topic := range tree.Topics {
		if topic.ID == topicID {
			return topic, true
		}
	}
	return Topic{}, false
}

func childTopics(tree TopicTree, parentID string) []Topic {
	children := make([]Topic, 0)
	for _, topic := range tree.Topics {
		if topic.ParentID == parentID {
			children = append(children, topic)
		}
	}
	return children
}

func childBySlug(children []Topic, slug string) (Topic, bool) {
	for _, child := range children {
		if child.Slug == slug {
			return child, true
		}
	}
	return Topic{}, false
}

// topicSlug derives a URL-safe slug from an LLM-proposed topic title.
func topicSlug(title string) string {
	return strings.Trim(nonSlugCharacter.ReplaceAllString(
		strings.ToLower(strings.TrimSpace(title)), "-",
	), "-")
}

func topicSlugs(topics []Topic) []string {
	slugs := make([]string, 0, len(topics))
	for _, topic := range topics {
		slugs = append(slugs, topic.Slug)
	}
	return slugs
}

// childViews describes each child topic to the navigator, sized by the number
// of active pages in its whole subtree.
func childViews(tree TopicTree, catalog map[string]PageCatalogEntry, children []Topic) []TreeChildTopic {
	views := make([]TreeChildTopic, 0, len(children))
	for _, child := range children {
		views = append(views, TreeChildTopic{
			Slug:  child.Slug,
			Title: child.Title,
			Pages: subtreePageCount(tree, catalog, child.ID),
		})
	}
	return views
}

func subtreePageCount(tree TopicTree, catalog map[string]PageCatalogEntry, topicID string) int {
	count := len(directPages(tree, catalog, topicID))
	for _, child := range childTopics(tree, topicID) {
		count += subtreePageCount(tree, catalog, child.ID)
	}
	return count
}

// directPlacements returns every placement pointing at topicID, including
// placements left behind by retired pages: it answers "how many ranks are
// already taken here", which is what a new placement's rank builds on.
func directPlacements(tree TopicTree, topicID string) []PagePlacement {
	placements := make([]PagePlacement, 0)
	for _, placement := range tree.Placements {
		if placement.TopicID == topicID {
			placements = append(placements, placement)
		}
	}
	return placements
}

// directPages returns the active pages sitting directly under topicID, in
// rank order. The root (an empty topicID) holds every active page that has no
// placement at all, in catalog order.
func directPages(tree TopicTree, catalog map[string]PageCatalogEntry, topicID string) []PageCatalogEntry {
	if topicID == "" {
		return rootPages(tree, catalog)
	}
	ranked := directPlacements(tree, topicID)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Rank != ranked[j].Rank {
			return ranked[i].Rank < ranked[j].Rank
		}
		return ranked[i].PageID < ranked[j].PageID
	})
	pages := make([]PageCatalogEntry, 0, len(ranked))
	for _, placement := range ranked {
		entry, active := catalog[placement.PageID]
		if !active {
			continue
		}
		pages = append(pages, entry)
	}
	return pages
}

func rootPages(tree TopicTree, catalog map[string]PageCatalogEntry) []PageCatalogEntry {
	placed := placementsByPage(tree)
	slugs := make([]string, 0, len(catalog))
	for id := range catalog {
		if _, found := placed[id]; found {
			continue
		}
		slugs = append(slugs, id)
	}
	sort.Strings(slugs)
	pages := make([]PageCatalogEntry, 0, len(slugs))
	for _, id := range slugs {
		pages = append(pages, catalog[id])
	}
	return pages
}

func splitPageViews(pages []PageCatalogEntry) []TreeSplitPage {
	views := make([]TreeSplitPage, 0, len(pages))
	for _, page := range pages {
		views = append(views, TreeSplitPage{Slug: page.Slug, Title: page.Title, Summary: page.Summary})
	}
	return views
}

// topicPath returns the topic titles from the root down to topicID; the root
// itself has an empty path. The step bound is defensive: the repository
// rejects cyclic parents, so it can only ever be reached by a corrupt tree.
func topicPath(tree TopicTree, topicID string) []string {
	titles := make([]string, 0)
	id := topicID
	for step := 0; id != "" && step <= len(tree.Topics); step++ {
		topic, found := findTopic(tree, id)
		if !found {
			break
		}
		titles = append([]string{topic.Title}, titles...)
		id = topic.ParentID
	}
	return titles
}

// topicDepth counts the levels between the root (depth 0) and topicID.
func topicDepth(tree TopicTree, topicID string) int {
	return len(topicPath(tree, topicID))
}
