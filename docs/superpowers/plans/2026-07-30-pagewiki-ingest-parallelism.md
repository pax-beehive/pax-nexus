# PageWiki Ingest Parallelism Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut PageWiki ingest latency by running per-brief LLM edits in parallel, moving the topic-tree reindex off the critical path behind a debounce, and backing off permanently failing streams in the session consumer.

**Architecture:** `Service.InjectSession` splits target processing into a parallel prepare phase (validation + `resolvePage` + `editor.Edit`, bounded by `errgroup.SetLimit(4)`) and a serial commit phase that publishes in brief order, so publish semantics are unchanged. The tree indexer moves to a background goroutine fed by a buffered(1) dirty channel with a 5s-quiet/60s-max debounce, plus a synchronous `FlushTreeReindex` for tests. The consumer keeps an in-memory per-stream failure map with exponential backoff (base = scan interval, factor 2, cap 10 min).

**Tech Stack:** Go 1.25, `golang.org/x/sync/errgroup` (already in go.mod as indirect), testify suites.

**Spec:** `docs/superpowers/specs/2026-07-30-pagewiki-ingest-parallelism-design.md`

## Global Constraints

- No new environment variables. Tunables are constants: `editConcurrency = 4`, `treeReindexQuiet = 5 * time.Second`, `treeReindexMaxWait = 60 * time.Second`, `failureBackoffCap = 10 * time.Minute`.
- Publish order must equal brief order; run/target status semantics, stable IDs, and idempotency keys must not change.
- Per-target failure isolation: one brief's failure must never cancel or fail sibling briefs.
- Repo-wide gate on `main` has pre-existing failures (3 lint + 2 DB tests). Verify with package-scoped commands only: `go build ./...`, `go vet ./internal/pagewiki/...`, `go test ./internal/pagewiki/...`.
- Every commit message ends with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

### Task 1: Parallel prepare phase in `Service.InjectSession`

**Files:**
- Modify: `internal/pagewiki/service.go` (replace `processTarget` with `prepareTargets`/`prepareTarget`/`commitTarget`; new `preparedTarget` struct; new `editConcurrency` const)
- Create: `internal/pagewiki/parallel_edit_acceptance_test.go`
- Modify: `go.mod` / `go.sum` (`golang.org/x/sync` becomes a direct dependency via `go mod tidy`)

**Interfaces:**
- Consumes: existing `Service` fields (`repository`, `editor`), `resolvePage`, `buildPublication`, `revisionsEquivalent`, `failTarget`, `stableID` — all already in `service.go`.
- Produces: unexported `type preparedTarget struct { target MaintenanceTarget; brief PageBrief; page *Page; currentRevision *PageRevision; draft PageDraft; ready bool }`, `func (s *Service) prepareTargets(ctx context.Context, runID string, sourceRevision SourceRevision, catalog PageCatalog, briefs []PageBrief) []preparedTarget`, `func (s *Service) commitTarget(ctx context.Context, sourceRevision SourceRevision, prepared preparedTarget) MaintenanceTarget`. Task 2 adds the dedupe inside `prepareTargets`.

- [ ] **Step 1: Write the failing concurrency test**

Create `internal/pagewiki/parallel_edit_acceptance_test.go`. It reuses `multiPageBriefs()`, `multiPageDrafts(false)`, and `multiPageSource()` from `multi_target_acceptance_test.go` (same `pagewiki_test` package):

```go
package pagewiki_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

// barrierEditor blocks every Edit call until release is closed, so the test
// can observe whether two edits are in flight at the same time.
type barrierEditor struct {
	inner   pagewiki.ScriptedEditor
	entered chan string
	release chan struct{}
}

func (e *barrierEditor) Edit(
	ctx context.Context,
	input pagewiki.EditInput,
) (pagewiki.PageDraft, error) {
	e.entered <- input.Brief.Key
	<-e.release
	return e.inner.Edit(ctx, input)
}

func TestGivenTwoBriefsWhenInjectedThenEditsRunConcurrently(t *testing.T) {
	repository := memory.NewRepository()
	editor := &barrierEditor{
		inner:   pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
		entered: make(chan string, 2),
		release: make(chan struct{}),
	}
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()},
		editor,
	)

	type injectOutcome struct {
		result pagewiki.InjectResult
		err    error
	}
	done := make(chan injectOutcome, 1)
	go func() {
		result, err := service.InjectSession(context.Background(), multiPageSource())
		done <- injectOutcome{result: result, err: err}
	}()

	// Both edits must enter before either is released: proves concurrency.
	deadline := time.After(5 * time.Second)
	for range 2 {
		select {
		case <-editor.entered:
		case <-deadline:
			t.Fatal("edits did not run concurrently: second Edit never started")
		}
	}
	close(editor.release)

	outcome := <-done
	require.NoError(t, outcome.err)
	require.Equal(t, pagewiki.RunStatusSucceeded, outcome.result.Run.Status)
	require.Len(t, outcome.result.Run.Targets, 2)

	_, err := repository.PageBySlug(context.Background(), "sqlite")
	require.NoError(t, err)
	_, err = repository.PageBySlug(context.Background(), "wiki-search")
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestGivenTwoBriefsWhenInjectedThenEditsRunConcurrently -v -timeout 30s`
Expected: FAIL with "edits did not run concurrently" (today the second `Edit` starts only after the first returns, so only one entry arrives before the deadline).

