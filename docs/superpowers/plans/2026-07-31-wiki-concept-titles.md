# Wiki Concept Titles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make generated wiki page titles concise concept noun phrases and page identity concept-shaped (not event-shaped), via prompt rules in planner/editor/tree-indexer plus two deterministic title guards in the planner.

**Architecture:** All three LLM stages live in `internal/pagewiki` as prompt constants consumed by thin Go wrappers. The prompt constants get new rules; the planner's `acceptBrief` create path gets a title normalizer/guard. No schema, API, or frontend change.

**Tech Stack:** Go, testify `suite`, existing `wikiChatClient` fake (defined in `llm_session_editor_test.go`), exported prompt constants in `export_test.go`.

**Spec:** `docs/superpowers/specs/2026-07-31-wiki-concept-titles-design.md`

## Global Constraints

- Prompt rule: titles are concept noun phrases of **at most five words**, no trailing period, never opening with a verb/gerund ("Fixing", "Adding", "Improving", "Updating").
- Code guard (planner only, deliberately looser than the prompt): reject a create brief whose title exceeds **9 words or 80 runes**; strip trailing `.` / `。` before checking.
- Guard rejection behaves exactly like an empty title today: `acceptBrief` returns `false`, the brief is dropped.
- Tests are black-box (`package pagewiki_test`), testify suite style, no real LLM calls.
- Run package tests with: `go test ./internal/pagewiki/`

---

### Task 1: Planner title guards (normalize + reject)

**Files:**
- Modify: `internal/pagewiki/llm_session_planner.go` (const block at :15, `acceptBrief` create path at :240-263)
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces:**
- Produces: unexported `normalizeProposedTitle(raw string) string` in `llm_session_planner.go` — returns the trimmed, period-stripped title, or `""` when the title exceeds 9 words or 80 runes (callers treat `""` as reject). Constants `plannerTitleMaxWords = 9`, `plannerTitleMaxRunes = 80`.
- Consumes: nothing from other tasks.

- [ ] **Step 1: Write the failing tests**

Append to `internal/pagewiki/llm_session_planner_test.go`:

