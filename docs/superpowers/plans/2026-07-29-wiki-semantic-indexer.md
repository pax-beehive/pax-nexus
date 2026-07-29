# Wiki Semantic Indexer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Flatten the PageWiki write path and add an LLM tree indexer that rebuilds the reader-facing topic tree from page semantics with B-tree density rules, plus give the planner catalog summaries.

**Architecture:** The planner/editor write path stops producing topic placements; pages publish flat. A new `LLMTreeIndexer` (third LLM role in `internal/pagewiki`, same chat client and model as planner/editor) runs at the end of every ingest run that changed the catalog, receives the full catalog (slug+title+summary) plus the previous tree, and returns a whole replacement tree that deterministic code normalizes (coverage, depth ≤ 2, underflow collapse) and atomically stores via a new `ReplaceTopicTree` repository operation. Navigation gains root-level pages so unplaced pages always stay reachable.

**Tech Stack:** Go (testify/suite), Postgres via pgx (JSON payload tables + in-memory hydration), Hertz thrift codegen (`make generate`), React + vitest.

**Spec:** `docs/superpowers/specs/2026-07-29-wiki-semantic-indexer-design.md`

## Global Constraints

- Density rules: MIN = 3 pages per topic (hard, enforced by code), MAX = 10 direct pages (advisory to LLM, warn only), depth ≤ 2 topic levels.
- Semantics only: the indexer prompt must forbid catch-all topics ("Misc", "Other", "General") and forbid grouping unrelated pages to satisfy a size rule; incoherent leftovers stay at the root.
- The indexer reuses the existing shared chat client and `config.llmwikiModel` — no new configuration keys.
- Indexer failure is non-fatal: log a warning, keep the previous tree, the ingest run still succeeds.
- All LLM prompts/outputs remain English; JSON-only responses with `trimJSONFence` tolerance.
- Repo conventions: table-driven testify suites in `package pagewiki_test`, errors wrapped with domain sentinel errors, no comments that narrate changes.
- Pre-existing red on `main` (3 lint findings + 2 DB tests) is out of scope — verify with the commands listed per task, not the full `make all`.
- Commit messages end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

### Task 1: Catalog entries carry summaries

**Files:**
- Modify: `internal/pagewiki/types.go:52-59` (PageCatalogEntry)
- Modify: `internal/pagewiki/memory/repository.go:82-93` (PageCatalog)
- Test: `internal/pagewiki/memory/repository_test.go`

**Interfaces:**
- Consumes: existing `Page`, `PageRevision` types.
- Produces: `PageCatalogEntry.Summary string` — Tasks 2 and 6 read it.

- [ ] **Step 1: Write the failing test**

In `internal/pagewiki/memory/repository_test.go`, add to the existing repository suite (follow its existing publish helpers — there are helpers building publications; reuse them):

```go
func (s *repositorySuite) TestPageCatalogCarriesCurrentSummary() {
	// Publish a page whose revision Summary is "Weekly release cadence."
	// using the suite's existing publication helper, then:
	catalog, err := s.repository.PageCatalog(context.Background())
	s.Require().NoError(err)
	s.Require().Len(catalog, 1)
	s.Equal("Weekly release cadence.", catalog[0].Summary)
}
```

Adapt the publish setup to whatever helper the suite already uses (grep for `PublishPage(` in that test file); the assertion above is the deliverable.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/memory/ -run TestRepository -v 2>&1 | head -30`
Expected: FAIL — `catalog[0].Summary` undefined (compile error).

- [ ] **Step 3: Implement**

`types.go` — add the field:

```go
type PageCatalogEntry struct {
	ID                string
	Slug              string
	Title             string
	CurrentRevisionID string
	Summary           string
}
```

`memory/repository.go` — the `pagewiki.PageCatalogEntry(page)` type conversion no longer compiles; replace the loop body:

```go
for _, page := range r.pages {
	catalog = append(catalog, pagewiki.PageCatalogEntry{
		ID:                page.ID,
		Slug:              page.Slug,
		Title:             page.Title,
		CurrentRevisionID: page.CurrentRevisionID,
		Summary:           r.pageRevisions[page.CurrentRevisionID].Summary,
	})
}
```

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/pagewiki/... 2>&1 | tail -20`
Expected: PASS (fix any other `PageCatalogEntry(` conversions the build surfaces the same way).

- [ ] **Step 5: Commit**

```bash
git add -A internal/pagewiki
git commit -m "feat(pagewiki): carry current revision summary in the page catalog"
```

---

### Task 2: Planner consumes catalog summaries

**Files:**
- Modify: `internal/pagewiki/llm_session_planner.go` (llmPlanPage, planRequest, pageWikiPlannerPrompt)
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces:**
- Consumes: `PageCatalogEntry.Summary` (Task 1).
- Produces: planner request JSON pages as `{"slug","title","summary"}`.

- [ ] **Step 1: Write the failing test**

```go
func (s *llmSessionPlannerSuite) TestPlanRequestCarriesCatalogSummaries() {
	client := &wikiChatClient{responses: []string{`{"briefs":[]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	_, err = planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
		PageCatalog: pagewiki.PageCatalog{{
			ID: "page-1", Slug: "existing-page", Title: "Existing Page",
			CurrentRevisionID: "revision-1",
			Summary:           "How weekly releases are cut.",
		}},
	})

	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[1].Content, "How weekly releases are cut.")
	s.Contains(client.requests[0].Messages[0].Content, "summary")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v 2>&1 | head -30`
Expected: FAIL — summary text absent from the payload.

- [ ] **Step 3: Implement**

```go
type llmPlanPage struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}
```

In `planRequest`, add `Summary: page.Summary` to the appended `llmPlanPage`.

In `pageWikiPlannerPrompt`, change the input-shape line to
`{"events":[{"id","content","truncated"}],"pages":[{"slug","title","summary"}]}`
and extend the update-bias paragraph with one sentence:
`Judge subject overlap with each page's summary, not its title alone.`