- [ ] **Step 3: Split `processTarget` into prepare (parallel) and commit (serial)**

In `internal/pagewiki/service.go`:

Add to the import block: `"golang.org/x/sync/errgroup"`.

Add to the `const` block at the top:

```go
	editConcurrency = 4
```

Replace the target loop inside `InjectSession` (currently `service.go:121-124`):

```go
	for _, brief := range briefs {
		target := s.processTarget(ctx, run.ID, sourceRevision, catalog, brief)
		run.Targets = append(run.Targets, target)
	}
```

with:

```go
	prepared := s.prepareTargets(ctx, run.ID, sourceRevision, catalog, briefs)
	for index := range prepared {
		run.Targets = append(run.Targets, s.commitTarget(ctx, sourceRevision, prepared[index]))
	}
```

Delete `processTarget` entirely and add in its place:

```go
// preparedTarget carries one brief's prepare-phase outcome into the serial
// commit phase. ready is true only when validation, page resolution, and the
// edit all succeeded; otherwise target already holds the final failed or
// terminal (source-only / ambiguous) state.
type preparedTarget struct {
	target          MaintenanceTarget
	brief           PageBrief
	page            *Page
	currentRevision *PageRevision
	draft           PageDraft
	ready           bool
}

// prepareTargets runs the expensive per-brief work (validation, page
// resolution, and the editor LLM call) concurrently. Each brief writes its
// outcome into its own slot so one target's failure never affects siblings.
// Publication stays out of this phase: commitTarget runs serially in brief
// order so publish semantics are identical to the previous sequential loop.
func (s *Service) prepareTargets(
	ctx context.Context,
	runID string,
	sourceRevision SourceRevision,
	catalog PageCatalog,
	briefs []PageBrief,
) []preparedTarget {
	prepared := make([]preparedTarget, len(briefs))
	var group errgroup.Group
	group.SetLimit(editConcurrency)
	for index, brief := range briefs {
		group.Go(func() error {
			prepared[index] = s.prepareTarget(ctx, runID, sourceRevision, catalog, brief)
			return nil
		})
	}
	_ = group.Wait()
	return prepared
}

func (s *Service) prepareTarget(
	ctx context.Context,
	runID string,
	sourceRevision SourceRevision,
	catalog PageCatalog,
	brief PageBrief,
) preparedTarget {
	result := preparedTarget{
		brief: brief,
		target: MaintenanceTarget{
			ID:       stableID("target", runID, brief.Key),
			BriefKey: brief.Key,
			Action:   brief.Action,
			Status:   TargetStatusFailed,
		},
	}
	if err := ValidatePageBrief(brief, catalog); err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidBrief, err)
		return result
	}
	if err := validateBriefEvidence(brief, sourceRevision); err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidBrief, err)
		return result
	}
	if brief.Action == PageActionSourceOnly || brief.Action == PageActionAmbiguous {
		if brief.Action == PageActionSourceOnly {
			result.target.Status = TargetStatusSucceeded
		} else {
			result.target.Status = TargetStatusPending
		}
		return result
	}
	page, currentRevision, err := s.resolvePage(ctx, sourceRevision.ID, brief)
	if err != nil {
		result.target = failTarget(result.target, TargetFailurePublicationConflict, err)
		return result
	}
	draft, err := s.editor.Edit(ctx, EditInput{
		SourceRevision:  sourceRevision,
		Brief:           brief,
		CurrentPage:     page,
		CurrentRevision: currentRevision,
	})
	if err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidDraft, err)
		return result
	}
	result.page = page
	result.currentRevision = currentRevision
	result.draft = draft
	result.ready = true
	return result
}

func (s *Service) commitTarget(
	ctx context.Context,
	sourceRevision SourceRevision,
	prepared preparedTarget,
) MaintenanceTarget {
	if !prepared.ready {
		return prepared.target
	}
	target := prepared.target
	pageValue, revision, reason, err := s.buildPublication(
		ctx,
		sourceRevision,
		prepared.brief,
		prepared.page,
		prepared.currentRevision,
		prepared.draft,
	)
	if err != nil {
		return failTarget(target, reason, err)
	}
	if prepared.currentRevision != nil &&
		prepared.page.Slug == pageValue.Slug &&
		prepared.page.Title == pageValue.Title &&
		revisionsEquivalent(*prepared.currentRevision, revision) {
		target.PageID = prepared.page.ID
		target.PageRevisionID = prepared.currentRevision.ID
		target.Status = TargetStatusSucceeded
		return target
	}
	publication := PagePublication{
		Page:     pageValue,
		Revision: revision,
	}
	if err := s.repository.PublishPage(ctx, publication); err != nil {
		return failTarget(target, TargetFailurePublicationConflict, err)
	}
	target.PageID = pageValue.ID
	target.PageRevisionID = revision.ID
	target.Status = TargetStatusSucceeded
	return target
}
```

