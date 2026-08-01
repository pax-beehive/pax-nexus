# PageWiki Curation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A fourth LLM role (Curator) that periodically merges near-duplicate wiki pages, retires low-value pages, resolves cross-page contradictions, and rewrites substandard pages — fully automatic, conservative by mechanism, nothing physically deleted.

**Architecture:** Deterministic candidate detection (embeddings + heuristics, zero tokens) feeds per-candidate LLM judgments; destructive verdicts must survive an adversarial verify call; execution goes through the existing CAS publication path plus a new append-only retire lifecycle. The postgres layer is a JSON event log hydrated into the in-memory repository, so persistence = new log tables + hydrate steps.

**Tech Stack:** Go 1.x, testify suite tests, pgx/pgxpool, DeepSeek via `internal/platform/llm`, embeddings via `internal/platform/textembedding`.

**Spec:** `docs/superpowers/specs/2026-07-31-wiki-curation-design.md`

## Global Constraints

- Spec deviations agreed during planning: merge-survivor tie-break is the lexicographically smaller PageID (the domain records no creation time); carried-forward links are rebuilt as a `Related knowledge` section from the union of outgoing link targets (old byte-anchored links cannot survive a full-text rewrite); the spec's `RetiredAt` field is dropped — the pagewiki domain is clock-free, and the postgres lifecycle row's `created_at` already records when the retire happened.
- Env vars: `LLMWIKI_CURATION_INTERVAL` (default `24h`, `0` disables), `LLMWIKI_CURATION_PAIR_LIMIT` (default 8), `LLMWIKI_CURATION_PAGE_LIMIT` (default 8).
- Code constants: similarity threshold `0.86`, quality body floor `400` bytes.
- Token metering component: `wiki-curator`.
- `PageStatus` zero value is active (`""`) so pre-existing JSON payloads hydrate unchanged.
- All new behavior degrades to no-op on any failure; never act on unparsed LLM output; a failed verify counts as refuted.
- Test style: testify `suite`, `TestGiven...When...Then...` names, scripted fakes in `internal/pagewiki/scripted.go`.
- Run tests with `go test ./internal/pagewiki/... ./internal/platform/postgres/...` (postgres-tagged tests may need the local DB; run what CI runs: `go test ./...` before finishing a task, and note that main-branch lint/DB gates have known pre-existing failures — do not try to fix unrelated red).
- Commit after every task with a `feat(pagewiki): ...` / `test(pagewiki): ...` message ending in the Claude co-author trailer.

---

### Task 1: Page lifecycle (retire / revive) in domain and memory repository

**Files:**
- Modify: `internal/pagewiki/types.go` (Page fields, PageStatus, RetireRequest, PagePublication.Revive)
- Modify: `internal/pagewiki/ports.go` (Repository: RetirePage)
- Modify: `internal/pagewiki/memory/repository.go`
- Test: `internal/pagewiki/memory/lifecycle_test.go` (new file)

**Interfaces:**
- Produces:
  ```go
  type PageStatus string
  const (
      PageStatusActive  PageStatus = "" // zero value: active, keeps old payloads valid
      PageStatusRetired PageStatus = "retired"
  )
  // Page gains: Status PageStatus; SuccessorPageID string; RetiredByRunID string
  func (p Page) Retired() bool { return p.Status == PageStatusRetired }

  type RetireRequest struct {
      PageID                 string
      ExpectedBaseRevisionID string // CAS against CurrentRevisionID
      SuccessorPageID        string // optional
      RunID                  string
  }
  // PagePublication gains: Revive bool
  // Repository gains: RetirePage(context.Context, RetireRequest) error
  ```
- Behavior later tasks rely on: `PageCatalog`, `Navigation`, `Search` exclude retired pages; `PageByID`/`PageBySlug`/`PageRevisionHistory` still return them; publishing an update (`Revision.BaseRevisionID != ""`) to a retired page fails unless `Revive` is true; a `Revive` publication flips the page back to active and clears successor fields.