- [ ] **Step 4: Run tests**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_planner.go internal/pagewiki/llm_session_planner_test.go
git commit -m "feat(pagewiki): planner reads catalog summaries for update-vs-create"
```

---

### Task 3: Flat write path — remove TopicPath end to end

**Files:**
- Modify: `internal/pagewiki/types.go:183-195` (PageBrief)
- Modify: `internal/pagewiki/contracts.go:30-74` (ValidatePageBrief)
- Modify: `internal/pagewiki/llm_session_planner.go` (llmPlanBrief, acceptBrief, trimmedTopicPath, prompt)
- Modify: `internal/pagewiki/session_document.go` (unit topicPath, knowledgeTopic, sameTopicPath)
- Modify: `internal/pagewiki/service.go:171-176,220-239` (placement on create, buildPlacement)
- Modify: `internal/pagewiki/memory/repository.go:229-231` (new-page-requires-placement rule)
- Modify: `cmd/pagewiki-preview/generated_plan.go:471`, `cmd/pagewiki-preview/main.go:211` (brief construction)
- Test: existing suites in `internal/pagewiki/` and `internal/pagewiki/memory/`

**Interfaces:**
- Consumes: nothing new.
- Produces: `PageBrief` without `TopicPath`; `PagePublication.Topics`/`Placement` stay in the type (legacy hydration still replays them) but the service never sets them; memory accepts new pages with nil placement. Tasks 4-7 build on this flat state.

- [ ] **Step 1: Delete the field and let the compiler drive**

Remove `TopicPath []string` from `PageBrief` in `types.go`. Run `go build ./... 2>&1 | head -40` and fix every site the compiler names:

- `contracts.go`: delete the topic-path length/blank checks (lines 34-41) and the `create requires a topic path` branch (lines 50-52).
- `llm_session_planner.go`: drop `TopicPath` from `llmPlanBrief`; in `acceptBrief` delete the `topicPath` local, its emptiness check, and the `TopicPath:` field in the returned create brief; delete `trimmedTopicPath`. In `pageWikiPlannerPrompt` remove `"topic_path":["Area","Subarea"]` from the schema line and the sentence `topic_path has at most two segments, for example ["Engineering","Runtime"].`
- `session_document.go`: remove the `topicPath` field from the unit struct, every assignment to it (lines 22, 46, 204), the `sameTopicPath` function and its use in the merge condition (line 145), and the `knowledgeTopic` function if now unused. Keep `nonSlugCharacter` (line 14) — the planner and indexer use it.
- `service.go`: delete the `if brief.Action == PageActionCreate { publication.Topics, publication.Placement = buildPlacement(...) }` block (lines 171-176) and the `buildPlacement` function (lines 220-239).
- `memory/repository.go`: delete the `else if publication.Placement == nil { ... "requires placement" }` branch (lines 229-231).
- `cmd/pagewiki-preview`: remove the `brief.TopicPath = append(...)` line (generated_plan.go:471) and the `TopicPath: spec.topicPath,` field if it targets `pagewiki.PageBrief` (main.go:211). The preview's own JSON `topic_path` plumbing for display can stay.

- [ ] **Step 2: Fix the tests the change breaks**

Run: `go test ./internal/pagewiki/... ./cmd/... 2>&1 | head -50`

Update, do not delete, failing assertions: planner tests asserting `create.TopicPath`/`update.TopicPath` (drop those lines), contract tests for the removed validation branches, session-document tests grouping by topic, acceptance tests (`inject_acceptance_test.go`, `llm_plan_acceptance_test.go`, `multi_target_acceptance_test.go`, `update_acceptance_test.go`) that pass or assert topic paths and placements, memory repository tests asserting the requires-placement error, and `cmd/pagewiki-preview` tests. Where a test asserted "create produced a placement", flip it to assert the publication has no topics and no placement.

- [ ] **Step 3: Run the full package tests**

Run: `go build ./... && go test ./internal/pagewiki/... ./cmd/... 2>&1 | tail -15`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A internal/pagewiki cmd
git commit -m "refactor(pagewiki): publish pages flat — drop TopicPath from the write path"
```

---

### Task 4: TopicTree domain type and memory replacement

**Files:**
- Modify: `internal/pagewiki/types.go` (TopicTree)
- Modify: `internal/pagewiki/memory/repository.go` (TopicTree, ReplaceTopicTree)
- Test: `internal/pagewiki/memory/repository_test.go`

**Interfaces:**
- Consumes: existing `Topic`, `PagePlacement`.
- Produces:
  - `type TopicTree struct { Topics []Topic; Placements []PagePlacement }` in package `pagewiki`.
  - `func (r *Repository) TopicTree(ctx context.Context) (pagewiki.TopicTree, error)` — topics sorted by (ParentID, Slug), placements by PageID.
  - `func (r *Repository) ReplaceTopicTree(ctx context.Context, tree pagewiki.TopicTree) error` — validates then atomically swaps both maps.
  (Methods on the concrete memory Repository only; the `pagewiki.Repository` interface grows in Task 5.)

- [ ] **Step 1: Write the failing tests**

```go
func (s *repositorySuite) TestReplaceTopicTreeSwapsWholeTree() {
	// publish two pages (existing helper), IDs "page-1", "page-2"
	first := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-a", Slug: "runtime", Title: "Runtime"}},
		Placements: []pagewiki.PagePlacement{{PageID: "page-1", TopicID: "topic-a"}},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), first))

	second := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-b", Slug: "storage", Title: "Storage"}},
		Placements: []pagewiki.PagePlacement{{PageID: "page-2", TopicID: "topic-b"}},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), second))

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Equal(second, tree)
}

func (s *repositorySuite) TestReplaceTopicTreeRejectsInvalidTrees() {
	cases := []struct {
		name string
		tree pagewiki.TopicTree
	}{
		{"unknown page", pagewiki.TopicTree{
			Topics:     []pagewiki.Topic{{ID: "t", Slug: "s", Title: "S"}},
			Placements: []pagewiki.PagePlacement{{PageID: "ghost", TopicID: "t"}},
		}},
		{"unknown topic", pagewiki.TopicTree{
			Placements: []pagewiki.PagePlacement{{PageID: "page-1", TopicID: "ghost"}},
		}},
		{"missing parent", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "t", ParentID: "ghost", Slug: "s", Title: "S"}},
		}},
		{"three levels", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{
				{ID: "a", Slug: "a", Title: "A"},
				{ID: "b", ParentID: "a", Slug: "b", Title: "B"},
				{ID: "c", ParentID: "b", Slug: "c", Title: "C"},
			},
		}},
		{"duplicate placement", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "t", Slug: "s", Title: "S"}},
			Placements: []pagewiki.PagePlacement{
				{PageID: "page-1", TopicID: "t"},
				{PageID: "page-1", TopicID: "t"},
			},
		}},
	}
	for _, testCase := range cases {
		err := s.repository.ReplaceTopicTree(context.Background(), testCase.tree)
		s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict, testCase.name)
	}
	// prior valid state is untouched
	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Empty(tree.Topics)
}
```

