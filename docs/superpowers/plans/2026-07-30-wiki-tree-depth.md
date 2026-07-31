# Configurable Wiki Tree Depth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the LLM wiki tree-depth limit configurable via `LLMWIKI_TREE_MAX_DEPTH` (default 5), replacing the hardcoded two-level structure in the tree indexer.

**Architecture:** `normalizeTree` in the LLM tree indexer becomes recursive with a depth cap (nodes at the cap flatten their subtree via the existing `collectNodePages`; the ≥3-pages pruning rule applies bottom-up at every level). The cap plumbs from env → `applicationConfig` → `LLMTreeIndexerConfig.MaxDepth`. The prompt's "at most two levels deep" becomes generated text. Spec: `docs/superpowers/specs/2026-07-30-wiki-tree-depth-design.md`.

**Tech Stack:** Go (testify suites, external test package `pagewiki_test`), docker compose env passthrough.

## Global Constraints

- Branch: `feat/wiki-tree-depth` (already created, stacked on `feat/wiki-standalone-page`; spec committed).
- Depth semantics: number of TOPIC levels; root topics are level 1. Default 5 (`treeDefaultMaxDepth`). `LLMWIKI_TREE_MAX_DEPTH` is read only in LLM organizer modes (`openai`/`harness`); set-but-invalid (non-integer or < 1) is a STARTUP ERROR, empty/unset falls back to the default.
- Behavior preservation at depth cap and below: duplicate topic slugs at the same level merge; a child node whose slug is empty or equals its parent's slug is absorbed into the parent (whole subtree of pages); a ROOT-level node with an empty slug is skipped WITHOUT claiming its pages (matches current code — the pages stay claimable by later topics); `treeMinTopicPages` (3) folds small topics upward; root-level folds count into the unplaced-budget warning; `treeMaxDirectPages` (10) warnings fire at every level; `claim` dedup and `topicSlug` normalization unchanged.
- Existing tests (e.g. `TestBuildsTwoLevelTreeWithStableIDs`, `tree_reindex_acceptance_test.go`) must keep passing unmodified — two-level responses behave identically under the new default.
- Pre-existing red on main (2 DB tests, 6 lint findings byte-identical at base) are not yours; no NEW failures.
- Commit after every task, messages ending with:

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>

---

### Task 1: recursive depth-capped tree indexer

**Files:**
- Modify: `internal/pagewiki/llm_tree_indexer.go`
- Test: `internal/pagewiki/llm_tree_indexer_test.go`

**Interfaces:**
- Consumes: existing `llmTreeNode`, `draftTopic`, `claim`/`topicSlug`/`collectNodePages`/`appendPlacements`/`stableID` helpers, `newIndexer` test helper.
- Produces: `LLMTreeIndexerConfig.MaxDepth int` (0 → `treeDefaultMaxDepth = 5`, negative → constructor error); Task 2 passes the parsed env value here.

- [ ] **Step 1: Write the failing tests**

Add to `llm_tree_indexer_test.go`. The suite's `newIndexer(s, responses...)` builds an indexer without MaxDepth (exercising the default); add a variant for explicit depth:

```go
func newDepthIndexer(
	s *llmTreeIndexerSuite, maxDepth int, responses ...string,
) (*pagewiki.LLMTreeIndexer, *wikiChatClient) {
	client := &wikiChatClient{responses: responses}
	indexer, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
		Client: client, Model: "test-model", MaxDepth: maxDepth,
	})
	s.Require().NoError(err)
	return indexer, client
}
```

Tests:

```go
// Three-level response survives intact under the default depth (5): each
// level gets its own topic with a parent-chained stable ID and placements.
func (s *llmTreeIndexerSuite) TestKeepsThreeLevelTreeUnderDefaultDepth() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00","page-01","page-02"],"children":[
			{"title":"Backend","pages":["page-03","page-04","page-05"],"children":[
				{"title":"Storage","pages":["page-06","page-07","page-08"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(9),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 3)
	engineering, backend, storage := tree.Topics[0], tree.Topics[1], tree.Topics[2]
	s.Equal("", engineering.ParentID)
	s.Equal(engineering.ID, backend.ParentID)
	s.Equal(backend.ID, storage.ParentID)
	s.Equal("storage", storage.Slug)
	s.Len(tree.Placements, 9)
	placementsByTopic := make(map[string]int)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID]++
	}
	s.Equal(3, placementsByTopic[storage.ID])
}

// Levels beyond MaxDepth flatten into the deepest kept topic.
func (s *llmTreeIndexerSuite) TestFlattensLevelsBeyondMaxDepth() {
	indexer, _ := newDepthIndexer(s, 2, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00","page-01","page-02"],"children":[
			{"title":"Backend","pages":["page-03","page-04","page-05"],"children":[
				{"title":"Storage","pages":["page-06","page-07","page-08"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(9),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 2)
	backend := tree.Topics[1]
	s.Equal("backend", backend.Slug)
	placementsByTopic := make(map[string]int)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID]++
	}
	// Backend keeps its own 3 pages plus Storage's 3 flattened pages.
	s.Equal(6, placementsByTopic[backend.ID])
}

// A deep topic below the minimum folds its pages into its parent, recursively.
func (s *llmTreeIndexerSuite) TestFoldsUndersizedDeepTopicIntoParent() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00","page-01","page-02"],"children":[
			{"title":"Backend","pages":["page-03","page-04","page-05"],"children":[
				{"title":"Storage","pages":["page-06"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(7),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 2)
	backend := tree.Topics[1]
	placementsByTopic := make(map[string]int)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID]++
	}
	s.Equal(4, placementsByTopic[backend.ID])
}

func (s *llmTreeIndexerSuite) TestRejectsNegativeMaxDepth() {
	_, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
		Client: &wikiChatClient{}, Model: "test-model", MaxDepth: -1,
	})
	s.Require().ErrorContains(err, "max depth")
}
```