- [ ] **Step 1: Write failing tests** in `internal/pagewiki/memory/lifecycle_test.go` (same suite pattern as `repository_test.go`; build fixtures locally — publish one page via `PublishPage` with a minimal `PagePublication{Page: ..., Revision: pagewiki.PageRevision{ID: "rev-1", PageID: "page-1", Title: "T", Summary: "S", Sections: []pagewiki.PageSection{{Key: "k", Heading: "H", Markdown: "body"}}, Markdown: "# T"}}`):
  - `TestGivenPublishedPageWhenRetiredThenCatalogNavigationSearchExcludeIt` — retire with matching `ExpectedBaseRevisionID`; assert `PageCatalog` no longer lists it, `Navigation` has no entry, `Search` returns nothing for its text, but `PageByID` returns `Status == PageStatusRetired` with `SuccessorPageID`/`RetiredByRunID` set and `PageRevisionHistory` intact.
  - `TestGivenStaleRevisionWhenRetiredThenRevisionConflict` — `ExpectedBaseRevisionID: "rev-stale"` → error wraps `pagewiki.ErrRevisionConflict`; page stays active.
  - `TestGivenUnknownPageWhenRetiredThenNotFound` — wraps `pagewiki.ErrNotFound`.
  - `TestGivenRetiredPageWhenUpdatePublishedThenConflict` — publish a revision with `BaseRevisionID: "rev-1"` on the retired page, no Revive → error; with `Revive: true` → succeeds, `PageByID` shows active with successor fields cleared.
- [ ] **Step 2: Run tests, verify they fail** — `go test ./internal/pagewiki/memory/ -run Lifecycle -v` fails to compile (missing types/methods).
- [ ] **Step 3: Implement.** In `types.go` add the types/fields above. In `ports.go` add `RetirePage` to `Repository`. In `memory/repository.go`:
  - `RetirePage`: lock; look up page (`ErrNotFound`); CAS `page.CurrentRevisionID == request.ExpectedBaseRevisionID` else `fmt.Errorf("retire Page %q: %w: revision changed", request.PageID, pagewiki.ErrRevisionConflict)`; set Status/SuccessorPageID/RetiredByRunID; store.
  - `PageCatalog`: `if page.Retired() { continue }`. `Navigation`: skip retired pages and their placements. `Search`: skip chunks whose page is retired.
  - `validatePublication`: when the target page exists, is retired, and the publication is not `Revive`, reject with `ErrRevisionConflict` ("page is retired"). In `applyPublication`, when `Revive`, reset `Status`, `SuccessorPageID`, `RetiredByRunID` to zero values.
- [ ] **Step 4: Run tests, verify pass**; also `go test ./internal/pagewiki/...` for regressions.
- [ ] **Step 5: Commit** `feat(pagewiki): page retire lifecycle in domain and memory repository`.

---

### Task 2: Curation run, page-embedding cache, and source ordinals

**Files:**
- Modify: `internal/pagewiki/types.go`
- Modify: `internal/pagewiki/ports.go`
- Modify: `internal/pagewiki/memory/repository.go`
- Test: `internal/pagewiki/memory/curation_records_test.go` (new file)

**Interfaces:**
- Produces:
  ```go
  type CurationVerdict string
  const (
      CurationVerdictMerge    CurationVerdict = "merge"
      CurationVerdictConflict CurationVerdict = "conflict"
      CurationVerdictDistinct CurationVerdict = "distinct"
      CurationVerdictRetire   CurationVerdict = "retire"
      CurationVerdictRewrite  CurationVerdict = "rewrite"
      CurationVerdictKeep     CurationVerdict = "keep"
  )
  type CurationOutcome struct {
      Kind      string // "pair" | "page"
      PageIDs   []string
      Verdict   CurationVerdict
      Rationale string
      Refuted   bool
      Status    TargetStatus
      Error     string
  }
  type CurationRun struct {
      ID          string
      Fingerprint string
      Status      RunStatus
      Outcomes    []CurationOutcome
  }
  type PageEmbedding struct {
      PageID     string
      RevisionID string
      Vector     []float32
  }
  // Repository gains:
  //   SaveCurationRun(context.Context, CurationRun) error
  //   CurationRun(context.Context, string) (CurationRun, error)
  //   PageEmbeddings(context.Context) ([]PageEmbedding, error)
  //   SavePageEmbedding(context.Context, PageEmbedding) error
  //   SourceRevisionOrdinals(context.Context) (map[string]int, error)
  ```
- `SourceRevisionOrdinals` returns the 0-based order in which source revisions were saved (postgres hydration replays in `created_at` order, so the ordinal is a chronology proxy for evidence recency).