```go
func (s *llmSessionPlannerSuite) TestStripsTrailingPeriodFromProposedTitle() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
		{"action":"create","proposed_slug":"release-policy","proposed_title":"Release Policy.",
		 "reader_goal":"Understand the release cadence.",
		 "evidence":[{"event_id":"event-1","exact_quote":"releases ship weekly"}]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal(pagewiki.PageActionCreate, briefs[0].Action)
	s.Equal("Release Policy", briefs[0].ProposedTitle)
}

func (s *llmSessionPlannerSuite) TestRejectsSentenceShapedTitles() {
	longByWords := "Fixing the planner so that xanadu links are created on the LLM planner path"
	longByRunes := strings.Repeat("ab", 45) // 90 runes, one word
	client := &wikiChatClient{responsesByIndex: []string{fmt.Sprintf(`{"briefs":[
		{"action":"create","proposed_slug":"planner-fix","proposed_title":%q,
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"long-rune-title","proposed_title":%q,
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"nine-word-title",
		 "proposed_title":"One Two Three Four Five Six Seven Eight Nine",
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`, longByWords, longByRunes)}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	// The two over-limit titles drop their briefs; the 9-word boundary title survives.
	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal("nine-word-title", briefs[0].ProposedSlug)
	s.Equal("One Two Three Four Five Six Seven Eight Nine", briefs[0].ProposedTitle)
}
```

Both `strings` and `fmt` are already imported by this test file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pagewiki/ -run 'TestLLMSessionPlannerSuite/(TestStripsTrailingPeriodFromProposedTitle|TestRejectsSentenceShapedTitles)' -v`
Expected: FAIL — `TestStripsTrailingPeriodFromProposedTitle` gets `"Release Policy."` (period kept); `TestRejectsSentenceShapedTitles` gets 3 briefs instead of 1.

- [ ] **Step 3: Implement the guard**

In `internal/pagewiki/llm_session_planner.go`:

Add to the existing const block (after `planEmptyBriefKey`):

```go
	plannerTitleMaxWords = 9
	plannerTitleMaxRunes = 80
```

Add `"unicode/utf8"` to the imports.

In `acceptBrief`, replace the create-path title line:

```go
		title := strings.TrimSpace(candidate.ProposedTitle)
```

with:

```go
		title := normalizeProposedTitle(candidate.ProposedTitle)
```

Add below `acceptBrief`:

```go
// normalizeProposedTitle trims the title and strips trailing periods; it
// returns "" for titles long enough to read as sentences, which drops the
// brief the same way an empty title does. The bounds are looser than the
// prompt's five-word rule on purpose: the prompt shapes, the guard only
// catches egregious sentence-titles.
func normalizeProposedTitle(raw string) string {
	title := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(raw), ".。"))
	if utf8.RuneCountInString(title) > plannerTitleMaxRunes ||
		len(strings.Fields(title)) > plannerTitleMaxWords {
		return ""
	}
	return title
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v`
Expected: PASS, including all pre-existing planner tests.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_planner.go internal/pagewiki/llm_session_planner_test.go
git commit -m "feat(pagewiki): planner rejects sentence-shaped page titles"
```

---

### Task 2: Planner prompt — concept page identity + title style

**Files:**
- Modify: `internal/pagewiki/llm_session_planner.go` (`pageWikiPlannerPrompt` at :353)
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces:**
- Consumes: exported test constant `pagewiki.PageWikiPlannerPromptForTest` (already in `export_test.go`, aliases `pageWikiPlannerPrompt`).
- Produces: new prompt paragraph other tasks do not depend on.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/llm_session_planner_test.go`:

```go
func (s *llmSessionPlannerSuite) TestPlannerPromptPinsConceptIdentityAndTitleStyle() {
	prompt := pagewiki.PageWikiPlannerPromptForTest
	s.Contains(prompt, "durable concept")
	s.Contains(prompt, "never an activity")
	s.Contains(prompt, "at most five words")
	s.Contains(prompt, "no trailing period")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run 'TestLLMSessionPlannerSuite/TestPlannerPromptPinsConceptIdentityAndTitleStyle' -v`
Expected: FAIL — prompt does not contain "durable concept".

- [ ] **Step 3: Extend the prompt**

In `pageWikiPlannerPrompt` (`llm_session_planner.go`), insert a new paragraph between the "Keep only knowledge…" noise paragraph (ends with "When in doubt, skip.") and the "Updating an existing page is the rule…" paragraph:

```
A page's subject is a durable concept — a component, subsystem, decision,
convention, or domain fact — never an activity, task, fix, or session.
Before proposing a create, name the concept the evidence is about: the
page is about that concept, and this session's events are only new
evidence for it. proposed_title is a concise noun phrase naming that
concept — at most five words, never a sentence, no trailing period, and
never opening with a verb or gerund such as "Fixing", "Adding",
"Improving", or "Updating". "Xanadu Links" is a title; "Fixing xanadu
links on the planner path" is not.
```

Keep the surrounding paragraphs byte-identical; blank line before and after the new paragraph, matching the prompt's existing paragraph separation.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v`
Expected: PASS — the new pin test and all existing ones (including `TestZeroGenerationDirectivesLeaveSystemPromptUnchanged`, which compares against the same constant and therefore still passes).

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_planner.go internal/pagewiki/llm_session_planner_test.go
git commit -m "feat(pagewiki): planner prompt pins concept page identity and title style"
```

---

### Task 3: Editor prompt — keep titles and headings concept-shaped

**Files:**
- Modify: `internal/pagewiki/llm_session_editor.go` (`pageWikiEnglishEditorPrompt` at :275)
- Test: `internal/pagewiki/llm_session_editor_test.go`

**Interfaces:**
- Consumes: exported test constant `pagewiki.PageWikiEnglishEditorPromptForTest` (already in `export_test.go`).
- Produces: new prompt sentences other tasks do not depend on.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/llm_session_editor_test.go`:

```go
func (s *llmSessionEditorSuite) TestEditorPromptPinsConceptTitleStyle() {
	prompt := pagewiki.PageWikiEnglishEditorPromptForTest
	s.Contains(prompt, "concise noun phrase")
	s.Contains(prompt, "at most five words")
	s.Contains(prompt, "keep it")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run 'TestLLMSessionEditorSuite/TestEditorPromptPinsConceptTitleStyle' -v`
Expected: FAIL — prompt does not contain "concise noun phrase".

- [ ] **Step 3: Extend the prompt**

In `pageWikiEnglishEditorPrompt` (`llm_session_editor.go`), insert after the sentence "Preserve proper nouns accurately." and before "evidence lists the exact quotes…":

```
The title is a concise noun phrase naming the page's concept — at most
five words, never a sentence, no trailing period; when current_text
already carries such a title, keep it rather than rewriting it. Each
section heading is likewise a short noun phrase, not a sentence.
```

Keep the rest of the prompt byte-identical.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -run LLMSessionEditor -v`
Expected: PASS, including `TestZeroGenerationDirectivesLeaveSystemPromptUnchanged`.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_editor.go internal/pagewiki/llm_session_editor_test.go
git commit -m "feat(pagewiki): editor prompt keeps titles and headings concept-shaped"
```

---

### Task 4: Tree indexer prompt — short topic names

**Files:**
- Modify: `internal/pagewiki/llm_tree_indexer.go` (`pageWikiTreeIndexerPromptTemplate` at :395)
- Test: `internal/pagewiki/llm_tree_indexer_test.go`

**Interfaces:**
- Consumes: exported helper `pagewiki.TreeIndexerPromptForTest(maxDepth int) string` and constant `pagewiki.TreeDefaultMaxDepthForTest` (already in `export_test.go`).
- Produces: new prompt sentence other tasks do not depend on.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/llm_tree_indexer_test.go`:

```go
func (s *llmTreeIndexerSuite) TestTreeIndexerPromptPinsShortTopicNames() {
	prompt := pagewiki.TreeIndexerPromptForTest(pagewiki.TreeDefaultMaxDepthForTest)
	s.Contains(prompt, "one to three words")
	s.Contains(prompt, "never a sentence")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run 'TestLLMTreeIndexerSuite/TestTreeIndexerPromptPinsShortTopicNames' -v`
Expected: FAIL — prompt does not contain "one to three words".

- [ ] **Step 3: Extend the prompt**

In `pageWikiTreeIndexerPromptTemplate` (`llm_tree_indexer.go`), insert after "Semantics are the only grouping principle. Group pages strictly by subject matter." and before "Never invent a catch-all topic…":

```
A topic title is a noun phrase of one to three words naming the subject
area, never a sentence.
```

The template is a `fmt.Sprintf` format string — the new text contains no `%` so no escaping is needed. Keep the rest byte-identical.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -run TreeIndexer -v`
Expected: PASS, including the existing max-depth and zero-directives prompt tests.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_tree_indexer.go internal/pagewiki/llm_tree_indexer_test.go
git commit -m "feat(pagewiki): tree indexer prompt pins short topic names"
```

---

### Task 5: Full verification

**Files:** none new.

- [ ] **Step 1: Run the full pagewiki package suite**

Run: `go test ./internal/pagewiki/...`
Expected: PASS (postgres integration tests may skip without a DB — skips are fine, failures are not).

- [ ] **Step 2: Build everything**

Run: `go build ./...`
Expected: clean exit.

- [ ] **Step 3: Lint the touched package**

Run: `golangci-lint run ./internal/pagewiki/...` (skip this step if `golangci-lint` is not installed locally; CI covers it).
Expected: no new issues in the touched files.

- [ ] **Step 4: Commit any stragglers**

Only if steps above required fixes:

```bash
git add -A internal/pagewiki
git commit -m "chore(pagewiki): fix lint/test fallout from concept-title changes"
```

---

## Acceptance (post-merge, manual)

Reset-and-rebuild replay on the workstation corpus; inspect that new page
titles are concept noun phrases and event-shaped pages no longer appear.
This is the user's standard replay workflow, not part of this plan's tasks.