(Publish "page-1"/"page-2" in setup with the suite's helper; adjust IDs to what the helper produces.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/pagewiki/memory/ -run TestRepository -v 2>&1 | head -20`
Expected: FAIL — `TopicTree` undefined.

- [ ] **Step 3: Implement**

`types.go`:

```go
type TopicTree struct {
	Topics     []Topic
	Placements []PagePlacement
}
```

`memory/repository.go`:

```go
func (r *Repository) TopicTree(_ context.Context) (pagewiki.TopicTree, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tree := pagewiki.TopicTree{
		Topics:     make([]pagewiki.Topic, 0, len(r.topics)),
		Placements: make([]pagewiki.PagePlacement, 0, len(r.placements)),
	}
	for _, topic := range r.topics {
		tree.Topics = append(tree.Topics, topic)
	}
	sort.Slice(tree.Topics, func(i, j int) bool {
		if tree.Topics[i].ParentID != tree.Topics[j].ParentID {
			return tree.Topics[i].ParentID < tree.Topics[j].ParentID
		}
		return tree.Topics[i].Slug < tree.Topics[j].Slug
	})
	for _, placement := range r.placements {
		tree.Placements = append(tree.Placements, placement)
	}
	sort.Slice(tree.Placements, func(i, j int) bool {
		return tree.Placements[i].PageID < tree.Placements[j].PageID
	})
	return tree, nil
}

func (r *Repository) ReplaceTopicTree(_ context.Context, tree pagewiki.TopicTree) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	topics := make(map[string]pagewiki.Topic, len(tree.Topics))
	for _, topic := range tree.Topics {
		if topic.ID == "" || topic.Slug == "" || topic.Title == "" {
			return fmt.Errorf("%w: invalid Topic", pagewiki.ErrRevisionConflict)
		}
		if _, duplicate := topics[topic.ID]; duplicate {
			return fmt.Errorf("%w: duplicate Topic %q", pagewiki.ErrRevisionConflict, topic.ID)
		}
		topics[topic.ID] = topic
	}
	for _, topic := range tree.Topics {
		if topic.ParentID != "" {
			if _, found := topics[topic.ParentID]; !found {
				return fmt.Errorf(
					"%w: parent Topic %q is missing",
					pagewiki.ErrRevisionConflict, topic.ParentID,
				)
			}
		}
		if topicDepth(topic.ID, topics) > 2 {
			return fmt.Errorf(
				"%w: Topic %q exceeds two levels",
				pagewiki.ErrRevisionConflict, topic.ID,
			)
		}
	}
	placements := make(map[string]pagewiki.PagePlacement, len(tree.Placements))
	for _, placement := range tree.Placements {
		if _, found := r.pages[placement.PageID]; !found {
			return fmt.Errorf(
				"%w: placed Page %q is missing",
				pagewiki.ErrRevisionConflict, placement.PageID,
			)
		}
		if _, found := topics[placement.TopicID]; !found {
			return fmt.Errorf(
				"%w: placement Topic %q is missing",
				pagewiki.ErrRevisionConflict, placement.TopicID,
			)
		}
		if _, duplicate := placements[placement.PageID]; duplicate {
			return fmt.Errorf(
				"%w: Page %q is placed twice",
				pagewiki.ErrRevisionConflict, placement.PageID,
			)
		}
		if placement.Rank < 0 {
			return fmt.Errorf("%w: invalid Page placement", pagewiki.ErrRevisionConflict)
		}
		placements[placement.PageID] = placement
	}
	r.topics = topics
	r.placements = placements
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/pagewiki/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/types.go internal/pagewiki/memory
git commit -m "feat(pagewiki): TopicTree type with atomic memory replacement"
```

---

### Task 5: Navigation gains root-level pages

**Files:**
- Modify: `internal/pagewiki/types.go:119-121` (Navigation)
- Modify: `internal/pagewiki/memory/repository.go:378-401` (Navigation)
- Test: `internal/pagewiki/memory/repository_test.go`

**Interfaces:**
- Consumes: placements state from Task 4.
- Produces: `Navigation.Pages []NavigationPage` — every page without a placement, sorted by slug, Rank 0. Task 9 maps it to the API.

- [ ] **Step 1: Write the failing test**

```go
func (s *repositorySuite) TestNavigationListsUnplacedPagesAtRoot() {
	// publish pages "page-1" (slug "alpha") and "page-2" (slug "beta");
	// place only page-2 under a topic via ReplaceTopicTree
	tree := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-a", Slug: "runtime", Title: "Runtime"}},
		Placements: []pagewiki.PagePlacement{{PageID: "page-2", TopicID: "topic-a"}},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), tree))

	navigation, err := s.repository.Navigation(context.Background())
	s.Require().NoError(err)
	s.Require().Len(navigation.Pages, 1)
	s.Equal("alpha", navigation.Pages[0].Slug)
	s.Require().Len(navigation.Roots, 1)
	s.Require().Len(navigation.Roots[0].Pages, 1)
	s.Equal("beta", navigation.Roots[0].Pages[0].Slug)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/pagewiki/memory/ -run TestRepository -v 2>&1 | head -20`
Expected: FAIL — `navigation.Pages` undefined.

- [ ] **Step 3: Implement**

`types.go`:

```go
type Navigation struct {
	Roots []NavigationTopic
	Pages []NavigationPage
}
```

`memory/repository.go` `Navigation()` — after building the `pages` map, collect the unplaced:

```go
rootPages := make([]pagewiki.NavigationPage, 0)
for id, page := range r.pages {
	if _, placed := r.placements[id]; placed {
		continue
	}
	rootPages = append(rootPages, pagewiki.NavigationPage{
		ID: page.ID, Slug: page.Slug, Title: page.Title,
	})
}
sort.Slice(rootPages, func(i, j int) bool {
	return rootPages[i].Slug < rootPages[j].Slug
})
return pagewiki.Navigation{
	Roots: buildNavigationTopics("", children, pages),
	Pages: rootPages,
}, nil
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/pagewiki/... 2>&1 | tail -10`
Expected: PASS (update any acceptance test that asserts a freshly created page appears under a topic — post-Task-3 it appears in `Navigation.Pages`).

- [ ] **Step 5: Commit**

```bash
git add -A internal/pagewiki
git commit -m "feat(pagewiki): navigation lists unplaced pages at the root"
```

---

### Task 6: Postgres persistence for the topic tree

**Files:**
- Create: `internal/platform/postgres/migrations/021_pagewiki_topic_trees.sql`
- Modify: `internal/pagewiki/ports.go:25-41` (Repository interface)
- Modify: `internal/pagewiki/postgres/repository.go` (delegates, hydrate, RebuildPageWiki)
- Test: `internal/pagewiki/postgres/repository_test.go`

**Interfaces:**
- Consumes: memory `TopicTree`/`ReplaceTopicTree` (Task 4).
- Produces: `pagewiki.Repository` interface now includes
  `TopicTree(context.Context) (TopicTree, error)` and
  `ReplaceTopicTree(context.Context, TopicTree) error`. Task 7's service calls these.

- [ ] **Step 1: Migration**

`021_pagewiki_topic_trees.sql`:

```sql
CREATE TABLE IF NOT EXISTS pagewiki_topic_trees (
    scope_id TEXT PRIMARY KEY,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Check how migrations are registered (embed directive or list near the other files in `internal/platform/postgres`); mirror whatever `020_pagewiki_session_consumer.sql` did to be picked up.

- [ ] **Step 2: Write the failing test**

In `internal/pagewiki/postgres/repository_test.go` (integration suite, follow its DB setup conventions; these run under `make integration-test` / a running `db-up` database):

```go
func (s *repositorySuite) TestTopicTreeSurvivesRehydration() {
	// publish one page via the suite's existing flow, then:
	tree := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-a", Slug: "runtime", Title: "Runtime"}},
		Placements: []pagewiki.PagePlacement{{PageID: pageID, TopicID: "topic-a"}},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), tree))

	reopened, err := postgres.NewRepository(context.Background(), s.pool, s.scopeID)
	s.Require().NoError(err)
	loaded, err := reopened.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Equal(tree, loaded)
}
```

Adapt pool/scope field names to the suite. Also extend the existing rebuild test (if present) to assert `TopicTree` is empty after `RebuildPageWiki`.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/pagewiki/postgres/ -run TestRepository -v 2>&1 | head -20` (with the dev database up via `make db-up`)
Expected: FAIL — methods undefined.