Then run `go mod tidy` (promotes `golang.org/x/sync` to a direct require).

Behavior note (expected, per spec): an update brief whose target page is republished by an *earlier brief in the same run* now fails at `PublishPage` ("base is stale", `memory/repository.go:233-234`) instead of at `resolvePage` ("changed after planning"). Both paths produce `TargetFailurePublicationConflict` wrapping `ErrRevisionConflict`, so target status and failure reason are unchanged; only the message differs. Task 2 makes this case fail before the edit call anyway.

- [ ] **Step 4: Run the new test and the full package suite**

Run: `go test ./internal/pagewiki/ -run TestGivenTwoBriefsWhenInjectedThenEditsRunConcurrently -v -timeout 30s`
Expected: PASS

Run: `go build ./... && go vet ./internal/pagewiki/... && go test ./internal/pagewiki/... -timeout 120s`
Expected: all PASS (existing acceptance suites — multi-target, update, inject, search-links, tree-reindex, plan — must pass unchanged; if any assertion matches the old "changed after planning" message for same-run conflicts, report it, do not silently rewrite it).

Run: `go test ./internal/pagewiki/ -race -run 'TestGivenTwoBriefs|TestMultiTarget' -timeout 60s`
Expected: PASS with no data races.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/service.go internal/pagewiki/parallel_edit_acceptance_test.go go.mod go.sum
git commit -m "feat(pagewiki): run per-brief edits concurrently, commit serially in brief order

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Duplicate-target dedupe before the edit phase

**Files:**
- Modify: `internal/pagewiki/service.go` (add `duplicateUpdateTargets`, wire into `prepareTargets`)
- Create: `internal/pagewiki/duplicate_target_acceptance_test.go`

**Interfaces:**
- Consumes: `prepareTargets`, `preparedTarget`, `failTarget`, `stableID` from Task 1.
- Produces: unexported `func duplicateUpdateTargets(briefs []PageBrief) map[int]struct{}` — returns indexes of update briefs whose non-empty `TargetPageID` already appeared on an earlier brief. Create briefs (empty `TargetPageID`) are never flagged.

- [ ] **Step 1: Write the failing test**

Create `internal/pagewiki/duplicate_target_acceptance_test.go`:

```go
package pagewiki_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

type countingEditor struct {
	inner pagewiki.ScriptedEditor
	calls atomic.Int32
}

func (e *countingEditor) Edit(
	ctx context.Context,
	input pagewiki.EditInput,
) (pagewiki.PageDraft, error) {
	e.calls.Add(1)
	return e.inner.Edit(ctx, input)
}

func TestGivenTwoUpdateBriefsForSamePageThenSecondFailsWithoutAnEditCall(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()

	// Seed run: create the page the update briefs will both target.
	seedService := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	seedResult, err := seedService.InjectSession(ctx, multiPageSource())
	require.NoError(t, err)
	require.Equal(t, pagewiki.RunStatusSucceeded, seedResult.Run.Status)
	page, err := repository.PageBySlug(ctx, "sqlite")
	require.NoError(t, err)

	// Second run: two update briefs aimed at the same page.
	raw := "event-review: The team reaffirmed SQLite after the storage review."
	reviewText := "The team reaffirmed SQLite after the storage review."
	reviewStart := strings.Index(raw, reviewText)
	request := pagewiki.InjectSessionRequest{
		SourceID:       "session-review",
		IdempotencyKey: "session-review-injection",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-review",
			StartByte: reviewStart,
			EndByte:   reviewStart + len(reviewText),
		}},
	}
	updateBrief := func(key string) pagewiki.PageBrief {
		return pagewiki.PageBrief{
			Key:                    key,
			Action:                 pagewiki.PageActionUpdate,
			TargetPageID:           page.ID,
			ExpectedBaseRevisionID: page.CurrentRevisionID,
			EvidenceEventIDs:       []string{"event-review"},
		}
	}
	updatedDraft := pagewiki.PageDraft{
		Slug:    "sqlite",
		Title:   "SQLite",
		Summary: "SQLite stores the local Wiki and survived the storage review.",
		Sections: []pagewiki.SectionDraft{{
			Key:      "decision",
			Heading:  "Decision",
			Markdown: "The team reaffirmed SQLite after the storage review.",
		}},
		Citations: []pagewiki.CitationDraft{{
			SectionKey: "decision",
			ExactText:  "reaffirmed SQLite",
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID:   "event-review",
				ExactText: reviewText,
			}},
		}},
	}
	editor := &countingEditor{inner: pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"first":  updatedDraft,
			"second": updatedDraft,
		},
	}}
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			updateBrief("first"),
			updateBrief("second"),
		}},
		editor,
	)

	result, err := service.InjectSession(ctx, request)

	require.NoError(t, err)
	require.Equal(t, pagewiki.RunStatusPartialSuccess, result.Run.Status)
	require.Equal(t, pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)
	require.Equal(t, pagewiki.TargetStatusFailed, result.Run.Targets[1].Status)
	require.Equal(
		t,
		pagewiki.TargetFailurePublicationConflict,
		result.Run.Targets[1].FailureReason,
	)
	require.EqualValues(t, 1, editor.calls.Load(),
		"duplicate target must not burn an editor call")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestGivenTwoUpdateBriefsForSamePageThenSecondFailsWithoutAnEditCall -v -timeout 30s`