- [ ] **Step 1: Write failing tests** in `curation_records_test.go`: save/load a `CurationRun` round-trips and unknown ID wraps `ErrNotFound`; `SavePageEmbedding` for the same PageID with a new RevisionID replaces the old row (`PageEmbeddings` returns one entry per page); `SourceRevisionOrdinals` reflects save order and re-saving the same revision ID keeps its original ordinal.
- [ ] **Step 2: Run, verify compile failure.**
- [ ] **Step 3: Implement** in memory repository: new maps `curationRuns map[string]pagewiki.CurationRun`, `pageEmbeddings map[string]pagewiki.PageEmbedding` (keyed by PageID), `sourceOrder map[string]int` (assigned in `SaveSourceRevision` when absent; also initialize all three in `Reset`). Deep-copy runs/embeddings on read and write (follow the copy discipline visible in `SourceRevision`).
- [ ] **Step 4: Run tests, verify pass.**
- [ ] **Step 5: Commit** `feat(pagewiki): curation run and embedding records in memory repository`.

---

### Task 3: Postgres persistence and hydration for curation records

**Files:**
- Create: `internal/platform/postgres/migrations/026_pagewiki_curation.sql`
- Modify: `internal/pagewiki/postgres/repository.go`
- Test: `internal/pagewiki/postgres/repository_test.go` (extend, follow existing suite/tagging conventions)

**Interfaces:**
- Consumes Task 1–2 types. Produces: postgres `Repository` implements the five new methods plus `RetirePage`, persisting via the JSON-log pattern and hydrating on startup; `RebuildPageWiki` clears the new tables.

- [ ] **Step 1: Migration** `026_pagewiki_curation.sql`:
  ```sql
  CREATE TABLE IF NOT EXISTS pagewiki_page_lifecycle (
      scope_id TEXT NOT NULL,
      ordinal BIGSERIAL,
      event_id TEXT NOT NULL,
      payload JSONB NOT NULL,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      PRIMARY KEY (scope_id, event_id)
  );
  CREATE TABLE IF NOT EXISTS pagewiki_curation_runs (
      scope_id TEXT NOT NULL,
      run_id TEXT NOT NULL,
      payload JSONB NOT NULL,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      PRIMARY KEY (scope_id, run_id)
  );
  CREATE TABLE IF NOT EXISTS pagewiki_page_embeddings (
      scope_id TEXT NOT NULL,
      page_id TEXT NOT NULL,
      payload JSONB NOT NULL,
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      PRIMARY KEY (scope_id, page_id)
  );
  ```