- [ ] **Step 4: Implement**

`ports.go` — add to `Repository`:

```go
TopicTree(context.Context) (TopicTree, error)
ReplaceTopicTree(context.Context, TopicTree) error
```

`postgres/repository.go`:

```go
func (r *Repository) TopicTree(ctx context.Context) (pagewiki.TopicTree, error) {
	return r.memory.TopicTree(ctx)
}

func (r *Repository) ReplaceTopicTree(ctx context.Context, tree pagewiki.TopicTree) error {
	if err := r.memory.ReplaceTopicTree(ctx, tree); err != nil {
		return err
	}
	payload, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("marshal Page Wiki topic tree: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO pagewiki_topic_trees (scope_id, payload)
VALUES ($1, $2)
ON CONFLICT (scope_id) DO UPDATE
SET payload = EXCLUDED.payload, updated_at = NOW()`, r.scopeID, payload); err != nil {
		return fmt.Errorf("persist Page Wiki topic tree: %w", err)
	}
	return nil
}
```

In `hydrate`, after the maintenance-runs block:

```go
if err := r.loadRows(ctx, `
SELECT payload FROM pagewiki_topic_trees
WHERE scope_id = $1`, func(payload []byte) error {
	var tree pagewiki.TopicTree
	if err := json.Unmarshal(payload, &tree); err != nil {
		return err
	}
	return r.memory.ReplaceTopicTree(ctx, tree)
}); err != nil {
	return fmt.Errorf("hydrate Page Wiki topic tree: %w", err)
}
```

In `RebuildPageWiki`, add `"DELETE FROM pagewiki_topic_trees WHERE scope_id = $1"` to the delete list.

- [ ] **Step 5: Run tests**

Run: `go build ./... && go test ./internal/pagewiki/... 2>&1 | tail -10` and the postgres suite from Step 3.
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A internal/pagewiki internal/platform/postgres
git commit -m "feat(pagewiki): persist the topic tree and replay it on hydration"
```

---

### Task 7: LLM tree indexer

**Files:**
- Create: `internal/pagewiki/llm_tree_indexer.go`
- Create: `internal/pagewiki/llm_tree_indexer_test.go`
- Modify: `internal/pagewiki/ports.go` (TreeIndexer port)

**Interfaces:**
- Consumes: `PageCatalog` with summaries (Task 1), `TopicTree` (Task 4), `llm.ChatClient`, package helpers `trimJSONFence`, `stableID`, `nonSlugCharacter`.
- Produces:
  - `type TreeIndexInput struct { Catalog PageCatalog; Current TopicTree }`
  - `type TreeIndexer interface { Index(context.Context, TreeIndexInput) (TopicTree, error) }`
  - `func NewLLMTreeIndexer(config LLMTreeIndexerConfig) (*LLMTreeIndexer, error)` with `LLMTreeIndexerConfig{Client llm.ChatClient; Model string; Logger *slog.Logger}`.
  Task 8 wires the service; Task 9's main.go constructs it.

- [ ] **Step 1: Write the failing tests**

`llm_tree_indexer_test.go` (reuse `wikiChatClient` from `llm_session_editor_test.go` — same package `pagewiki_test`):

```go
package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type llmTreeIndexerSuite struct {
	suite.Suite
}

func TestLLMTreeIndexerSuite(t *testing.T) {
	suite.Run(t, new(llmTreeIndexerSuite))
}

func indexerCatalog(size int) pagewiki.PageCatalog {
	catalog := make(pagewiki.PageCatalog, 0, size)
	for index := 0; index < size; index++ {
		slug := fmt.Sprintf("page-%02d", index)
		catalog = append(catalog, pagewiki.PageCatalogEntry{
			ID: "id-" + slug, Slug: slug, Title: "Page " + slug,
			CurrentRevisionID: "revision-" + slug, Summary: "Summary of " + slug,
		})
	}
	return catalog
}

func newIndexer(s *llmTreeIndexerSuite, responses ...string) (*pagewiki.LLMTreeIndexer, *wikiChatClient) {
	client := &wikiChatClient{responses: responses}
	indexer, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	return indexer, client
}

func (s *llmTreeIndexerSuite) TestBuildsTwoLevelTreeWithStableIDs() {
	indexer, client := newIndexer(s, `{"root_pages":["page-00"],"topics":[
		{"title":"Engineering","pages":["page-01","page-02","page-03"]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(4),
	})
	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 1)
	s.Equal("engineering", tree.Topics[0].Slug)
	s.Equal("", tree.Topics[0].ParentID)
	s.Require().Len(tree.Placements, 3)
	s.Equal("id-page-01", tree.Placements[0].PageID)
	s.Equal(tree.Topics[0].ID, tree.Placements[0].TopicID)
	s.Equal(0, tree.Placements[0].Rank)
	s.Equal(1, tree.Placements[1].Rank)
	// request carried summaries and the current tree shape
	s.Contains(client.requests[0].Messages[1].Content, "Summary of page-01")
	s.Contains(client.requests[0].Messages[0].Content, "Misc")
}

