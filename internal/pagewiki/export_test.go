package pagewiki

import "context"

// Exported for the external pagewiki_test package: it needs to assert that
// the zero-directives system prompt is byte-identical to today's prompt
// constants, which are otherwise unexported.
const (
	PageWikiPlannerPromptForTest       = pageWikiPlannerPrompt
	PageWikiEnglishEditorPromptForTest = pageWikiEnglishEditorPrompt
)

// TopicIDForTest exposes the Topic ID derivation so tests can assert the
// service created exactly the topic it was supposed to, without duplicating
// the hashing rule.
func TopicIDForTest(parentID, slug string) string {
	return StableID("topic", parentID, slug)
}

// EnqueueTreeInsertForTest queues one page for (re)placement, the same task
// InjectSession queues after a successful target.
func (s *Service) EnqueueTreeInsertForTest(pageID string) {
	s.enqueueTreeTask(treeTask{kind: treeTaskInsert, id: pageID})
}

// EnqueueTreeSplitForTest queues one topic for splitting (an empty topicID
// means the root), the same task an overflow check queues.
func (s *Service) EnqueueTreeSplitForTest(topicID string) {
	s.enqueueTreeTask(treeTask{kind: treeTaskSplit, id: topicID})
}

// EnqueueUnplacedInsertsForTest exposes the curation hook: every active page
// without a placement is queued for insertion.
func (s *Service) EnqueueUnplacedInsertsForTest(ctx context.Context) {
	s.enqueueUnplacedInserts(ctx)
}

// DissolveUnderfullTopicsForTest exposes the curation hook that collapses
// leaf topics left with at most one active direct page.
func (s *Service) DissolveUnderfullTopicsForTest(ctx context.Context) {
	s.dissolveUnderfullTopics(ctx)
}

// PendingTreeTasksForTest reports how many tree tasks are queued but not yet
// processed. Acceptance tests use it to assert that a catalog change queued
// re-placement work, independent of whether a TreeNavigator is configured.
func (s *Service) PendingTreeTasksForTest() int {
	return len(s.treeTasks)
}

// ShrinkTreeQueueForTest replaces the task queue with a smaller one so a test
// can exercise the queue-full path without enqueueing hundreds of tasks. It
// must be called before any task is enqueued.
func (s *Service) ShrinkTreeQueueForTest(capacity int) {
	s.treeTasks = make(chan treeTask, capacity)
}
