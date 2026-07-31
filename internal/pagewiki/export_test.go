package pagewiki

// Exported for the external pagewiki_test package: it needs to assert that
// the zero-directives system prompt is byte-identical to today's prompt
// constants, which are otherwise unexported.
const (
	PageWikiPlannerPromptForTest       = pageWikiPlannerPrompt
	PageWikiEnglishEditorPromptForTest = pageWikiEnglishEditorPrompt
)

func TreeIndexerPromptForTest(maxDepth int) string {
	return treeIndexerPrompt(maxDepth)
}