func (s *llmTreeIndexerSuite) TestCollapsesUnderfullTopicsAndCoversEveryPage() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-01","page-02","page-03"],
		 "children":[{"title":"Tiny","pages":["page-04"]}]},
		{"title":"Lonely","pages":["page-05"]},
		{"title":"Ghost","pages":["missing-slug","page-01"]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(7),
	})
	s.Require().NoError(err)
	// "Tiny" (1 page) collapsed into Engineering; "Lonely" (1 page) collapsed
	// to root; "Ghost" kept nothing: unknown slug dropped, page-01 already placed.
	s.Require().Len(tree.Topics, 1)
	s.Equal("engineering", tree.Topics[0].Slug)
	s.Require().Len(tree.Placements, 4) // page-01..04 under Engineering
	placed := make(map[string]struct{})
	for _, placement := range tree.Placements {
		placed[placement.PageID] = struct{}{}
	}
	s.Contains(placed, "id-page-04")
	s.NotContains(placed, "id-page-05") // at root: page-00, 05, 06
}

func (s *llmTreeIndexerSuite) TestFlattensThirdLevelIntoSecond() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00"],"children":[
			{"title":"Runtime","pages":["page-01","page-02"],"children":[
				{"title":"Deep","pages":["page-03"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(4),
	})
	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 2)
	byID := make(map[string]pagewiki.Topic)
	for _, topic := range tree.Topics {
		byID[topic.ID] = topic
	}
	for _, placement := range tree.Placements {
		s.Contains(byID, placement.TopicID)
	}
	// page-03 landed under Runtime (level 2), not a third level
	var runtimeID string
	for _, topic := range tree.Topics {
		if topic.Slug == "runtime" {
			runtimeID = topic.ID
		}
	}
	counted := 0
	for _, placement := range tree.Placements {
		if placement.TopicID == runtimeID {
			counted++
		}
	}
	s.Equal(3, counted)
}

func (s *llmTreeIndexerSuite) TestRetriesOnceThenFails() {
	indexer, client := newIndexer(s, "not json", "still not json")
	_, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(2),
	})
	s.Require().Error(err)
	s.Len(client.requests, 2)
}
```

Add `"fmt"` to imports. Also add a request-shape test asserting `current_topics` carries the previous tree: build `TreeIndexInput.Current` with one topic + placement and assert the user payload contains that topic title.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/pagewiki/ -run TestLLMTreeIndexerSuite -v 2>&1 | head -20`
Expected: FAIL — types undefined.

- [ ] **Step 3: Implement `llm_tree_indexer.go`**