- [ ] **Step 2: Write failing tests** (extend the postgres suite): retire a hydrated page, rebuild the repository from the pool, assert the page is still retired after re-hydration; save a curation run and an embedding, re-open, assert both load; `RebuildPageWiki` empties all three new tables.
- [ ] **Step 3: Implement.** `RetirePage`: delegate to memory first (CAS validation), then `insertJSON` into `pagewiki_page_lifecycle` with `event_id = stableID`-style deterministic ID — use `request.PageID + ":" + request.ExpectedBaseRevisionID` as the event ID and marshal the whole `RetireRequest` as payload. Hydrate order in `hydrate`: sources → publications → **lifecycle events** (replay `RetirePage` on memory; note `Revive` publications already restore active state because they replay in the publication log after being written — to keep ordering exact, replay lifecycle and publications from a single merged ordering is NOT needed: a revive is always a later publication row, and hydration applies publications before lifecycle; therefore a retire event whose page was later revived must be skipped. Encode that by having the postgres `RetirePage` also record `ExpectedBaseRevisionID`, and during hydration ignore a lifecycle event whose page's `CurrentRevisionID` no longer equals it — the CAS check does this for free) → runs → tree. `SaveCurationRun`/`CurationRun`, `SavePageEmbedding` (`INSERT ... ON CONFLICT (scope_id, page_id) DO UPDATE SET payload = $3, updated_at = NOW()`), `PageEmbeddings` hydrate into memory on startup like the other tables. Add the three `DELETE FROM` lines to `RebuildPageWiki`.
- [ ] **Step 4: Run** `go test ./internal/pagewiki/postgres/ -v` (respect existing build tags / local DB requirements).
- [ ] **Step 5: Commit** `feat(pagewiki): persist curation lifecycle, runs, and embeddings`.

---

### Task 4: Curator and embedder ports plus scripted fakes

**Files:**
- Modify: `internal/pagewiki/ports.go`
- Modify: `internal/pagewiki/scripted.go`

**Interfaces:**
- Produces (exact shapes later tasks depend on):
  ```go
  type CurationQuote struct {
      ExactText     string
      SourceOrdinal int
  }
  type CurationPageView struct {
      PageID   string
      Title    string
      Summary  string
      Markdown string
      Quotes   []CurationQuote
  }
  type PairJudgeInput struct {
      A, B       CurationPageView
      Directives GenerationDirectives
  }
  type PageJudgeInput struct {
      Page       CurationPageView
      Signals    []string // human-readable reasons the page was selected
      Directives GenerationDirectives
  }
  type CurationDraft struct {
      Title    string
      Summary  string
      Sections []SectionDraft
  }
  type PairVerdict struct {
      Verdict   CurationVerdict // merge | conflict | distinct
      Rationale string
      Draft     *CurationDraft // required for merge/conflict
  }
  type PageVerdict struct {
      Verdict   CurationVerdict // retire | rewrite | keep
      Rationale string
      Draft     *CurationDraft // required for rewrite
  }
  type VerifyInput struct {
      Action     CurationVerdict
      Rationale  string
      Pages      []CurationPageView
      Directives GenerationDirectives
  }
  type VerifyVerdict struct {
      Refuted   bool
      Rationale string
  }
  type Curator interface {
      JudgePair(context.Context, PairJudgeInput) (PairVerdict, error)
      JudgePage(context.Context, PageJudgeInput) (PageVerdict, error)
      Verify(context.Context, VerifyInput) (VerifyVerdict, error)
  }
  type TextEmbedder interface {
      Embed(ctx context.Context, texts []string) ([][]float32, error)
  }
  ```
- Scripted fakes (patterned on `ScriptedPlanner`):
  ```go
  type ScriptedCurator struct {
      PairVerdicts map[string]PairVerdict // key: sorted "idA|idB"
      PageVerdicts map[string]PageVerdict // key: pageID
      Verifies     map[string]VerifyVerdict // key: same as the judged candidate; zero value = not refuted
      Errs         map[string]error
      JudgeCalls   *int
      VerifyCalls  *int
  }
  type ScriptedEmbedder struct {
      Vectors map[string][]float32 // key: exact input text; missing key = error
      Err     error
  }
  ```
  `ScriptedCurator` key helper `pairKey(a, b string) string` (sorted, `|`-joined) is exported from `scripted.go` as `PairKey` for tests.

- [ ] **Step 1:** Add the types to `ports.go`, fakes to `scripted.go` (fakes return `Errs[key]` first, then the mapped verdict; a missing verdict key returns an error so tests fail loudly). Compile: `go build ./internal/pagewiki/...`.
- [ ] **Step 2: Commit** `feat(pagewiki): curator port and scripted fakes`.

---

### Task 5: Deterministic candidate detection

**Files:**
- Create: `internal/pagewiki/curation_candidates.go`
- Test: `internal/pagewiki/curation_candidates_test.go`

**Interfaces:**
- Produces (package-private, used by Task 7):
  ```go
  const (
      curationPairSimilarityThreshold = 0.86
      curationBodyByteFloor           = 400
      curationDefaultPairLimit        = 8
      curationDefaultPageLimit        = 8
  )
  type pagePair struct {
      AID, BID   string
      Similarity float64
  }
  func catalogFingerprint(catalog PageCatalog) string
  func cosineSimilarity(a, b []float32) float64
  func duplicatePairs(catalog PageCatalog, vectors map[string][]float32, tree TopicTree, limit int) []pagePair
  func qualityCandidates(catalog PageCatalog, revisions map[string]PageRevision, incomingLinks map[string]int, limit int) []qualityCandidate
  type qualityCandidate struct {
      PageID  string
      Signals []string
  }
  func curationEmbeddingText(entry PageCatalogEntry) string // Title + "\n" + Summary
  ```
- Rules: `catalogFingerprint` sorts `pageID + "@" + currentRevisionID` strings and feeds them to `stableID("curation-fingerprint", ...)`. `duplicatePairs` emits pairs above the threshold plus same-tree-leaf pairs whose `topicSlug(title)` values are equal (assign those `Similarity` 1.0), dedupes, sorts by similarity descending then by `AID` for determinism, caps at `limit`; a page appears in at most one pair per round (skip pairs whose member is already taken). `qualityCandidates` scores: orphan (no incoming links and the revision has no outgoing links) = 1, body `len(revision.Markdown) < curationBodyByteFloor` = 1, `normalizeProposedTitle(entry.Title) == ""` = 1; select score ≥ 2, sort by score descending then PageID, cap at `limit`, record the firing signals as strings (`"orphan"`, `"short-body"`, `"sentence-title"`).

- [ ] **Step 1: Write failing tests**: fingerprint is order-independent and revision-sensitive; cosine of identical/orthogonal vectors; near-duplicate pair detected above threshold and capped by limit with deterministic order; a page never appears in two pairs; same-leaf identical-slugged-title pair detected without embeddings; quality scoring selects an orphan+short page but not a merely-short page; signals recorded.
- [ ] **Step 2: Run, verify failure.**
- [ ] **Step 3: Implement** (pure functions; `topicSlug` and `normalizeProposedTitle` already exist in the package).
- [ ] **Step 4: Run, verify pass.**
- [ ] **Step 5: Commit** `feat(pagewiki): deterministic curation candidate detection`.

---

### Task 6: Curation draft assembly (citation carry-forward, related rebuild)

**Files:**
- Create: `internal/pagewiki/curation_publish.go`
- Test: `internal/pagewiki/curation_publish_test.go`

**Interfaces:**
- Produces:
  ```go
  // buildCurationDraft assembles the deterministic PageDraft for a curation
  // rewrite or merge: normalized LLM sections + a rebuilt "Source evidence"
  // section carrying forward the union of anchors + a rebuilt "Related
  // knowledge" section from outgoing link targets.
  func buildCurationDraft(
      slug string,
      draft CurationDraft,
      carried []PageCitation, // union of the source revisions' citations
      related []RelatedPage,  // union of outgoing link targets (already excludes merged pair)
  ) (PageDraft, error)
  ```
- Rules: validate `draft.Title`/`draft.Summary` non-empty and ≥1 section (else error). Normalize section keys with the existing `uniqueLLMSectionKey`. Collect carried quotes: flatten every `PageCitation.SourceAnchors`; dedupe anchors by `ID`; group by `ExactQuote`; drop any quote that is a substring of another kept quote (longest-first, same overlap rule as `relatedKnowledgeSection`); require ≥1 surviving quote (a page with no citations cannot be curated — return error, the caller degrades to keep). Emit `source-evidence` section = quotes joined by `"\n\n"`, one `CitationDraft{SectionKey: "source-evidence", ExactText: quote, Evidence: nil}` per quote **carrying the anchors directly**: extend `CitationDraft` with `Anchors []SourceAnchor` (json-tagged, empty for the session path) and teach the publication path (Task 7) to use `Anchors` verbatim when present instead of re-resolving `Evidence` against a source revision. Append the `relatedKnowledgeSection(related)` output when non-empty.

- [ ] **Step 1: Write failing tests**: merged draft contains normalized LLM sections plus `source-evidence` with deduped union quotes and per-quote anchors preserved; a quote contained in a longer quote is dropped; zero surviving quotes → error; related section and links emitted; empty title → error.
- [ ] **Step 2: Run, verify failure.**
- [ ] **Step 3: Implement** (`CitationDraft.Anchors` added in `types.go`).
- [ ] **Step 4: Run, verify pass**, plus `go test ./internal/pagewiki/...`.
- [ ] **Step 5: Commit** `feat(pagewiki): curation draft assembly with anchor carry-forward`.

---

### Task 7: Curation round orchestration in the service

**Files:**
- Create: `internal/pagewiki/curation_service.go`
- Modify: `internal/pagewiki/service.go` (options only), `internal/pagewiki/service_helpers_test.go` if fixtures need it
- Test: `internal/pagewiki/curation_acceptance_test.go`

**Interfaces:**
- Consumes Tasks 1–6. Produces:
  ```go
  type CurationConfig struct {
      Interval  time.Duration // 0 disables the background loop
      PairLimit int           // 0 → curationDefaultPairLimit
      PageLimit int           // 0 → curationDefaultPageLimit
  }
  func WithCurator(curator Curator, embedder TextEmbedder, config CurationConfig, logger *slog.Logger) ServiceOption
  func (s *Service) RunCurationRound(ctx context.Context) (CurationRun, error)
  ```
- Round flow (all in `curation_service.go`):
  1. `curationMu` serializes rounds. Load catalog; `fingerprint := catalogFingerprint(catalog)`; `runID := stableID("curation-run", fingerprint)`; if `s.repository.CurationRun(ctx, runID)` exists → return it (idempotent skip, zero curator calls).
  2. Load directives, topic tree, source ordinals. Compute vectors: for each catalog entry reuse the cached `PageEmbedding` when `RevisionID` matches, else `embedder.Embed` the batch of stale texts and `SavePageEmbedding`; on embed error log-warn and proceed with `vectors = nil` (pair lane off).
  3. Build candidates via `duplicatePairs` and `qualityCandidates` (incoming-link counts from `s.repository.PageLinks` per catalog page).
  4. Judge each candidate with per-candidate isolation (an error in one candidate records a failed outcome, continues). Judge errors retry once (`curatorAttempts = 2`), then degrade to `keep` outcome with `Status: TargetStatusFailed` and the error string.
  5. Destructive verdicts (`merge`/`conflict`/`retire`) call `curator.Verify`; verify error or `Refuted` → outcome verdict preserved, `Refuted: true`, no execution, `Status: TargetStatusSucceeded` (the conservative no-op is the intended behavior).
  6. Execute:
     - **merge/conflict**: survivor = more incoming links, tie → lexicographically smaller PageID; loser = the other. `buildCurationDraft(survivor.Slug, *verdict.Draft, unionCitations, unionRelated)`; build the revision exactly like `buildPublication` does but using `CitationDraft.Anchors` directly (new helper `buildCurationPublication(ctx, page, currentRevision, draft) (Page, PageRevision, error)` in `curation_service.go` that reuses `validateDraft`-equivalent checks, `uniqueTextRange`, `renderMarkdown`, `revisionIdentity`, and `s.buildLinks` with `linkable = nil`); `PublishPage` (CAS via `BaseRevisionID`); then `RetirePage{PageID: loser.ID, ExpectedBaseRevisionID: loserRevisionID, SuccessorPageID: survivor.ID, RunID: runID}`. Any error → failed outcome, no compensation (next round self-heals per spec).
     - **rewrite**: same publication helper on the page itself, no retire.
     - **retire**: `RetirePage` without successor.
  7. `SaveCurationRun`; if any outcome executed a page change, `s.markTreeDirty()`. Run status via existing `RunStatus` semantics (all succeeded → succeeded, mix → partial_success, all failed → failed, empty candidates → succeeded).
- `CurationPageView` assembly: `Markdown` from the current revision, `Quotes` from its citations (`ExactText` of each source anchor with `SourceOrdinal` looked up via `SourceRevisionOrdinals`).

- [ ] **Step 1: Write failing acceptance tests** in `curation_acceptance_test.go` using memory repository + `ScriptedCurator`/`ScriptedEmbedder` (seed two near-duplicate pages by giving both identical vectors; publish pages with citations carrying `SourceAnchors` so carry-forward has material):
  - merge happy path: survivor has a new revision whose citations contain both pages' anchors, loser retired with `SuccessorPageID`, catalog hides loser, curation run recorded, `FlushTreeReindex`-observable dirty mark set (assert via a `ScriptedTreeIndexer`-style captured call or `treeDirty` channel through an exported test helper in `export_test.go`).
  - skeptic veto: verify refuted → zero writes (page count, revision count unchanged), outcome recorded with `Refuted: true`.
  - retire path: quality candidate (orphan, short, bad title) retired; revisions intact.
  - rewrite path: new revision, slug unchanged, anchors carried.
  - conflict-unresolvable: curator returns `conflict` with nil draft → degrades to keep, both pages untouched.
  - idempotency: second `RunCurationRound` on unchanged catalog returns the stored run, `JudgeCalls` unchanged.
  - CAS race: between judgment and execution the test publishes a new revision to the loser (scripted curator verdict fixed); merge outcome fails, no partial state (survivor unchanged too — publish succeeded? No: build the race on the survivor so `PublishPage` CAS fails first; assert loser still active).
  - embedder failure: `ScriptedEmbedder.Err` set → pair lane empty, quality lane still judged.
- [ ] **Step 2: Run, verify failure.**
- [ ] **Step 3: Implement** `curation_service.go` per the flow above; add `curator`, `curationEmbedder`, `curationConfig`, `curationMu` fields to `Service`; `WithCurator` sets them (nil-safe defaults; logger fallback `observability.DiscardLogger()`).
- [ ] **Step 4: Run the full package tests.**
- [ ] **Step 5: Commit** `feat(pagewiki): curation round orchestration`.

---

### Task 8: Background curation loop

**Files:**
- Modify: `internal/pagewiki/curation_service.go`
- Test: `internal/pagewiki/curation_maintenance_test.go`

**Interfaces:**
- Produces: `func (s *Service) StartCurationMaintenance(ctx context.Context)` — no-op when curator is nil or `Interval <= 0`; otherwise a goroutine ticking at `Interval`, each tick calling `RunCurationRound` and logging (never propagating) errors; stops on ctx cancel.

- [ ] **Step 1: Failing test**: with a 10ms interval and a scripted curator, `StartCurationMaintenance` runs at least one round (poll the repository for the saved run with a deadline, pattern-match `tree_maintenance_test.go`); with `Interval: 0` no round ever runs.
- [ ] **Step 2: Run, verify failure.**
- [ ] **Step 3: Implement** with `time.Ticker` + `ctx.Done()` select.
- [ ] **Step 4: Run, verify pass.**
- [ ] **Step 5: Commit** `feat(pagewiki): background curation maintenance loop`.

---

### Task 9: Slug revival on create collision with a retired page

**Files:**
- Modify: `internal/pagewiki/service.go` (`resolvePage`, `commitTarget` publication construction)
- Test: extend `internal/pagewiki/update_acceptance_test.go` or new `revival_acceptance_test.go`

**Interfaces:**
- Behavior: in `resolvePage`, for `PageActionCreate`, first `PageBySlug(brief.ProposedSlug)`; if found **and retired**, return that page with its current revision (an update-shaped resolution) and mark the prepared target so the publication sets `Revive: true`. Add field `revive bool` to `preparedTarget`; `buildPublication` callers thread it onto `PagePublication{Revive: ...}`. If found and active, keep today's behavior (publication-conflict path). Draft slug validation: the create brief's `ProposedSlug` equals the revived page's slug by construction, so `validateDraft`'s create check still passes; `resolvePage` returning a non-nil `currentRevision` makes the editor treat it as an update (current text injected).

- [ ] **Step 1: Failing acceptance test**: retire a page (via repository), then run `InjectSession` with a scripted planner emitting a create brief for the same slug and a scripted editor draft; assert the page is active again, same PageID, new revision chains `BaseRevisionID` to the old one, and no second page with a suffixed slug exists.
- [ ] **Step 2: Run, verify failure.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run full package tests (regression: plain create, create-vs-active-slug conflict).**
- [ ] **Step 5: Commit** `feat(pagewiki): revive retired pages on create slug collision`.

---

### Task 10: LLM Curator adapter

**Files:**
- Create: `internal/pagewiki/llm_curator.go`
- Test: `internal/pagewiki/llm_curator_test.go`

**Interfaces:**
- Produces:
  ```go
  type LLMCuratorConfig struct {
      Client llm.ChatClient
      Model  string
      Logger *slog.Logger
  }
  func NewLLMCurator(config LLMCuratorConfig) (*LLMCurator, error) // nil client / empty model → error
  var _ Curator = (*LLMCurator)(nil)
  ```
- Three prompts (package consts, follow the tone/format of `pageWikiPlannerPrompt`), each ending with "Return JSON only", each request built like the planner (system = prompt + `generationDirectivesPrompt(directives)`, user = JSON payload), `curatorAttempts = 2` retries on call or decode error, `trimJSONFence` before decode:
  - `pageWikiCuratorPairPrompt`: input `{"a":{"title","summary","markdown","quotes":[{"text","recency_rank"}]},"b":{...}}`; output `{"verdict":"merge|conflict|distinct","rationale":"...","draft":{"title","summary","sections":[{"key","heading","markdown"}]}}`. Rules in prompt: `distinct` unless the two pages clearly describe the same subject; `conflict` only when they state incompatible facts about the same subject, and the draft must keep the claim with the higher recency_rank and state in prose what was superseded; drafts follow the concept-title rules (noun phrase, no sentence titles).
  - `pageWikiCuratorPagePrompt`: input `{"page":{...},"signals":["orphan","short-body","sentence-title"]}`; output `{"verdict":"retire|rewrite|keep","rationale":"...","draft":{...}}`. Rules: `retire` only for content with no durable team value (logs, fragments, one-off noise); `rewrite` when the subject is durable but the page violates title/structure rules; when in doubt, `keep`.
  - `pageWikiCuratorVerifyPrompt`: input `{"action":"merge|conflict|retire","rationale":"...","pages":[{...}]}`; output `{"refuted":true|false,"rationale":"..."}`. The prompt instructs the model to argue AGAINST the action and refute unless the case is unambiguous.
- Decode into adapter-local structs (`llmPairVerdict` etc.), map to domain types; unknown verdict string → decode error (drives retry, then the service's degrade-to-keep). Missing draft for merge/conflict/rewrite → decode error.

- [ ] **Step 1: Failing tests** with a local stub `ChatClient` defined in the test file (`type stubChat struct{ replies []string; calls int }` returning each reply in order): pair verdict happy decode incl. fenced JSON; invalid verdict retries then errors; verify decode; constructor validation.
- [ ] **Step 2: Run, verify failure.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run, verify pass.**
- [ ] **Step 5: Commit** `feat(pagewiki): LLM curator adapter and prompts`.

---

### Task 11: Wiring — main.go, compose, metering

**Files:**
- Modify: `main.go` (`applicationConfig` fields + env reads near the `llmwiki*` block at ~line 565; `buildPageWikiMaintainers` → also return a `pagewiki.Curator`; `buildPageWikiHTTPHandler` gains the embedder param, builds `CurationConfig`, appends `WithCurator`, calls `service.StartCurationMaintenance(ctx)` next to `StartTreeMaintenance`)
- Modify: `compose.yaml` (pass through the three new env vars next to the existing `LLMWIKI_*` block)

**Interfaces:**
- Consumes: `NewLLMCurator`, `WithCurator`, `CurationConfig`, `textembedding.Embedder` (already satisfies `pagewiki.TextEmbedder`).
- Config parsing: `LLMWIKI_CURATION_INTERVAL` via `time.ParseDuration` (empty → 24h default; parse error → startup error mirroring the tree-depth validation style); the two limits via `strconv.Atoi` (empty → 0 → package defaults; non-positive → startup error). Curator is built only in the `openai|harness` branch, with a metered client `metered("wiki-curator")`. In `local` mode no curator exists. When the embedder is nil (embedding env unset), still wire the curator — the pair lane degrades at runtime per Task 7.

- [ ] **Step 1:** Implement wiring + compose passthrough. `go build ./...`.
- [ ] **Step 2:** Extend the existing main-level config tests if present (grep `llmwikiTreeMaxDepth` tests for the pattern; add interval/limit parse-error cases alongside).
- [ ] **Step 3:** Run `go test ./...` (accept pre-existing main-branch failures only).
- [ ] **Step 4: Commit** `feat(pagewiki): wire curation maintainers and config`.

---

### Task 12: Surface retired state — HTTP API and web

**Files:**
- Modify: `internal/pagewiki/transport/httpapi/endpoints.go` (`GetPage` + `currentPageToAPI` call site), the response struct in `internal/pagewiki/transport/httpapi/model/pagewiki/api/page_wiki.go` (add `Status string` and `SuccessorSlug string` json fields, following how existing fields are declared in that generated-but-hand-maintained model)
- Modify: `web/src/api/wiki.ts` (page type gains `status?: string; successorSlug?: string`), `web/src/pages/WikiBrowsePage.tsx` (archived banner with a link to the successor when present), `web/src/styles/wiki.css` (banner style)
- Test: extend `internal/pagewiki/transport/httpapi/contract_acceptance_test.go`; web tests per existing `web/tests` conventions if a harness exists (otherwise `npm run build` as the check)

**Interfaces:**
- `GetPage`: after loading the page, when `page.Retired()`, resolve `SuccessorPageID` (when set) via `h.reader.PageByID` to obtain its slug; respond with the page as today plus `status: "retired"` and `successorSlug`. Retired pages remain fetchable by slug (repository guarantees this from Task 1).

- [ ] **Step 1: Failing contract test**: fetch a retired page by slug → 200 with `status == "retired"` and the successor slug; active pages → `status` empty/omitted.
- [ ] **Step 2: Run, verify failure; implement API side; verify pass.**
- [ ] **Step 3: Web**: render a banner at the top of the wiki article when `status === "retired"`: text "This page has been archived." plus "See <successor title link>" when `successorSlug` is present (fetch the successor title lazily or just link the slug). Match existing wiki styling. `npm run build` (and `npm test` if configured) passes.
- [ ] **Step 4: Commit** `feat(pagewiki): surface retired pages in API and web`.

---

## Final verification

- [ ] `go build ./... && go vet ./...`
- [ ] `go test ./...` — new failures only where main is already red (document any).
- [ ] `cd web && npm run build`.
- [ ] Re-read the spec end to end; confirm every requirement maps to a merged task or a recorded deviation (the two Global-Constraints deviations are intentional).
- [ ] Prepare PR: branch `feat/wiki-curation`, base `main`, spec + plan + implementation, PR body summarizing the four curation capabilities and the conservative-execution guarantees.
