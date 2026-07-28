# Wiki Quality Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Longer, better-consolidated wiki articles with less noise (planner + editor prompt/context changes) and a collapsed-by-default Source evidence section in the reader.

**Architecture:** Three independent changes: (A) rewrite `pageWikiPlannerPrompt` with a durability test, the user's four noise categories, and a hard update-over-create rule; (B) give `LLMSessionEditor` the full content of evidence events (`evidence_context`, 8 KiB/event cap) and rewrite its prompt for complete articles; (C) `WikiMarkdown` renders the `source-evidence` section inside a collapsed `<details>`. No deterministic-layer or API changes.

**Tech Stack:** Go (`internal/pagewiki`, testify suites in package `pagewiki_test`), React/TypeScript (`web/`, vitest + testing-library).

**Spec:** `docs/superpowers/specs/2026-07-28-wiki-quality-2-design.md`

## Global Constraints

- Branch `feat/wiki-quality-2` (worktree exists). ALL work happens inside that worktree; verify `git branch --show-current` prints `feat/wiki-quality-2` before every commit.
- Backend gate: `go build ./... && go test ./...`. Frontend gate: `cd web && npm test && npm run build` (node_modules may need `npm ci` once).
- Deterministic layers are untouched: no changes to planner validation, citation building, `PageBrief`, or any non-prompt editor logic beyond adding `evidence_context`.
- The editor request cap is exactly 8 KiB per event: `editorEvidenceContextBytes = 8 << 10`.
- The UI section key that folds is exactly `source-evidence`.

---

### Task 1: Planner prompt — durability, noise categories, update rule