Expected: FAIL on the editor-call assertion (`2 != 1`) — today both briefs pay an edit call and the second dies at publish.

- [ ] **Step 3: Implement the dedupe**

In `internal/pagewiki/service.go`, add:

```go
// duplicateUpdateTargets flags every update brief whose non-empty
// TargetPageID already appeared on an earlier brief. The first brief keeps
// the page; later ones would only burn an editor call before failing the
// base-revision check at publish, so they fail up front with the same
// conflict outcome. Create briefs carry no TargetPageID and are never
// flagged.
func duplicateUpdateTargets(briefs []PageBrief) map[int]struct{} {
	duplicates := make(map[int]struct{})
	seen := make(map[string]struct{}, len(briefs))
	for index, brief := range briefs {
		if brief.Action != PageActionUpdate || brief.TargetPageID == "" {
			continue
		}
		if _, found := seen[brief.TargetPageID]; found {
			duplicates[index] = struct{}{}
			continue
		}
		seen[brief.TargetPageID] = struct{}{}
	}
	return duplicates
}
```

In `prepareTargets`, replace the loop body:

```go
	duplicates := duplicateUpdateTargets(briefs)
	for index, brief := range briefs {
		if _, duplicate := duplicates[index]; duplicate {
			prepared[index] = preparedTarget{
				brief: brief,
				target: failTarget(MaintenanceTarget{
					ID:       stableID("target", runID, brief.Key),
					BriefKey: brief.Key,
					Action:   brief.Action,
					Status:   TargetStatusFailed,
				}, TargetFailurePublicationConflict, fmt.Errorf(
					"%w: Page %q is already targeted by an earlier brief in this run",
					ErrRevisionConflict,
					brief.TargetPageID,
				)),
			}
			continue
		}
		group.Go(func() error {
			prepared[index] = s.prepareTarget(ctx, runID, sourceRevision, catalog, brief)
			return nil
		})
	}
```

- [ ] **Step 4: Run the test and the package suite**

Run: `go test ./internal/pagewiki/ -run TestGivenTwoUpdateBriefsForSamePageThenSecondFailsWithoutAnEditCall -v -timeout 30s`
Expected: PASS

Run: `go test ./internal/pagewiki/... -timeout 120s`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/service.go internal/pagewiki/duplicate_target_acceptance_test.go
git commit -m "feat(pagewiki): fail duplicate update targets before the edit call

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Debounced async tree reindex

**Files:**
- Modify: `internal/pagewiki/service.go` (dirty channel + debounce goroutine + flush; delete `maybeReindexTree`)
- Modify: `internal/pagewiki/tree_reindex_acceptance_test.go` (inject-then-flush; new coalescing test)
- Create: `internal/pagewiki/tree_maintenance_test.go` (internal, package `pagewiki`: debounce timing)
- Modify: `main.go:205` (start the maintenance goroutine)

**Interfaces:**
- Consumes: `Service.treeIndexer`, `catalogChanged`, `Repository.PageCatalog/TopicTree/ReplaceTopicTree`, `TreeIndexer.Index`.
- Produces: exported `func (s *Service) StartTreeMaintenance(ctx context.Context)` and `func (s *Service) FlushTreeReindex(ctx context.Context)`; unexported `markTreeDirty()`, `debounceThenReindex(ctx)`, `reindexTree(ctx)`; `Service` fields `treeDirty chan struct{}`, `treeQuiet time.Duration`, `treeMaxWait time.Duration` (fields exist so internal tests can shrink the windows; production values come from the constants).

- [ ] **Step 1: Update the acceptance tests to the flush contract and add the coalescing test**

In `internal/pagewiki/tree_reindex_acceptance_test.go`:

In `TestSuccessfulRunReplacesTree` and `TestIndexerFailureKeepsRunAndOldTree`, immediately after the `service.InjectSession(...)` call and its `NoError`/run-status assertions, insert:

```go
	service.FlushTreeReindex(context.Background())
```

(the `indexer.calls` / tree assertions that follow stay unchanged — they now count flushed reindexes).

In `TestSourceOnlyRunSkipsIndexer`, after the run-status assertion and before `s.Require().Equal(0, indexer.calls)`, insert:

```go
	service.FlushTreeReindex(context.Background())
```

(a source-only run never marks the tree dirty, so the flush must be a no-op).

Add the coalescing test to the same file:

```go
func (s *treeReindexSuite) TestTwoRunsThenOneFlushReindexOnce() {
	planner, editor, request := s.createBriefAndEditor()
	indexer := &recordingIndexer{}
	service := pagewiki.NewService(s.repository, planner, editor, pagewiki.WithTreeIndexer(indexer, nil))

	first, err := service.InjectSession(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, first.Run.Status)

	secondRaw := []byte("event-2: The wiki search stays lexical for now.")
	secondText := "The wiki search stays lexical for now."
	secondStart := len("event-2: ")
	secondPlanner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{{
			Key:              "wiki-search",
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     "wiki-search",
			ProposedTitle:    "Wiki Search",
			EvidenceEventIDs: []string{"event-2"},
		}},
	}
	secondEditor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"wiki-search": {
				Slug:    "wiki-search",
				Title:   "Wiki Search",
				Summary: "Wiki search stays lexical in this iteration.",
				Sections: []pagewiki.SectionDraft{{
					Key:      "retrieval",
					Heading:  "Retrieval",
					Markdown: "The wiki search stays lexical for now.",
				}},
				Citations: []pagewiki.CitationDraft{{
					SectionKey: "retrieval",
					ExactText:  "stays lexical",
					Evidence: []pagewiki.EvidenceQuoteDraft{{
						EventID:   "event-2",
						ExactText: secondText,
					}},
				}},
			},
		},
	}
	secondService := pagewiki.NewService(
		s.repository, secondPlanner, secondEditor, pagewiki.WithTreeIndexer(indexer, nil),
	)
	second, err := secondService.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-2",
		IdempotencyKey: "session-2-injection",
		Raw:            secondRaw,
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-2",
			StartByte: secondStart,
			EndByte:   secondStart + len(secondText),
		}},
	})
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, second.Run.Status)

	// Neither run reindexed inline; each service instance carries its own
	// dirty flag, so flushing both yields exactly one reindex per dirty
	// service — the coalescing win is per service instance.
	s.Require().Equal(0, indexer.calls)
	secondService.FlushTreeReindex(context.Background())
	s.Require().Equal(1, indexer.calls)
	secondService.FlushTreeReindex(context.Background())
	s.Require().Equal(1, indexer.calls)
	service.FlushTreeReindex(context.Background())
	s.Require().Equal(2, indexer.calls)
}
```

- [ ] **Step 2: Write the failing internal debounce test**

Create `internal/pagewiki/tree_maintenance_test.go` (package `pagewiki` — it may not import `internal/pagewiki/memory`, that would cycle; it uses a stub repository instead):

```go
package pagewiki

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingTreeIndexer struct {
	calls atomic.Int32
}

func (c *countingTreeIndexer) Index(
	context.Context,
	TreeIndexInput,
) (TopicTree, error) {
	c.calls.Add(1)
	return TopicTree{}, nil
}

// stubTreeRepository implements only what reindexTree touches; the embedded
// nil interface panics loudly if anything else is called.
type stubTreeRepository struct {
	Repository
	replaced atomic.Int32
}

func (r *stubTreeRepository) PageCatalog(context.Context) (PageCatalog, error) {
	return PageCatalog{}, nil
}

func (r *stubTreeRepository) TopicTree(context.Context) (TopicTree, error) {
	return TopicTree{}, nil
}

func (r *stubTreeRepository) ReplaceTopicTree(context.Context, TopicTree) error {
	r.replaced.Add(1)
	return nil
}

func TestTreeMaintenanceCoalescesDirtyMarksIntoOneReindex(t *testing.T) {
	indexer := &countingTreeIndexer{}
	repository := &stubTreeRepository{}
	service := NewService(
		repository, ScriptedPlanner{}, ScriptedEditor{},
		WithTreeIndexer(indexer, nil),
	)
	service.treeQuiet = 20 * time.Millisecond
	service.treeMaxWait = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartTreeMaintenance(ctx)

	service.markTreeDirty()
	time.Sleep(5 * time.Millisecond)
	service.markTreeDirty()

	require.Eventually(t, func() bool {
		return indexer.calls.Load() == 1
	}, time.Second, 5*time.Millisecond, "debounced reindex never ran")

	// Past another full quiet window: no further marks, no further reindex.
	time.Sleep(60 * time.Millisecond)
	require.EqualValues(t, 1, indexer.calls.Load())
	require.EqualValues(t, 1, repository.replaced.Load())
}

func TestFlushTreeReindexRunsOnlyWhenDirty(t *testing.T) {
	indexer := &countingTreeIndexer{}
	repository := &stubTreeRepository{}
	service := NewService(
		repository, ScriptedPlanner{}, ScriptedEditor{},
		WithTreeIndexer(indexer, nil),
	)

	service.FlushTreeReindex(context.Background())
	require.EqualValues(t, 0, indexer.calls.Load())

	service.markTreeDirty()
	service.FlushTreeReindex(context.Background())
	require.EqualValues(t, 1, indexer.calls.Load())

	service.FlushTreeReindex(context.Background())
	require.EqualValues(t, 1, indexer.calls.Load())
}

func TestStartTreeMaintenanceWithoutIndexerIsANoOp(t *testing.T) {
	service := NewService(&stubTreeRepository{}, ScriptedPlanner{}, ScriptedEditor{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartTreeMaintenance(ctx) // must not panic or spin
	service.FlushTreeReindex(ctx)     // must not panic
}
```

