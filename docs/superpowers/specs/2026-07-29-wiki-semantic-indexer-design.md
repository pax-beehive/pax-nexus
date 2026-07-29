# Wiki semantic indexer design

Date: 2026-07-29
Status: approved by user (flat pages, LLM indexer with B-tree density rules,
per-ingest trigger, no migration — reset-rebuild instead)

## Problem

Two gaps against the LLM-wiki pattern (Karpathy) and the Open Knowledge
Format layering the current PageWiki extraction was measured against:

1. **The planner routes evidence against a thin catalog.** `PlanInput`
   carries only slug and title per page, so the update-vs-create decision
   lacks the one-line summary both references treat as the index an LLM
   reads first. Misrouting creates near-duplicate pages that the hard
   update-bias prompt alone cannot prevent.
2. **Navigation structure is baked into the write path.** The planner
   invents a `topic_path` per created page, blind to the existing topic
   tree, at the only moment it has no global view. Placement is permanent
   (updates never touch topics), synonym branches accumulate, and many
   topics hold a single page. Per the references, documents should be a
   flat collection; the tree-view index is a derived, rebuildable view.

## Design

### A. Planner catalog gains summaries

- `PageCatalogEntry` gains `Summary` (loaded from the page's current
  revision); `Repository.PageCatalog` implementations (postgres, memory)
  return it.
- `llmPlanPage` gains `summary` and the planner prompt directs the model
  to use the summaries when deciding update versus create.

### B. Write path goes flat

- `PageBrief.TopicPath` is removed: dropped from `llmPlanBrief`, from
  `acceptBrief` validation (a create needs only slug and title), from the
  planner prompt, and from the service (`buildPlacement` call and
  `PagePublication.Topics`/`Placement` on create are removed).
- Pages are published with no placement. Navigation for not-yet-indexed
  pages comes from the indexer run at the end of the same ingest run.

### C. Semantic indexer

A new LLM role (`llm_tree_indexer.go`, alongside planner and editor,
using the shared `platform/llm` chat client) rebuilds the whole tree-view
index from semantics:

- **Trigger**: at the end of every ingest run whose targets created a page
  or published a revision with a changed title or summary. Runs after
  publication, outside the per-target flow. Failure is non-fatal: log a
  warning and keep the previous tree.
- **Input** (JSON): the full page catalog (slug, title, summary) and the
  previous tree (topics with their page slugs), plus the density rules.
- **Output** (JSON): the complete new tree — topic nodes (title, children)
  and the page slugs placed under each node, plus root-level page slugs.
- **Stability rule** in the prompt: preserve the previous tree's names and
  placements unless a density rule is violated or a placement is clearly
  wrong; evolve, do not reinvent.

### D. B-tree density rules

- **Flat first**: pages sit directly at the root until the root overflows.
  A small wiki has no topics at all.
- **Split on overflow**: a node (including the root) with more than
  MAX = 10 direct pages is split by semantics into child topics of at
  least MIN = 3 pages each.
- **Merge on underflow**: a topic with fewer than MIN = 3 pages is
  collapsed into its parent.
- **Depth cap**: 2 levels of topics, as the UI supports today.

Deterministic validation after the LLM responds (MIN and coverage are
hard guarantees; MAX is advisory to the LLM):

- Every catalog page appears exactly once: missing pages are attached to
  the root; duplicate placements keep the first occurrence.
- Topics deeper than 2 levels are flattened into their level-2 ancestor.
- Underfull topics are collapsed into their parent by code.
- Overfull nodes are tolerated with a logged warning until the next run.
- Unknown page slugs in the response are dropped.

Topic IDs keep the existing stable derivation (`stableID` from parent and
slug), so unchanged names map to unchanged IDs across rebuilds.

### E. Persistence and read path

- `Repository` gains `ReplaceTopicTree(ctx, tree)` which atomically
  replaces all topics and placements (postgres: transactional delete and
  insert; memory: swap).
- `Navigation` gains root-level `Pages` alongside `Roots`, and it always
  includes every page without a placement — so pages published before the
  indexer runs, or left unplaced by a failed indexer run, stay reachable.
  The web `TopicTree` renders root-level pages as a flat list above or
  beside the topic groups.

### F. Deployment

No data migration. After merge and deploy, run the existing PageWiki
reset-and-rebuild; the tree grows from the new rules. The first indexer
run wholesale-replaces whatever tree exists.

## Non-goals

- No scheduled or manual indexer trigger (per-ingest only).
- No human curation UI for the tree.
- No OKF export bundle (future round).
- No lint/health worker (deliberately deferred; the wiki remains
  rebuildable from the Session Lake).
- No change to editor behavior or citation validation.

## Testing

- Planner: request-payload test asserting `summary` is present per catalog
  page; prompt assertion that the update decision references summaries;
  existing suite green with `topic_path` assertions removed.
- Indexer: unit tests for the deterministic validator (missing page →
  root, duplicate → first wins, depth flattening, underflow collapse,
  unknown slug dropped); request-payload test asserting catalog and
  previous tree are sent; degraded-path test (LLM failure keeps the old
  tree and the ingest run still succeeds).
- Service: acceptance test that an ingest run that publishes a page ends
  with a replaced tree, and that a run with no catalog change skips the
  indexer.
- Repository: contract test for `ReplaceTopicTree` atomic replacement in
  both adapters.
- Web: `TopicTree` renders root-level pages; wiki suite green.