```go
package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	treeIndexerAttempts = 2
	treeMinTopicPages   = 3
	treeMaxDirectPages  = 10
)

type LLMTreeIndexerConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
}

// LLMTreeIndexer organizes published pages into the reader-facing topic
// tree while deterministic code enforces coverage, depth, and density.
type LLMTreeIndexer struct {
	client llm.ChatClient
	model  string
	logger *slog.Logger
}

func NewLLMTreeIndexer(config LLMTreeIndexerConfig) (*LLMTreeIndexer, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki tree indexer: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki tree indexer: model is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &LLMTreeIndexer{
		client: config.Client, model: strings.TrimSpace(config.Model), logger: logger,
	}, nil
}

type llmTreePage struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

type llmTreeNode struct {
	Title    string        `json:"title"`
	Pages    []string      `json:"pages,omitempty"`
	Children []llmTreeNode `json:"children,omitempty"`
}

type llmTreeRequest struct {
	Pages         []llmTreePage `json:"pages"`
	CurrentRoot   []string      `json:"current_root_pages"`
	CurrentTopics []llmTreeNode `json:"current_topics"`
}

type llmTreeResponse struct {
	RootPages []string      `json:"root_pages"`
	Topics    []llmTreeNode `json:"topics"`
}

func (x *LLMTreeIndexer) Index(
	ctx context.Context,
	input TreeIndexInput,
) (TopicTree, error) {
	payload, err := json.Marshal(treeRequest(input))
	if err != nil {
		return TopicTree{}, fmt.Errorf("encode Page Wiki tree request: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < treeIndexerAttempts; attempt++ {
		response, err := x.client.Complete(ctx, llm.ChatRequest{
			Model: x.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: pageWikiTreeIndexerPrompt},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		var decoded llmTreeResponse
		if err := json.Unmarshal(
			[]byte(trimJSONFence(response.Message.Content)),
			&decoded,
		); err != nil {
			lastErr = err
			continue
		}
		return x.normalizeTree(decoded, input.Catalog), nil
	}
	return TopicTree{}, fmt.Errorf("index Page Wiki tree: %w", lastErr)
}

func treeRequest(input TreeIndexInput) llmTreeRequest {
	request := llmTreeRequest{
		Pages:         make([]llmTreePage, 0, len(input.Catalog)),
		CurrentRoot:   make([]string, 0),
		CurrentTopics: make([]llmTreeNode, 0),
	}
	for _, page := range input.Catalog {
		request.Pages = append(request.Pages, llmTreePage{
			Slug: page.Slug, Title: page.Title, Summary: page.Summary,
		})
	}
	slugsByID := make(map[string]string, len(input.Catalog))
	for _, page := range input.Catalog {
		slugsByID[page.ID] = page.Slug
	}
	placed := make(map[string]struct{}, len(input.Current.Placements))
	pagesByTopic := make(map[string][]string)
	for _, placement := range input.Current.Placements {
		slug, found := slugsByID[placement.PageID]
		if !found {
			continue
		}
		placed[placement.PageID] = struct{}{}
		pagesByTopic[placement.TopicID] = append(pagesByTopic[placement.TopicID], slug)
	}
	for _, page := range input.Catalog {
		if _, found := placed[page.ID]; !found {
			request.CurrentRoot = append(request.CurrentRoot, page.Slug)
		}
	}
	request.CurrentTopics = currentTreeNodes("", input.Current.Topics, pagesByTopic)
	return request
}

func currentTreeNodes(
	parentID string,
	topics []Topic,
	pagesByTopic map[string][]string,
) []llmTreeNode {
	nodes := make([]llmTreeNode, 0)
	for _, topic := range topics {
		if topic.ParentID != parentID {
			continue
		}
		nodes = append(nodes, llmTreeNode{
			Title:    topic.Title,
			Pages:    pagesByTopic[topic.ID],
			Children: currentTreeNodes(topic.ID, topics, pagesByTopic),
		})
	}
	return nodes
}

type draftTopic struct {
	slug     string
	title    string
	pageIDs  []string
	children []*draftTopic
}

func (x *LLMTreeIndexer) normalizeTree(
	decoded llmTreeResponse,
	catalog PageCatalog,
) TopicTree {
	pageIDsBySlug := make(map[string]string, len(catalog))
	for _, page := range catalog {
		pageIDsBySlug[page.Slug] = page.ID
	}
	placed := make(map[string]struct{}, len(catalog))
	claim := func(slug string) (string, bool) {
		pageID, known := pageIDsBySlug[slug]
		if !known {
			return "", false
		}
		if _, duplicate := placed[pageID]; duplicate {
			return "", false
		}
		placed[pageID] = struct{}{}
		return pageID, true
	}
	for _, slug := range decoded.RootPages {
		claim(slug)
	}
	roots := make([]*draftTopic, 0, len(decoded.Topics))
	rootIndex := make(map[string]*draftTopic)
	for _, node := range decoded.Topics {
		slug := topicSlug(node.Title)
		if slug == "" {
			continue
		}
		root, found := rootIndex[slug]
		if !found {
			root = &draftTopic{slug: slug, title: strings.TrimSpace(node.Title)}
			rootIndex[slug] = root
			roots = append(roots, root)
		}
		for _, pageSlug := range node.Pages {
			if pageID, ok := claim(pageSlug); ok {
				root.pageIDs = append(root.pageIDs, pageID)
			}
		}
		childIndex := make(map[string]*draftTopic)
		for _, existing := range root.children {
			childIndex[existing.slug] = existing
		}
		for _, childNode := range node.Children {
			childSlug := topicSlug(childNode.Title)
			if childSlug == "" || childSlug == slug {
				for _, pageSlug := range collectNodePages(childNode) {
					if pageID, ok := claim(pageSlug); ok {
						root.pageIDs = append(root.pageIDs, pageID)
					}
				}
				continue
			}
			child, exists := childIndex[childSlug]
			if !exists {
				child = &draftTopic{slug: childSlug, title: strings.TrimSpace(childNode.Title)}
				childIndex[childSlug] = child
				root.children = append(root.children, child)
			}
			for _, pageSlug := range collectNodePages(childNode) {
				if pageID, ok := claim(pageSlug); ok {
					child.pageIDs = append(child.pageIDs, pageID)
				}
			}
		}
	}
	tree := TopicTree{Topics: make([]Topic, 0), Placements: make([]PagePlacement, 0)}
	unplacedBudget := 0
	for _, page := range catalog {
		if _, found := placed[page.ID]; !found {
			unplacedBudget++
		}
	}
	for _, root := range roots {
		kept := root.children[:0]
		for _, child := range root.children {
			if len(child.pageIDs) < treeMinTopicPages {
				root.pageIDs = append(root.pageIDs, child.pageIDs...)
				continue
			}
			kept = append(kept, child)
		}
		root.children = kept
		total := len(root.pageIDs)
		for _, child := range root.children {
			total += len(child.pageIDs)
		}
		if total < treeMinTopicPages {
			unplacedBudget += total
			continue
		}
		rootID := stableID("topic", "", root.slug)
		tree.Topics = append(tree.Topics, Topic{
			ID: rootID, Slug: root.slug, Title: root.title,
		})
		appendPlacements(&tree, rootID, root.pageIDs)
		if len(root.pageIDs) > treeMaxDirectPages {
			x.logger.Warn(
				"Page Wiki topic exceeds the direct-page target",
				"topic", root.slug, "pages", len(root.pageIDs),
			)
		}
		for _, child := range root.children {
			childID := stableID("topic", rootID, child.slug)
			tree.Topics = append(tree.Topics, Topic{
				ID: childID, ParentID: rootID, Slug: child.slug, Title: child.title,
			})
			appendPlacements(&tree, childID, child.pageIDs)
			if len(child.pageIDs) > treeMaxDirectPages {
				x.logger.Warn(
					"Page Wiki topic exceeds the direct-page target",
					"topic", child.slug, "pages", len(child.pageIDs),
				)
			}
		}
	}
	if unplacedBudget > treeMaxDirectPages {
		x.logger.Warn(
			"Page Wiki root exceeds the direct-page target",
			"pages", unplacedBudget,
		)
	}
	return tree
}

func appendPlacements(tree *TopicTree, topicID string, pageIDs []string) {
	for rank, pageID := range pageIDs {
		tree.Placements = append(tree.Placements, PagePlacement{
			PageID: pageID, TopicID: topicID, Rank: rank,
		})
	}
}

func collectNodePages(node llmTreeNode) []string {
	slugs := append([]string(nil), node.Pages...)
	for _, child := range node.Children {
		slugs = append(slugs, collectNodePages(child)...)
	}
	return slugs
}

func topicSlug(title string) string {
	return strings.Trim(nonSlugCharacter.ReplaceAllString(
		strings.ToLower(strings.TrimSpace(title)), "-",
	), "-")
}

const pageWikiTreeIndexerPrompt = `You are the librarian of a durable, evidence-backed team Wiki.
You organize finished pages into a reader-facing topic tree; you never rewrite pages.
You receive one JSON object: {"pages":[{"slug","title","summary"}],"current_root_pages":[...],"current_topics":[{"title","pages","children"}]}.
current_root_pages and current_topics describe the tree as it stands today.
Return exactly one JSON object and no Markdown fence:
{"root_pages":["slug"],"topics":[{"title":"English topic name","pages":["slug"],"children":[{"title":"...","pages":["slug"]}]}]}

