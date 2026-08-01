package pagewiki

// Exported for the external pagewiki_test package: it needs to assert that
// the zero-directives system prompt is byte-identical to today's prompt
// constants, which are otherwise unexported.
const (
	PageWikiPlannerPromptForTest       = pageWikiPlannerPrompt
	PageWikiEnglishEditorPromptForTest = pageWikiEnglishEditorPrompt
	TreeDefaultMaxDepthForTest         = treeDefaultMaxDepth
)

func TreeIndexerPromptForTest(maxDepth int) string {
	return treeIndexerPrompt(maxDepth)
}

// TreeDirtyForTest reports whether a topic-tree dirty mark is currently
// pending on the service, without consuming it: acceptance tests use it to
// assert markTreeDirty fired after a curation round, independent of whether a
// TreeIndexer is configured (FlushTreeReindex is a no-op without one).
func (s *Service) TreeDirtyForTest() bool {
	select {
	case pending := <-s.treeDirty:
		s.treeDirty <- pending
		return true
	default:
		return false
	}
}