- [ ] **Step 3: Run both test files to verify they fail**

Run: `go test ./internal/pagewiki/ -run 'TestTreeMaintenance|TestFlushTreeReindex|TestStartTreeMaintenance|TreeReindex' -v -timeout 60s`
Expected: FAIL to compile (`service.FlushTreeReindex`, `markTreeDirty`, `treeQuiet` undefined) — that is the failing state for this step.

- [ ] **Step 4: Implement the debounced maintenance loop**

In `internal/pagewiki/service.go`:

Add `"time"` to the import block. Add constants:

```go
	treeReindexQuiet   = 5 * time.Second
	treeReindexMaxWait = 60 * time.Second
```

Add fields to `Service`:

```go
	treeDirty   chan struct{}
	treeQuiet   time.Duration
	treeMaxWait time.Duration
```

In `NewService`, before the options loop:

```go
	service.treeDirty = make(chan struct{}, 1)
	service.treeQuiet = treeReindexQuiet
	service.treeMaxWait = treeReindexMaxWait
```

In `InjectSession`, replace `s.maybeReindexTree(ctx, briefs, run.Targets)` with:

```go
	if s.treeIndexer != nil && catalogChanged(briefs, run.Targets) {
		s.markTreeDirty()
	}
```

Delete `maybeReindexTree` and add:

```go
// markTreeDirty records that the Page catalog changed since the last topic
// tree rebuild. The channel is buffered(1): an already-pending mark absorbs
// further marks, and the eventual rebuild reads the latest catalog anyway.
func (s *Service) markTreeDirty() {
	select {
	case s.treeDirty <- struct{}{}:
	default:
	}
}

// StartTreeMaintenance runs topic-tree rebuilds in the background: a dirty
// mark opens a debounce window (treeQuiet of silence, capped at treeMaxWait)
// and then rebuilds once. This keeps the rebuild off the ingest critical
// path and coalesces a replay's many runs into one rebuild. A mark pending
// when ctx is cancelled is dropped; the tree is a derived view and the next
// catalog change re-marks it.
func (s *Service) StartTreeMaintenance(ctx context.Context) {
	if s.treeIndexer == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.treeDirty:
				s.debounceThenReindex(ctx)
			}
		}
	}()
}

func (s *Service) debounceThenReindex(ctx context.Context) {
	quiet := time.NewTimer(s.treeQuiet)
	defer quiet.Stop()
	deadline := time.NewTimer(s.treeMaxWait)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.treeDirty:
			if !quiet.Stop() {
				<-quiet.C
			}
			quiet.Reset(s.treeQuiet)
		case <-quiet.C:
			s.reindexTree(ctx)
			return
		case <-deadline.C:
			s.reindexTree(ctx)
			return
		}
	}
}

// FlushTreeReindex rebuilds the topic tree now if a dirty mark is pending.
// Tests and the rebuild flow use it to make the async contract synchronous.
func (s *Service) FlushTreeReindex(ctx context.Context) {
	if s.treeIndexer == nil {
		return
	}
	select {
	case <-s.treeDirty:
		s.reindexTree(ctx)
	default:
	}
}

// reindexTree is best-effort: any failure is logged and swallowed so a
// reindex problem never fails or delays ingestion, and the previously
// stored TopicTree stays in place.
func (s *Service) reindexTree(ctx context.Context) {
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
```

In `main.go`, immediately before `controller.Start(ctx)` (`main.go:205`), add:

```go
	service.StartTreeMaintenance(ctx)
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/pagewiki/ -run 'TestTreeMaintenance|TestFlushTreeReindex|TestStartTreeMaintenance|TreeReindex' -v -timeout 60s`
Expected: all PASS

Run: `go build ./... && go vet ./internal/pagewiki/... && go test ./internal/pagewiki/... -timeout 120s`
Expected: all PASS. Also run `go test ./internal/pagewiki/ -race -run 'TestTreeMaintenance|TestFlushTreeReindex' -timeout 60s` — PASS, no races.

- [ ] **Step 6: Commit**

```bash
git add internal/pagewiki/service.go internal/pagewiki/tree_maintenance_test.go internal/pagewiki/tree_reindex_acceptance_test.go main.go
git commit -m "feat(pagewiki): debounced background topic-tree reindex off the ingest path

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Per-stream failure backoff in the session consumer

**Files:**
- Modify: `internal/pagewiki/sessionconsumer/consumer.go`
- Create: `internal/pagewiki/sessionconsumer/backoff_test.go` (internal, package `sessionconsumer`)

**Interfaces:**
- Consumes: `Controller` fields (`store`, `injector`, `logger`, `interval`, `mu`), `consume`, `Stream`.
- Produces: unexported `type failureRecord struct { head int64; attempts int; nextRetryAt time.Time }`, `Controller` fields `failures map[string]failureRecord` and `now func() time.Time`, helpers `streamKey(Stream) string`, `(c *Controller) backedOff(Stream, time.Time) bool`, `(c *Controller) recordFailure(Stream) failureRecord`, `(c *Controller) backoffDelay(attempts int) time.Duration`, const `failureBackoffCap = 10 * time.Minute`.

- [ ] **Step 1: Write the failing internal tests**

Create `internal/pagewiki/sessionconsumer/backoff_test.go`:

```go
package sessionconsumer

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/require"
)