Semantics are the only grouping principle. Group pages strictly by subject
matter. Never invent a catch-all topic such as "Misc", "Other", or
"General", and never group unrelated pages to satisfy a size rule; when no
coherent group exists, leave those pages in root_pages. Flat first: a
small wiki needs no topics at all. Introduce a topic only when more than
10 pages would otherwise sit together and a coherent group of at least 3
pages exists. Split a topic holding more than 10 direct pages into child
topics of at least 3 pages each, at most two levels deep. Preserve the
current tree's topic names and placements unless a rule above is violated
or a placement is clearly wrong: evolve the tree, do not reinvent it.
Every page slug must appear exactly once, in root_pages or under exactly
one topic. Return JSON only.`

var _ TreeIndexer = (*LLMTreeIndexer)(nil)
```

`ports.go` — add:

```go
type TreeIndexInput struct {
	Catalog PageCatalog
	Current TopicTree
}

type TreeIndexer interface {
	Index(context.Context, TreeIndexInput) (TopicTree, error)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/pagewiki/ -run TestLLMTreeIndexerSuite -v 2>&1 | tail -15`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_tree_indexer.go internal/pagewiki/llm_tree_indexer_test.go internal/pagewiki/ports.go
git commit -m "feat(pagewiki): LLM tree indexer with B-tree density normalization"
```

---

### Task 8: Service triggers the indexer per ingest run

**Files:**
- Modify: `internal/pagewiki/service.go` (Service struct, NewService, InjectSession tail)
- Test: new `internal/pagewiki/tree_reindex_acceptance_test.go`

**Interfaces:**
- Consumes: `TreeIndexer` (Task 7), `Repository.TopicTree`/`ReplaceTopicTree` (Task 6).
- Produces: `NewService(repository, planner, editor, options ...ServiceOption)` and `WithTreeIndexer(indexer TreeIndexer, logger *slog.Logger) ServiceOption`. Existing `NewService(r, p, e)` call sites keep compiling unchanged. Task 9 wires main.go.

- [ ] **Step 1: Write the failing acceptance test**

`tree_reindex_acceptance_test.go` in `package pagewiki_test`, using the memory repository and the scripted planner/editor the other acceptance tests use (copy their setup pattern):

```go
type recordingIndexer struct {
	calls int
	tree  pagewiki.TopicTree
	err   error
}

func (r *recordingIndexer) Index(
	_ context.Context,
	_ pagewiki.TreeIndexInput,
) (pagewiki.TopicTree, error) {
	r.calls++
	return r.tree, r.err
}

func (s *treeReindexSuite) TestSuccessfulRunReplacesTree() {
	// service built with pagewiki.WithTreeIndexer(indexer, nil); scripted
	// planner returns one create brief. indexer.tree places the new page
	// under one topic (IDs computed from the published page).
	// After InjectSession: indexer.calls == 1 and repository.TopicTree
	// equals indexer.tree.
}

func (s *treeReindexSuite) TestSourceOnlyRunSkipsIndexer() {
	// scripted planner returns only a source-only brief.
	// After InjectSession: indexer.calls == 0.
}

func (s *treeReindexSuite) TestIndexerFailureKeepsRunAndOldTree() {
	// Seed a valid tree via ReplaceTopicTree, then indexer.err = errors.New("boom").
	// InjectSession still returns a succeeded run; repository.TopicTree
	// still returns the seeded tree.
}

func (s *treeReindexSuite) TestServiceWithoutIndexerStillWorks() {
	// NewService(repository, planner, editor) — no option — injects fine.
}
```

Flesh these out against the real scripted planner/editor helpers in `llm_plan_acceptance_test.go` / `inject_acceptance_test.go` (they exist — mirror their request/brief construction; the indexer's returned tree can be built after a first inject to learn the page ID, or use a fake planner that proposes a known slug so `stableID("page", ...)` is predictable — copy how existing tests predict page IDs).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/pagewiki/ -run TestTreeReindex -v 2>&1 | head -20`
Expected: FAIL — `WithTreeIndexer` undefined.

- [ ] **Step 3: Implement**

`service.go`:

```go
type Service struct {
	repository  Repository
	planner     Planner
	editor      Editor
	treeIndexer TreeIndexer
	logger      *slog.Logger
}

type ServiceOption func(*Service)

func WithTreeIndexer(indexer TreeIndexer, logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		s.treeIndexer = indexer
		if logger != nil {
			s.logger = logger
		}
	}
}

func NewService(
	repository Repository,
	planner Planner,
	editor Editor,
	options ...ServiceOption,
) *Service {
	service := &Service{
		repository: repository,
		planner:    planner,
		editor:     editor,
		logger:     observability.DiscardLogger(),
	}
	for _, option := range options {
		option(service)
	}
	return service
}
```

(Imports gain `log/slog` and `internal/platform/observability`.)

At the end of `InjectSession`, between `SaveMaintenanceRun` and the return:

```go
s.maybeReindexTree(ctx, briefs, run.Targets)
```

And:

```go
func (s *Service) maybeReindexTree(
	ctx context.Context,
	briefs []PageBrief,
	targets []MaintenanceTarget,
) {
	if s.treeIndexer == nil || !catalogChanged(briefs, targets) {
		return
	}
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "load catalog", "error", err)
		return
	}
	current, err := s.repository.TopicTree(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "load tree", "error", err)
		return
	}
	tree, err := s.treeIndexer.Index(ctx, TreeIndexInput{Catalog: catalog, Current: current})
	if err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "index", "error", err)
		return
	}
	if err := s.repository.ReplaceTopicTree(ctx, tree); err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "replace", "error", err)
	}
}

func catalogChanged(briefs []PageBrief, targets []MaintenanceTarget) bool {
	for index, target := range targets {
		if index >= len(briefs) || target.Status != TargetStatusSucceeded {
			continue
		}
		switch briefs[index].Action {
		case PageActionCreate:
			return true
		case PageActionUpdate:
			if target.PageRevisionID != briefs[index].ExpectedBaseRevisionID {
				return true
			}
		}
	}
	return false
}
```

