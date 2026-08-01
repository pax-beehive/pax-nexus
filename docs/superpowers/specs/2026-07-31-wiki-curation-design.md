# PageWiki Curation design

Date: 2026-07-31
Status: approved by user (scope, execution mode, trigger, action semantics)

## Problem

The ingest pipeline only ever grows the wiki. The planner's action space is
`create | update | source-only | ambiguous`: there is no delete, merge, or
page-level reorganization, and the editor sees exactly one page per brief. As
a result:

- near-duplicate pages accumulate when different sessions touch the same
  topic under slightly different framing;
- garbage and low-value pages (including the pre-LLM chunker backlog) stay in
  the catalog forever;
- two pages can carry contradictory claims about the same fact with nothing
  to notice or resolve it;
- pages written under older prompt regimes never get re-edited to current
  quality rules unless new session evidence happens to target them.

## Goal

A fourth LLM role — the **Curator** — periodically reviews the catalog and
performs global maintenance: merge near-duplicates, retire low-value pages,
resolve cross-page contradictions, and rewrite substandard pages. Fully
automatic, no human approval gate, but **conservative by mechanism**: every
destructive verdict must survive an independent adversarial verify call, all
uncertainty degrades to "do nothing", and nothing is ever physically deleted.

The design keeps the established PageWiki split: the LLM owns judgment,
deterministic code owns authority (candidate selection, verify policy,
citation carry-forward, CAS publication, audit).

Non-goals: no human review UI, no agentic filesystem/tool-loop curator, no
changes to the ingest planner/editor contracts, no physical deletion of pages
or revisions, no backfill migration (the quality lane handles the garbage
backlog incrementally over rounds).

## Architecture and trigger

- New port next to Planner/Editor/TreeIndexer:

  ```go
  type Curator interface {
      JudgePair(context.Context, PairJudgeInput) (PairVerdict, error)
      JudgePage(context.Context, PageJudgeInput) (PageVerdict, error)
  }
  ```

  Injected via `WithCurator(...)` service option; without it the feature does
  not exist (same pattern as `WithTreeIndexer`).

- `StartCurationMaintenance(ctx)` runs a background loop modeled on
  `StartTreeMaintenance`: one curation round per fixed interval
  (`LLMWIKI_CURATION_INTERVAL`, default `24h`, `0` disables). A synchronous
  `RunCurationRound(ctx)` is exported for tests and manual triggering.

- **Round idempotency.** The curation run ID is
  `stableID("curation-run", <catalog fingerprint>)`, where the fingerprint
  hashes the sorted `(pageID, currentRevisionID)` list of the active catalog.
  A round whose run ID already exists is skipped entirely. Consequences:
  - at most one curation round per catalog state — `keep` verdicts are not
    re-litigated (and not re-billed) until something actually changes;
  - no rewrite→re-judge oscillation: a round that changes pages produces a
    new fingerprint, but a round that keeps everything produces no new state.

## Candidate detection (deterministic, zero tokens)

Each round selects a bounded candidate set from three lanes:

1. **Duplicate-pair lane.** Embed `title + "\n" + summary` of every active
   page with the existing embedding service, cached in `page_embeddings`
   keyed by `(page_id, revision_id)` so only changed pages re-embed. Candidate
   pairs are cosine similarity above a code-constant threshold, plus pairs
   sharing a topic-tree leaf with near-identical titles. Top
   `LLMWIKI_CURATION_PAIR_LIMIT` (default 8) pairs per round, highest
   similarity first.