type backoffStore struct {
	streams []Stream
	events  []session.SessionEvent
}

func (s *backoffStore) AutoInjectEnabled(context.Context, string) (bool, error) { return true, nil }
func (s *backoffStore) SetAutoInjectEnabled(context.Context, string, bool) error { return nil }
func (s *backoffStore) PendingStreams(context.Context) ([]Stream, error) {
	return append([]Stream(nil), s.streams...), nil
}
func (s *backoffStore) StreamsBySessionID(
	_ context.Context, scopeID string, sessionID string,
) ([]Stream, error) {
	result := make([]Stream, 0)
	for _, stream := range s.streams {
		if stream.ScopeID == scopeID && stream.Actor.SessionID == sessionID {
			result = append(result, stream)
		}
	}
	return result, nil
}
func (s *backoffStore) SessionEvents(context.Context, Stream) ([]session.SessionEvent, error) {
	return append([]session.SessionEvent(nil), s.events...), nil
}
func (s *backoffStore) AdvanceCursor(context.Context, Stream) error { return nil }

type flakyInjector struct {
	err   error
	calls int
}

func (i *flakyInjector) InjectSession(
	context.Context, pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	i.calls++
	if i.err != nil {
		return pagewiki.InjectResult{}, i.err
	}
	return pagewiki.InjectResult{
		Run: pagewiki.MaintenanceRun{Status: pagewiki.RunStatusSucceeded},
	}, nil
}

type noopRebuilder struct{}

func (noopRebuilder) RebuildPageWiki(context.Context, string, string, string) error { return nil }

func newBackoffFixture(t *testing.T) (*backoffStore, *flakyInjector, *Controller, *time.Time) {
	t.Helper()
	store := &backoffStore{
		streams: []Stream{{
			ScopeID: "local-team",
			Actor:   session.Actor{AgentID: "agent-1", SessionID: "session-1"},
			Head:    2,
		}},
		events: []session.SessionEvent{
			{ID: "event-1", Sequence: 1, Type: "assistant", Content: "hello"},
		},
	}
	injector := &flakyInjector{err: errors.New("planner down")}
	controller, err := New(store, injector, noopRebuilder{}, slog.New(slog.DiscardHandler), time.Second)
	require.NoError(t, err)
	now := time.Unix(1_000_000, 0)
	controller.now = func() time.Time { return now }
	return store, injector, controller, &now
}

func TestScanSkipsFailingStreamUntilBackoffExpires(t *testing.T) {
	_, injector, controller, now := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx) // attempt 1: delay = 1s << 1 = 2s
	require.Equal(t, 1, injector.calls)

	controller.scan(ctx) // inside the 2s window
	require.Equal(t, 1, injector.calls)

	*now = now.Add(3 * time.Second)
	controller.scan(ctx) // attempt 2: delay = 1s << 2 = 4s
	require.Equal(t, 2, injector.calls)

	*now = now.Add(3 * time.Second)
	controller.scan(ctx) // still inside the 4s window
	require.Equal(t, 2, injector.calls)

	*now = now.Add(2 * time.Second)
	controller.scan(ctx)
	require.Equal(t, 3, injector.calls)
}

func TestHeadAdvanceResetsBackoff(t *testing.T) {
	store, injector, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx)
	require.Equal(t, 1, injector.calls)

	store.streams[0].Head = 3 // new events arrived
	controller.scan(ctx)      // no clock movement needed
	require.Equal(t, 2, injector.calls)
}

func TestSuccessClearsBackoffAndAttemptsRestart(t *testing.T) {
	_, injector, controller, now := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx)
	controller.scan(ctx)
	require.Equal(t, 1, injector.calls)

	injector.err = nil
	*now = now.Add(time.Hour)
	controller.scan(ctx)
	require.Equal(t, 2, injector.calls)
	require.Empty(t, controller.failures)

	injector.err = errors.New("planner down again")
	controller.scan(ctx)
	require.Equal(t, 3, injector.calls)
	record := controller.failures["local-team/agent-1/session-1"]
	require.Equal(t, 1, record.attempts, "attempts must restart after a success")
}

func TestManualInjectBypassesAndClearsBackoff(t *testing.T) {
	_, injector, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx) // stream now backed off
	require.Equal(t, 1, injector.calls)

	injector.err = nil
	_, err := controller.InjectSession(ctx, "local-team", "session-1")
	require.NoError(t, err)
	require.Equal(t, 2, injector.calls, "manual inject must ignore the backoff window")
	require.Empty(t, controller.failures)
}