Also add a prompt-wording test if the chat client records requests (check `wikiChatClient` — if it captures `llm.ChatRequest`, assert the system prompt of a depth-3 indexer contains `"at most 3 levels"`; if it doesn't capture requests, extend it minimally to record them).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pagewiki/ -run TestLLMTreeIndexerSuite -count=1`
Expected: compile error (`MaxDepth` unknown field), or the three-level test failing with 2 topics.

- [ ] **Step 3: Implement in `llm_tree_indexer.go`**

Constants and config:

```go
const (
	treeIndexerAttempts = 2
	treeMinTopicPages   = 3
	treeMaxDirectPages  = 10
	treeDefaultMaxDepth = 5
)

type LLMTreeIndexerConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
	// MaxDepth caps topic nesting (root topics are level 1); 0 means the
	// default. Deeper LLM output is flattened into the level-MaxDepth topic.
	MaxDepth int
}
```

Constructor: after the existing checks,

```go
	maxDepth := config.MaxDepth
	if maxDepth == 0 {
		maxDepth = treeDefaultMaxDepth
	}
	if maxDepth < 1 {
		return nil, errors.New("create Page Wiki tree indexer: max depth must be positive")
	}
	return &LLMTreeIndexer{
		client: config.Client, model: strings.TrimSpace(config.Model),
		logger: logger, maxDepth: maxDepth,
	}, nil
```

with `maxDepth int` added to the `LLMTreeIndexer` struct. In `Index`, the system message becomes `treeIndexerPrompt(x.maxDepth)`.

Replace the body of `normalizeTree` (keep `pageIDsBySlug`/`placed`/`claim` setup and the `decoded.RootPages` claim loop unchanged) with:

```go
	roots, absorbed := buildDraftTopics(decoded.Topics, claim, "", 1, x.maxDepth)
	kept, folded := pruneDraftTopics(roots)
	tree := TopicTree{Topics: make([]Topic, 0), Placements: make([]PagePlacement, 0)}
	unplacedBudget := len(absorbed) + len(folded)
	for _, page := range catalog {
		if _, found := placed[page.ID]; !found {
			unplacedBudget++
		}
	}
	x.emitTopics(&tree, "", kept)
	if unplacedBudget > treeMaxDirectPages {
		x.logger.Warn(
			"Page Wiki root exceeds the direct-page target",
			"pages", unplacedBudget,
		)
	}
	return tree
```

New helpers (replacing the inline two-level loops; `collectNodePages`, `appendPlacements`, `topicSlug`, `draftTopic` stay as they are):

```go
// buildDraftTopics converts LLM nodes at one level into draft topics,
// merging duplicate slugs and recursing until maxDepth. Nodes at maxDepth
// keep their whole subtree's pages. Returns the topics plus page IDs the
// caller must absorb (child nodes whose slug is empty or repeats the
// parent's). At the root level (empty parentSlug) an empty-slug node is
// skipped without claiming, preserving the previous behavior.
func buildDraftTopics(
	nodes []llmTreeNode,
	claim func(string) (string, bool),
	parentSlug string,
	depth, maxDepth int,
) ([]*draftTopic, []string) {
	topics := make([]*draftTopic, 0, len(nodes))
	index := make(map[string]*draftTopic)
	grouped := make(map[string][]llmTreeNode)
	absorbed := make([]string, 0)
	for _, node := range nodes {
		slug := topicSlug(node.Title)
		if slug == "" && parentSlug == "" {
			continue
		}
		if slug == "" || slug == parentSlug {
			for _, pageSlug := range collectNodePages(node) {
				if pageID, ok := claim(pageSlug); ok {
					absorbed = append(absorbed, pageID)
				}
			}
			continue
		}
		topic, found := index[slug]
		if !found {
			topic = &draftTopic{slug: slug, title: strings.TrimSpace(node.Title)}
			index[slug] = topic
			topics = append(topics, topic)
		}
		pages := node.Pages
		if depth == maxDepth {
			pages = collectNodePages(node)
		}
		for _, pageSlug := range pages {
			if pageID, ok := claim(pageSlug); ok {
				topic.pageIDs = append(topic.pageIDs, pageID)
			}
		}
		if depth < maxDepth {
			grouped[slug] = append(grouped[slug], node.Children...)
		}
	}
	for _, topic := range topics {
		children := grouped[topic.slug]
		if len(children) == 0 {
			continue
		}
		built, childAbsorbed := buildDraftTopics(children, claim, topic.slug, depth+1, maxDepth)
		topic.children = built
		topic.pageIDs = append(topic.pageIDs, childAbsorbed...)
	}
	return topics, absorbed
}

// pruneDraftTopics enforces the minimum-pages rule bottom-up: a topic whose
// subtree holds fewer than treeMinTopicPages pages folds its pages into its
// parent (or, at the root level, back into the unplaced budget).
func pruneDraftTopics(topics []*draftTopic) ([]*draftTopic, []string) {
	kept := make([]*draftTopic, 0, len(topics))
	folded := make([]string, 0)
	for _, topic := range topics {
		children, childFolded := pruneDraftTopics(topic.children)
		topic.children = children
		topic.pageIDs = append(topic.pageIDs, childFolded...)
		if len(subtreePageIDs(topic)) < treeMinTopicPages {
			folded = append(folded, subtreePageIDs(topic)...)
			continue
		}
		kept = append(kept, topic)
	}
	return kept, folded
}

func subtreePageIDs(topic *draftTopic) []string {
	ids := append([]string(nil), topic.pageIDs...)
	for _, child := range topic.children {
		ids = append(ids, subtreePageIDs(child)...)
	}
	return ids
}

func (x *LLMTreeIndexer) emitTopics(
	tree *TopicTree,
	parentID string,
	topics []*draftTopic,
) {
	for _, topic := range topics {
		id := stableID("topic", parentID, topic.slug)
		tree.Topics = append(tree.Topics, Topic{
			ID: id, ParentID: parentID, Slug: topic.slug, Title: topic.title,
		})
		appendPlacements(tree, id, topic.pageIDs)
		if len(topic.pageIDs) > treeMaxDirectPages {
			x.logger.Warn(
				"Page Wiki topic exceeds the direct-page target",
				"topic", topic.slug, "pages", len(topic.pageIDs),
			)
		}
		x.emitTopics(tree, id, topic.children)
	}
}
```

Prompt: rename the const to a template and add the generator (the prompt text contains no `%` characters, so `fmt.Sprintf` is safe — verify with a grep before relying on it):

```go
func treeIndexerPrompt(maxDepth int) string {
	return fmt.Sprintf(pageWikiTreeIndexerPromptTemplate, maxDepth)
}
```

In the template string, change only `at most two levels deep` → `at most %d levels of topics deep`.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/pagewiki/ -count=1`
Expected: PASS — including the untouched `TestBuildsTwoLevelTreeWithStableIDs` and `tree_reindex_acceptance_test.go` (behavior identical for two-level responses).

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_tree_indexer.go internal/pagewiki/llm_tree_indexer_test.go
git commit -m "feat: depth-capped recursive wiki tree indexer with configurable max depth"
```

---

### Task 2: env plumbing and compose passthrough

**Files:**
- Modify: `main.go` (applicationConfig + `buildPageWikiMaintainers`), `compose.yaml`

**Interfaces:**
- Consumes: `LLMTreeIndexerConfig.MaxDepth` from Task 1.
- Produces: `LLMWIKI_TREE_MAX_DEPTH` env var, parsed only in the `openai`/`harness` branch.

- [ ] **Step 1: main.go changes**

`applicationConfig` struct: add `llmwikiTreeMaxDepth string` next to the other llmwiki fields; in the config loader add `llmwikiTreeMaxDepth: os.Getenv("LLMWIKI_TREE_MAX_DEPTH"),`.

In `buildPageWikiMaintainers`, inside the `case "openai", "harness":` branch, before constructing the indexer:

```go
		maxDepth := 0
		if raw := strings.TrimSpace(config.llmwikiTreeMaxDepth); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				return nil, nil, nil, fmt.Errorf(
					"initialize Page Wiki LLM maintainers: LLMWIKI_TREE_MAX_DEPTH must be a positive integer, got %q",
					raw,
				)
			}
			maxDepth = parsed
		}
```

and pass `MaxDepth: maxDepth` in the `NewLLMTreeIndexer` config literal. Add `"strconv"` to imports if absent.

- [ ] **Step 2: compose passthrough**

In `compose.yaml`, `team-memory` service `environment` block (next to the `TEAM_MEMORY_EXTRACTOR_*` group), add:

```yaml
      LLMWIKI_ORGANIZER_MODE: ${LLMWIKI_ORGANIZER_MODE:-}
      LLMWIKI_LLM_BASE_URL: ${LLMWIKI_LLM_BASE_URL:-}
      LLMWIKI_LLM_API_KEY: ${LLMWIKI_LLM_API_KEY:-}
      LLMWIKI_LLM_MODEL: ${LLMWIKI_LLM_MODEL:-}
      LLMWIKI_TREE_MAX_DEPTH: ${LLMWIKI_TREE_MAX_DEPTH:-}
```

- [ ] **Step 3: Verify**

Run: `go build ./... && go vet ./... && docker compose -f compose.yaml config --quiet && make workstation-config-check`
Expected: all clean (`workstation-config-check` validates the overlay still composes; if that target needs node and fails on environment grounds unrelated to this change, report it rather than fixing).

- [ ] **Step 4: Commit**

```bash
git add main.go compose.yaml
git commit -m "feat: LLMWIKI_TREE_MAX_DEPTH env plumbing and compose passthrough"
```

---

### Task 3: repo verification

- [ ] **Step 1:** `go build ./... && go test ./internal/pagewiki/... ./internal/teamnote/... -count=1` — PASS expected (frontend untouched; full `make test-unit` optional given scope, run it if time permits and classify failures against the known pre-existing set).
- [ ] **Step 2:** `make lint` — no NEW findings vs the 6 pre-existing baseline findings.
- [ ] **Step 3:** `git status --short` clean; report `git log --oneline` for the branch.