**Files:**
- Modify: `internal/pagewiki/llm_session_planner.go` (`pageWikiPlannerPrompt` const only)
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces:**
- Consumes: existing `llmSessionPlannerSuite`, `plannerRevision()`, `wikiChatClient` (in `llm_session_editor_test.go`, same package `pagewiki_test`).
- Produces: the system prompt contains the literal phrases `still need in a month`, `one-off session narratives`, and `MUST be update` — pinned by test so later edits cannot silently drop them.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/llm_session_planner_test.go`:

```go
func (s *llmSessionPlannerSuite) TestPlannerPromptPinsUpdateRuleAndNoisePolicy() {
	client := &wikiChatClient{responses: []string{`{"briefs":[]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	_, err = planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().NotEmpty(client.requests)
	system := client.requests[0].Messages[0].Content
	s.Contains(system, "still need in a month")
	s.Contains(system, "one-off session narratives")
	s.Contains(system, "MUST be update")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v 2>&1 | grep -E "PinsUpdate|FAIL|ok"`
Expected: FAIL — the current prompt contains none of the three phrases.

- [ ] **Step 3: Replace the prompt**

In `internal/pagewiki/llm_session_planner.go`, replace the entire `pageWikiPlannerPrompt` constant with:

```go
const pageWikiPlannerPrompt = `You are the maintenance planner of a durable, evidence-backed team Wiki.
You receive one JSON object: {"events":[{"id","content","truncated"}],"pages":[{"slug","title"}]}.
Return exactly one JSON object and no Markdown fence:
{"briefs":[{"action":"create|update|skip_noise","target_slug":"existing page slug, update only","proposed_slug":"kebab-case, create only","proposed_title":"English title, create only","reader_goal":"one English sentence","topic_path":["Area","Subarea"],"evidence":[{"event_id":"...","exact_quote":"verbatim substring of that event's content"}]}]}

Keep only knowledge a teammate would still need in a month: decisions and
their rationale, architecture, conventions, durable project state, and
domain facts. Treat as noise and mark skip_noise: one-off session
narratives about what an agent did or tried in a single session; transient
operational or verification records such as release checks, approvals, and
test-run logs; content unrelated to the team's work; single bug-fix details
that establish no lasting convention; code diffs and fragments; JSON or log
output; tool transcripts; agent system or skill instructions; branch names;
and timestamps. When in doubt, skip.

Updating an existing page is the rule, not a preference: when any page in
pages covers the same subject or a parent subject, or the new evidence
continues that subject's story, the action MUST be update with that page's
slug. Creating a page whose subject overlaps an existing page is an error.
Group related evidence aggressively into one page; most sessions should
yield zero to two briefs. Every exact_quote must be copied verbatim from
the event content and must genuinely support the page. topic_path has at
most two segments, for example ["Engineering","Runtime"]. Account for every
event with either a page brief or skip_noise. Return at most 8 briefs and
JSON only.`
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/pagewiki/`
Expected: PASS — every pre-existing planner/editor/acceptance test stays green (none pins removed prompt phrases; if one does fail on a prompt assertion, update only that assertion and say so in the report).

- [ ] **Step 5: Full gate and commit**

Run: `go build ./... && go test ./...`
Expected: PASS.

```bash
git add internal/pagewiki/llm_session_planner.go internal/pagewiki/llm_session_planner_test.go
git commit -m "feat(pagewiki): tighten planner durability, noise, and update rules"
```

---

### Task 2: Editor — full evidence context and article-length prompt

**Files:**
- Modify: `internal/pagewiki/llm_session_editor.go` (`llmEditRequest`, `Edit`, `pageWikiEnglishEditorPrompt`; new const `editorEvidenceContextBytes`)
- Test: `internal/pagewiki/llm_session_editor_test.go`

**Interfaces:**
- Consumes: `EditInput.SourceRevision` (`Raw []byte`, `Events []SourceEvent{ID, StartByte, EndByte}`), `Brief.EvidenceEventIDs` (already deduplicated by both planners).
- Produces: request JSON gains `"evidence_context":[...]` — full event bodies in `EvidenceEventIDs` order, each capped at 8192 bytes. The `evidence` field (exact quotes) is unchanged.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/llm_session_editor_test.go`:

```go
func (s *llmSessionEditorSuite) TestSendsFullEvidenceContextToTheModel() {
	client := &wikiChatClient{responses: []string{
		`{"title":"Release Policy","summary":"How the team ships.","sections":[{"key":"policy","heading":"Policy","markdown":"Ships weekly."}]}`,
	}}
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	first := "background before the quote. the decision: ship weekly. aftermath after the quote."
	oversized := strings.Repeat("x", 9000) + "TAIL-MARKER"
	raw := first + oversized
	_, err = editor.Edit(context.Background(), pagewiki.EditInput{
		SourceRevision: pagewiki.SourceRevision{
			ID:  "source-revision-1",
			Raw: []byte(raw),
			Events: []pagewiki.SourceEvent{
				{ID: "event-1", StartByte: 0, EndByte: len(first)},
				{ID: "event-2", StartByte: len(first), EndByte: len(raw)},
			},
		},
		Brief: pagewiki.PageBrief{
			Key: "release-policy", Action: pagewiki.PageActionCreate,
			ProposedSlug: "release-policy", ProposedTitle: "Release Policy",
			TopicPath:        []string{"Engineering"},
			EvidenceEventIDs: []string{"event-1", "event-2"},
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID: "event-1", ExactText: "the decision: ship weekly.",
			}},
		},
	})
	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	payload := client.requests[0].Messages[1].Content
	s.Contains(payload, "evidence_context")
	s.Contains(payload, "background before the quote")
	s.Contains(payload, "aftermath after the quote")
	s.NotContains(payload, "TAIL-MARKER")
}
```

Add `"strings"` to the test file imports if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionEditorSuite -v 2>&1 | grep -E "EvidenceContext|FAIL|ok"`
Expected: FAIL — the payload has no `evidence_context` and no text beyond the exact quote.

- [ ] **Step 3: Implement**

In `internal/pagewiki/llm_session_editor.go`:

1. Add the cap constant near the top of the file:

```go
const editorEvidenceContextBytes = 8 << 10
```

2. Add the field to `llmEditRequest` after `Evidence`:

```go
	EvidenceContext []string `json:"evidence_context,omitempty"`
```

3. In `Edit`, before building the payload, collect the context and include it:

```go
	contentByEvent := make(map[string]string, len(input.SourceRevision.Events))
	for _, event := range input.SourceRevision.Events {
		contentByEvent[event.ID] = string(input.SourceRevision.Raw[event.StartByte:event.EndByte])
	}
	evidenceContext := make([]string, 0, len(input.Brief.EvidenceEventIDs))
	for _, eventID := range input.Brief.EvidenceEventIDs {
		content, found := contentByEvent[eventID]
		if !found {
			continue
		}
		if len(content) > editorEvidenceContextBytes {
			content = content[:editorEvidenceContextBytes]
		}
		evidenceContext = append(evidenceContext, content)
	}
```

and set `EvidenceContext: evidenceContext` in the `llmEditRequest` literal.

4. Replace the entire `pageWikiEnglishEditorPrompt` constant with:

```go
const pageWikiEnglishEditorPrompt = `You are the senior editor of a durable, evidence-backed Wiki.
Return exactly one JSON object with this shape and no Markdown fence:
{"title":"English title","summary":"English summary","sections":[{"key":"stable-kebab-case","heading":"English heading","markdown":"English prose"}]}

Write every generated title, summary, heading, and prose sentence in English,
regardless of the language of the evidence. Preserve proper nouns accurately.
evidence lists the exact quotes that will be cited; evidence_context carries
the full source material they come from. Ground every claim in that
material: preserve attribution, negation, scope, uncertainty, and
chronology. Never invent a fact, decision, owner, status, date, outcome, or
relationship. Write a complete, self-contained article, not a Session
transcript or a list of chat turns: open with a substantive lead, then two
to six sections organized by reader meaning. Expand every point the
material supports — background, rationale, consequences, current state —
and stop where the evidence stops instead of padding. If current_text is
present, retain still-supported durable knowledge while updating it from
the supplied evidence. Do not include source quotations or a related-pages
section; the runtime appends exact evidence and audited Xanadu links
deterministically. Return JSON only.`
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/pagewiki/`
Expected: PASS — the pre-existing editor test asserting the system prompt contains "in English" still holds; acceptance suites unaffected.

- [ ] **Step 5: Full gate and commit**

Run: `go build ./... && go test ./...`
Expected: PASS.

```bash
git add internal/pagewiki/llm_session_editor.go internal/pagewiki/llm_session_editor_test.go
git commit -m "feat(pagewiki): feed the editor full evidence context for complete articles"
```

---

### Task 3: Reader UI — Source evidence collapsed by default

**Files:**
- Modify: `web/src/pages/WikiPage.tsx` (`WikiMarkdown` only)
- Modify: `web/src/styles.css`
- Test: `web/tests/wiki.dom.test.tsx`

**Interfaces:**
- Consumes: `revision.sections[].key` mapping built in `WikiMarkdown`'s `sectionKeys` memo; the folded key is exactly `source-evidence`.
- Produces: a `<details class="wiki-evidence-fold">` element (no `open` attribute by default) whose `<summary>` shows the section's heading; all other sections render unchanged.

- [ ] **Step 1: Write the failing test**

The wiki DOM fixtures in `web/tests/wiki.dom.test.tsx` build revisions via a helper (around line 20-60) whose sections currently include key `decision`. Extend the fixture revision with a `source-evidence` section (both in `sections` and appended to the `markdown` string, matching how the existing fixture keeps them consistent), then add a test:

```tsx
  it("collapses the Source evidence section by default", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path) => wikiFetch(path),
    });

    await screen.findByRole("heading", { name: "SQLite" });
    const fold = document.querySelector("details.wiki-evidence-fold");
    expect(fold).not.toBeNull();
    expect(fold?.hasAttribute("open")).toBe(false);
    expect(within(fold as HTMLElement).getByText("Source evidence")).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "Source evidence" })).toBeNull();
  });
```

Fixture change: add to the revision factory's `sections` array

```tsx
      {
        key: "source-evidence",
        heading: "Source evidence",
        markdown: "SQLite is searchable.",
      },
```

and append `\n\n## Source evidence\n\nSQLite is searchable.` to the factory's `markdown` template string. Note: jsdom does not natively toggle `<details>` on summary click, so the test pins collapsed-by-default rendering, not the toggle interaction — the browser provides the toggle for free.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run tests/wiki.dom.test.tsx`
Expected: the new test FAILS (`fold` is null — today the section renders as a plain h2 + paragraph). Pre-existing tests in the file may also fail if the fixture change altered assertions (e.g. a `queryByText("SQLite is searchable.")` absence assertion from the phase-1 UI change) — adjust ONLY assertions invalidated by the fixture addition and say so in the report.

- [ ] **Step 3: Implement**

In `web/src/pages/WikiPage.tsx`, rework `WikiMarkdown`'s block building so `source-evidence` content collects separately and renders inside a `<details>` at the position the section appeared:

```tsx
  const blocks: ReactNode[] = [];
  const evidenceBlocks: ReactNode[] = [];
  let evidenceHeading = "Source evidence";
  let evidenceAnchor = -1;
  let paragraph: string[] = [];
  let sectionKey = "";
  let blockKey = 0;
  const push = (node: ReactNode) => {
    if (sectionKey === "source-evidence") {
      if (evidenceAnchor < 0) evidenceAnchor = blocks.length;
      evidenceBlocks.push(node);
    } else {
      blocks.push(node);
    }
  };
  const flush = () => {
    if (paragraph.length === 0) return;
    const text = paragraph.join(" ");
    push(
      <p key={`p-${blockKey++}`} data-section={sectionKey || undefined}>
        {linkedText(text, sectionKey, relations, onSelect)}
      </p>,
    );
    paragraph = [];
  };
```

In the line loop, the `### ` branch calls `push(<h3 …>)` instead of `blocks.push`, and the `## ` branch becomes:

```tsx
      if (line.startsWith("## ")) {
        flush();
        const heading = line.slice(3);
        sectionKey = sectionKeys.get(heading) ?? "";
        if (sectionKey === "source-evidence") {
          evidenceHeading = heading;
          return;
        }
        blocks.push(
          <h2 key={`h2-${blockKey++}`} data-section={sectionKey || undefined}>
            {heading}
          </h2>,
        );
        return;
      }
```

After the final `flush()`:

```tsx
  if (evidenceBlocks.length > 0) {
    blocks.splice(
      evidenceAnchor < 0 ? blocks.length : evidenceAnchor,
      0,
      <details key="source-evidence-fold" className="wiki-evidence-fold">
        <summary>{evidenceHeading}</summary>
        {evidenceBlocks}
      </details>,
    );
  }
  return <div className="wiki-markdown">{blocks}</div>;
```

In `web/src/styles.css`, next to the other `.wiki-markdown` rules:

```css
.wiki-evidence-fold { margin-top: 28px; padding-top: 7px; border-top: 1px solid var(--border); }
.wiki-evidence-fold > summary { color: var(--muted); font-family: var(--sans); font-size: 12px; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; cursor: pointer; }
.wiki-evidence-fold[open] > summary { margin-bottom: 8px; }
```

- [ ] **Step 4: Run the web suite and build**

Run: `cd web && npm test && npm run build`
Expected: all PASS, build clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/WikiPage.tsx web/src/styles.css web/tests/wiki.dom.test.tsx
git commit -m "feat(web): collapse the wiki Source evidence section by default"
```

---

### Task 4: Manual deployment verification

Not a code task. After merge and deployment update on the workstation (`http://100.125.72.76:58080/`):

- [ ] **Step 1:** Run the PageWiki reset-and-rebuild from the wiki UI (shipped in PR #28) so legacy heading-chunk pages are wiped and sessions replay through the new prompts.
- [ ] **Step 2:** Verify in the navigation: no legacy fragment titles (`Findings`, `Scope`, `Workflow`); session-narrative and verification-record pages absent; overlapping subjects (single-team deployment) consolidated into single pages that carry revision history.
- [ ] **Step 3:** Open several pages: articles have a lead plus multiple substantive sections; the Source evidence block at the bottom is collapsed until clicked.

---

## Self-Review Notes

- Spec coverage: A → Task 1, B → Task 2, C → Task 3, D → Task 4. Non-goals need no tasks.
- Type consistency: `editorEvidenceContextBytes` defined and used only in Task 2; `wiki-evidence-fold` class matches between TSX and CSS and the DOM test; the three pinned prompt phrases in Task 1's test match the prompt text verbatim.
- Known judgment call: the DOM test pins collapsed-by-default only — jsdom does not implement native `<details>` toggling, and wiring a synthetic toggle would test the test, not the UI.