func TestRebuildClearsAllBackoff(t *testing.T) {
	_, _, controller, _ := newBackoffFixture(t)
	ctx := context.Background()

	controller.scan(ctx)
	require.NotEmpty(t, controller.failures)

	_, err := controller.Rebuild(ctx, "local-team")
	require.NoError(t, err)
	require.Empty(t, controller.failures)
}

func TestBackoffDelayIsCapped(t *testing.T) {
	controller := &Controller{interval: 2 * time.Second}
	require.Equal(t, 4*time.Second, controller.backoffDelay(1))
	require.Equal(t, 8*time.Second, controller.backoffDelay(2))
	require.Equal(t, failureBackoffCap, controller.backoffDelay(10))
	require.Equal(t, failureBackoffCap, controller.backoffDelay(64))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/pagewiki/sessionconsumer/ -run 'Backoff|TestScanSkips|TestHeadAdvance|TestSuccessClears|TestManualInject|TestRebuildClears' -v -timeout 30s`
Expected: FAIL to compile (`controller.now`, `controller.failures`, `backoffDelay` undefined).

- [ ] **Step 3: Implement the backoff**

In `internal/pagewiki/sessionconsumer/consumer.go`:

Add:

```go
const failureBackoffCap = 10 * time.Minute

// failureRecord tracks one stream's consecutive injection failures at a
// given head. It lives only in process memory: a restart resets all backoff,
// which is the desired behavior on a single-team workstation deployment.
type failureRecord struct {
	head        int64
	attempts    int
	nextRetryAt time.Time
}

func streamKey(stream Stream) string {
	return stream.ScopeID + "/" + stream.Actor.AgentID + "/" + stream.Actor.SessionID
}
```

Extend the `Controller` struct with:

```go
	failures map[string]failureRecord
	now      func() time.Time
```

and initialize both in `New` (inside the returned literal):

```go
		failures: make(map[string]failureRecord),
		now:      time.Now,
```

Replace the loop body in `scan` (currently `consumer.go:171-180`):

```go
	now := c.now()
	for _, stream := range streams {
		if c.backedOff(stream, now) {
			continue
		}
		if err := c.consume(ctx, stream); err != nil {
			record := c.recordFailure(stream)
			c.logger.ErrorContext(ctx, "Page Wiki session injection failed",
				"scope_id", stream.ScopeID,
				"agent_id", stream.Actor.AgentID,
				"session_id", stream.Actor.SessionID,
				"attempts", record.attempts,
				"next_retry_at", record.nextRetryAt,
				"error", err,
			)
			continue
		}
		delete(c.failures, streamKey(stream))
	}
```

Add the helpers:

```go
// backedOff reports whether the stream is still inside its retry window. A
// head advance (new session events) always clears the way immediately.
func (c *Controller) backedOff(stream Stream, now time.Time) bool {
	record, found := c.failures[streamKey(stream)]
	if !found || record.head != stream.Head {
		return false
	}
	return now.Before(record.nextRetryAt)
}

func (c *Controller) recordFailure(stream Stream) failureRecord {
	key := streamKey(stream)
	record := c.failures[key]
	if record.head != stream.Head {
		record = failureRecord{head: stream.Head}
	}
	record.attempts++
	record.nextRetryAt = c.now().Add(c.backoffDelay(record.attempts))
	c.failures[key] = record
	return record
}

func (c *Controller) backoffDelay(attempts int) time.Duration {
	if attempts >= 16 {
		return failureBackoffCap
	}
	delay := c.interval << attempts
	if delay <= 0 || delay > failureBackoffCap {
		return failureBackoffCap
	}
	return delay
}
```

In `InjectSession`, inside the locked loop (currently `consumer.go:155-159`), clear the record before consuming so an explicit user request always tries now:

```go
	for _, stream := range streams {
		delete(c.failures, streamKey(stream))
		if err := c.consume(ctx, stream); err != nil {
			return InjectResult{}, err
		}
	}
```

In `Rebuild`, clear the whole map inside the locked section (currently `consumer.go:125-127`):

```go
	c.mu.Lock()
	err := c.rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion)
	if err == nil {
		c.failures = make(map[string]failureRecord)
	}
	c.mu.Unlock()
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/pagewiki/sessionconsumer/ -v -timeout 60s`
Expected: all PASS (new backoff tests plus the existing external consumer suite — the existing suite constructs streams with `UserID` set; `streamKey` ignores `UserID` by design, matching the `(scope, agent, session)` stream identity).

Run: `go build ./... && go vet ./internal/pagewiki/... && go test ./internal/pagewiki/... -timeout 120s`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/sessionconsumer/consumer.go internal/pagewiki/sessionconsumer/backoff_test.go
git commit -m "feat(pagewiki): exponential per-stream backoff for failing session injections

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Final verification (after all tasks)

- [ ] Run: `go build ./... && go vet ./internal/pagewiki/... && go test ./internal/pagewiki/... -race -timeout 300s`
- [ ] Confirm `git log --oneline` shows the four feature commits on top of the spec commit.