(`run.Targets` is appended in brief order in `InjectSession`, so index pairing is sound. The equivalence-skip path keeps `PageRevisionID == ExpectedBaseRevisionID`, which correctly reads as "unchanged".)

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/pagewiki/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A internal/pagewiki
git commit -m "feat(pagewiki): reindex the topic tree after catalog-changing runs"
```

---

### Task 9: Wire the indexer in main.go

**Files:**
- Modify: `main.go` (buildPageWikiMaintainers, service construction ~line 181)
- Test: `main_test.go` (only if it pins the maintainer builder signature)

**Interfaces:**
- Consumes: `NewLLMTreeIndexer`, `WithTreeIndexer` (Tasks 7-8).
- Produces: running binary indexes trees in `openai`/`harness` mode; `local` mode has no indexer (flat root list).

- [ ] **Step 1: Implement**

Change `buildPageWikiMaintainers` to return `(pagewiki.Planner, pagewiki.Editor, pagewiki.TreeIndexer, error)`:

- `local` branch: `return pagewiki.SessionDocumentPlanner{}, pagewiki.SessionDocumentEditor{}, nil, nil`
- LLM branch, after the editor:

```go
indexer, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
	Client: client, Model: config.llmwikiModel, Logger: logger,
})
if err != nil {
	return nil, nil, nil, err
}
return planner, editor, indexer, nil
```

At the call site (~line 181):

```go
planner, editor, indexer, err := buildPageWikiMaintainers(config, logger)
if err != nil {
	return nil, nil, err
}
options := make([]pagewiki.ServiceOption, 0, 1)
if indexer != nil {
	options = append(options, pagewiki.WithTreeIndexer(indexer, logger))
}
service := pagewiki.NewService(repository, planner, editor, options...)
```

(Match the surrounding error-return shape — the enclosing function returns `(handler, controller, error)`; keep whatever it does today.)

- [ ] **Step 2: Verify**

Run: `go build ./... && go test . 2>&1 | tail -10`
Expected: build and root-package tests PASS.

- [ ] **Step 3: Commit**

```bash
git add main.go main_test.go
git commit -m "feat(pagewiki): wire the tree indexer with the shared llmwiki client"
```

---

### Task 10: Navigation API exposes root pages

**Files:**
- Modify: `idl/page_wiki.thrift:193-195` (NavigationResponse)
- Regenerate: `internal/pagewiki/transport/httpapi/model/...` via `make generate`
- Modify: `internal/pagewiki/transport/httpapi/mapping.go:238`
- Test: `internal/pagewiki/transport/httpapi/contract_acceptance_test.go`

**Interfaces:**
- Consumes: `Navigation.Pages` (Task 5).
- Produces: `GET /v1/wiki/navigation` responds `{"roots":[...],"pages":[...]}`. Task 11's web client reads `pages`.

- [ ] **Step 1: Write the failing contract assertion**

In `contract_acceptance_test.go`, find the navigation contract test and extend it: publish one placed and one unplaced page (or reuse its fixtures — post-Task-3 every published page is unplaced until a tree is replaced), call the endpoint, and assert the JSON body has a `pages` array listing the unplaced page's slug.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/pagewiki/transport/... -run TestContract -v 2>&1 | head -20`
Expected: FAIL — no `pages` field.

- [ ] **Step 3: Implement**

`idl/page_wiki.thrift`:

```thrift
struct NavigationResponse {
  1: required list<NavigationTopic> roots
  2: required list<NavigationPage> pages
}
```

Run `make generate`. Then in `mapping.go`, where `NavigationResponse` is built (line 238), map the root pages exactly the way topic pages are mapped a few lines below (same `api.NavigationPage` construction loop — extract a helper if one doesn't exist):

```go
return &api.NavigationResponse{
	Roots: navigationTopicsToAPI(navigation.Roots),
	Pages: navigationPagesToAPI(navigation.Pages),
}
```

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/pagewiki/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add idl internal/pagewiki/transport
git commit -m "feat(pagewiki): expose root-level pages in the navigation API"
```

---

### Task 11: Web renders root pages in the wiki rail

**Files:**
- Modify: `web/src/api/wiki.ts:25-27` (WikiNavigation)
- Modify: `web/src/components/wiki/TopicTree.tsx` (RootPageList)
- Modify: `web/src/pages/WikiPage.tsx` (state, collectPages composition, render)
- Test: the existing wiki web test file (grep `web/src` for the wiki test suite; add there)

**Interfaces:**
- Consumes: navigation JSON with `pages` (Task 10).
- Produces: unplaced pages render as a flat list above the topic groups and count toward page selection/empty-state logic.

- [ ] **Step 1: Write the failing test**

In the existing wiki component/page test file (follow its render helpers), add:

```tsx
it("renders root-level pages above topic groups", () => {
  // render the rail with navigation { pages: [{id:"p1",slug:"alpha",title:"Alpha",rank:0}],
  //   roots: [one topic with one page] }
  // assert a button "Alpha" exists and appears before the topic heading
});
```

Run: `cd web && npm test 2>&1 | tail -15` — expect FAIL.

- [ ] **Step 2: Implement**

`api/wiki.ts`:

```ts
export interface WikiNavigation {
  roots: WikiNavigationTopic[];
  pages: WikiNavigationPage[];
}
```

`TopicTree.tsx` — export a root list using the same button markup as `Topic`:

```tsx
export function RootPageList({
  pages,
  selectedSlug,
  onSelect,
}: {
  pages: WikiNavigationPage[];
  selectedSlug: string;
  onSelect: (slug: string) => void;
}) {
  if (pages.length === 0) return null;
  return (
    <section className="wiki-topic">
      {pages.map((page) => (
        <button
          key={page.id}
          type="button"
          className={page.slug === selectedSlug ? "wiki-page-link active" : "wiki-page-link"}
          aria-current={page.slug === selectedSlug ? "page" : undefined}
          onClick={() => onSelect(page.slug)}
        >
          {page.title}
        </button>
      ))}
    </section>
  );
}
```

`WikiPage.tsx`: add `const [rootPages, setRootPages] = useState<WikiNavigationPage[]>([]);`; in the navigation load, `setRootPages(navigation.pages ?? [])` and include them first in the flattened list used for selection/empty-state: `const pages = [...rootPages, ...collectPages(topics)];` (both places it is computed). Render `<RootPageList pages={rootPages} ... />` immediately before the topic `map`.

- [ ] **Step 3: Run tests**

Run: `cd web && npm test 2>&1 | tail -10` and `cd web && npm run build 2>&1 | tail -5`
Expected: PASS, clean tsc.

- [ ] **Step 4: Commit**

```bash
git add web/src
git commit -m "feat(web): render unplaced wiki pages at the root of the rail"
```

---

## Final verification

- [ ] `go build ./... && go test ./internal/pagewiki/... ./cmd/... . 2>&1 | tail -15` — all PASS.
- [ ] `cd web && npm test && npm run build` — PASS.
- [ ] `make db-up && go test ./internal/pagewiki/postgres/ -v 2>&1 | tail -10` — PASS (pre-existing failures elsewhere in the repo's DB suites are out of scope).
- [ ] Grep check: `grep -rn "TopicPath" internal/pagewiki cmd | grep -v _test` returns nothing (preview's own display-side `topic_path` JSON may remain).