2. **Quality lane.** Heuristic score over active pages: orphan (no inbound
   and no outbound links), body length below a floor, title failing the
   concept-shape rules (reusing the PR #53 title validation as a detector).
   Top `LLMWIKI_CURATION_PAGE_LIMIT` (default 8) pages per round.
3. **Contradiction lane.** Not detected separately: contradictions surface
   inside near-duplicate pairs and are routed by the pair verdict (`conflict`).

Per-round LLM calls are therefore hard-capped at
`(pairs + pages) × 2` including verify calls.

## Data model

1. `Page` gains lifecycle fields: `Status` (`active` | `retired`, default
   active), `SuccessorPageID` (nullable), `RetiredAt`, `RetiredByRunID`.
   - Repository adds `RetirePage(ctx, RetireRequest)` with CAS: it succeeds
     only while the page's `CurrentRevisionID` still equals the revision the
     verdict was made against.
   - `PageCatalog`, `Navigation`, `Search`, and the tree indexer input
     exclude retired pages. `PageByID`/`PageBySlug` still return them with
     successor info; the UI renders an "archived, see X" redirect notice.
   - Revisions are never deleted. Rollback = flip `Status` back to `active`.
2. `CurationRun` audit record (peer of `MaintenanceRun`): run ID, catalog
   fingerprint, and per-candidate outcomes (candidate, verdict, short
   rationale, verify result, execution status/error). Repository adds
   `SaveCurationRun` / `CurationRun(id)`; the run-ID existence check is the
   idempotency mechanism.
3. `page_embeddings` cache table: `(page_id, revision_id, vector)`,
   append-only, superseded rows deleted opportunistically.

## Curator port contract

Inputs contain only what the LLM needs — no internal IDs beyond opaque
handles the service resolves back deterministically:

- `PairJudgeInput`: both pages' full current text plus each page's citation
  quotes with their source timestamps.
- `PageJudgeInput`: one page's full current text, its citation quotes with
  timestamps, and the quality signals that selected it.

Verdicts (strict JSON, decoded and validated in Go):

- `JudgePair` → `merge` | `conflict` | `distinct`. `merge` and `conflict`
  carry a merged draft (title, summary, sections). A `conflict` draft must
  resolve contradictions in favor of the newer source evidence and state in
  prose what was superseded.
- `JudgePage` → `retire` | `rewrite` | `keep`. `rewrite` carries the new
  draft (title may change to satisfy concept-shape rules; slug never
  changes).

## Conservative execution policy (service layer)

- Destructive verdicts (`merge`, `conflict`, `retire`) trigger a second,
  independent Curator call with a skeptic prompt ("argue why these pages must
  NOT be merged / this page must NOT be retired"). If the skeptic prevails,
  the candidate degrades to `keep`. `distinct` / `keep` / `rewrite` skip
  verify: they lose no page, and rewrites still pass full deterministic draft
  validation.
- Any LLM failure or invalid JSON: retry once, then degrade to `keep`
  (mirrors the planner degradation policy — never act on output that did not
  parse).

## Action semantics

- **Merge.** Survivor chosen deterministically: more inbound links wins, tie
  broken by earlier page creation — the LLM never picks. The new survivor
  revision uses the verdict draft for prose; **citations are the union of
  both pages' current-revision anchors**, re-emitted into the survivor's
  `Source evidence` section (anchors reference immutable `SourceRevision`s,
  so no re-anchoring against sources is needed; identical quotes dedupe to
  satisfy the exactly-once text rule). Links are the union minus the pair's
  mutual links. Publication goes through the existing `PublishPage` CAS; on
  success the losing page is `RetirePage`d with `SuccessorPageID` = survivor.
- **Conflict.** Same execution path as merge; the difference is the verdict
  requirement (newer-evidence resolution stated in prose). If the model
  cannot rank the evidence or verify disagrees, the whole candidate degrades
  to `keep` — a visible contradiction is preferred over a wrong resolution.
- **Rewrite.** New revision on the same page: fresh prose from the draft,
  citations carried forward as the full anchor set of the current revision,
  slug unchanged.
- **Retire.** `RetirePage` only (no successor unless part of a merge).
- Every successful action calls the existing `markTreeDirty`; the topic tree
  catches up through its own debounced rebuild.

## Error handling

- **Per-candidate isolation**: one candidate's failure is recorded in the
  `CurationRun` and never aborts the rest of the round (same philosophy as
  `prepareTargets`).
- **Execution-time CAS conflict** (page changed between judgment and
  publish/retire): the action is dropped. The catalog fingerprint has
  changed, so the next round re-evaluates from scratch; no compensation
  logic.
- **Merge is two steps** (publish survivor, then retire loser) and
  self-heals: if the retire step fails, the next round re-detects the pair
  (similarity is still high), the re-judged merge draft lands on a survivor
  that already contains the merged content, `revisionsEquivalent` turns the
  publish into a no-op, and the retire is retried.
- **Embedding service unavailable**: skip the duplicate-pair lane for this
  round, run the quality lane normally, log a warning.
- **Round-level load failures** (catalog, settings, tree): log and skip the
  round, identical to `reindexTree`'s posture.

## Concurrency

- Rounds are serialized with each other by a mutex (same pattern as
  `treeReindexMu`). Concurrency with ingest is safe through `PublishPage`
  CAS and `RetirePage` CAS; curation takes no locks against ingest.
- Two new ingest-boundary rules:
  1. `PublishPage` rejects an update whose target page is retired
     (publication conflict). This closes the race where a page is retired
     between plan and commit; the planner never sees retired pages in its
     catalog, so this is rare by construction.
  2. **Slug revival.** Retired pages keep their slug reserved. When a later
     session produces a `create` brief whose slug collides with a retired
     page, the existing create-to-update salvage path converts it into an
     update on that page and flips `Status` back to `active`. Evidence for a
     re-awakened topic accumulates on the original page instead of failing
     or forking.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `LLMWIKI_CURATION_INTERVAL` | `24h` | Round interval; `0` disables curation |
| `LLMWIKI_CURATION_PAIR_LIMIT` | `8` | Max duplicate pairs judged per round |
| `LLMWIKI_CURATION_PAGE_LIMIT` | `8` | Max quality-lane pages judged per round |

The Curator is wired in `main.go` only when the LLM maintainer pair is
configured (`LLMWIKI_LLM_*`), reusing the same DeepSeek client, model, and
token metering (`component = "pagewiki-curator"`). Similarity threshold and
quality-score floors are code constants until evidence says they need tuning.

## Testing

- **Acceptance (scripted Curator fake, BDD style like existing suites):**
  - merge happy path: survivor gets union citations, loser retired with
    successor, hidden from catalog/navigation/search, tree marked dirty;
  - skeptic veto: destructive verdict refuted → zero repository writes,
    `CurationRun` records the degradation to keep;
  - retire: revisions intact, redirect info served, rollback restores;
  - rewrite: citations carried forward, slug stable, title may change;
  - conflict: newer-evidence resolution recorded; unresolvable → both pages
    untouched;
  - idempotency: unchanged fingerprint → second round makes no Curator calls;
  - CAS race: ingest publishes to the loser between judge and execute → the
    merge is dropped cleanly;
  - slug revival: create on a retired slug salvages to update and
    reactivates the page.
- **Unit:** candidate detection determinism and caps, fingerprint stability,
  survivor selection rule, citation union dedupe.
- **Postgres integration:** `RetirePage` CAS, retired exclusion in
  catalog/navigation/search, update-to-retired rejection, embedding cache
  keyed by revision.
