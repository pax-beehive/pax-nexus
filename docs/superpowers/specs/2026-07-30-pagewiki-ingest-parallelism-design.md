# PageWiki ingest parallelism design

Date: 2026-07-30
Status: approved by user (option A + failure backoff: in-run parallel
edits, debounced async tree reindex, per-stream retry backoff; no
cross-stream parallelism yet)

## Problem

A single ingest run is a fully serial chain of LLM calls, and the
session consumer serializes everything behind one mutex:

1. `sessionconsumer.Controller.scan` holds `c.mu` for the whole scan
   round, so streams are processed strictly one at a time and manual
   inject / rebuild requests queue behind LLM work.
2. Inside `Service.InjectSession` a run is `1 (plan) + N (edit, one per
   brief, up to 8) + 1 (tree reindex)` sequential LLM calls
   (`service.go:121-129`).
3. The topic-tree reindex runs synchronously at the end of every
   catalog-changing run. During a reset-rebuild replay of N sessions
   that is N full-tree LLM rebuilds where only the last one matters —
   the tree is a derived, rebuildable view by prior decision
   (2026-07-29 wiki semantic indexer design).
4. `consume` only advances the cursor on `RunStatusSucceeded`
   (`consumer.go:196-200`) and scan re-runs every 2 seconds, so a
   permanently failing stream re-burns its full plan+edit LLM cost
   every 2 seconds forever, and occupies the serial scan channel each
   round.

Correctness does not depend on the coarse serialization. Conflict
protection is already optimistic and lives at the edges: update briefs
carry `ExpectedBaseRevisionID` checked in `resolvePage`
(`service.go:314`), and `PublishPage` re-validates both slug uniqueness
and base-revision freshness (`memory/repository.go:229-238`). The LLM
calls themselves never needed to be inside any lock; only the catalog
snapshot read and the publish commit do.

Reset-rebuild replay is the scenario that hurts today; the same changes
also cut normal single-session inject latency.

## Non-goals

- Cross-stream parallelism (worker pool over pending streams). Deferred
  until A is measured; the CAS/slug safety net makes it a later,
  separable step.
- Speculative execution with in-order commit. YAGNI.
- Any change to identity, idempotency, cursor, or publish-order
  semantics.

## Design

### A. In-run parallel edits (`service.go`)

Split `processTarget` into two phases:

- **Prepare (parallel):** brief validation, evidence validation,
  `resolvePage`, and `editor.Edit`. Runs under
  `errgroup.SetLimit(editConcurrency)` with `editConcurrency = 4`
  (constant; planner emits at most 8 briefs). Each brief writes its
  draft or error into its own slot — one target's failure never
  cancels sibling edits, preserving today's per-target failure
  isolation. `source_only` and `ambiguous` briefs resolve in this
  phase without an LLM call.
- **Commit (serial, brief order):** `buildPublication` (including
  `buildLinks` repository reads), the revision-equivalence short
  circuit, and `PublishPage`. Publish order is identical to today, so
  links from a later brief to a page created earlier in the same run
  still validate.

Duplicate-target guard: before the prepare phase, update briefs are
deduped by their non-empty `TargetPageID` (create briefs, whose target
is empty, are never affected). The second update brief aimed at the same page fails
immediately with a publication conflict instead of burning an edit call
and then failing the base-revision CAS at publish — same final outcome
as today, minus one LLM call. `PublishPage`'s "base is stale" check
remains the authoritative backstop.

### B. Debounced async tree reindex (`service.go`, `main.go`)

- `InjectSession` no longer calls the indexer inline. When
  `catalogChanged` is true it does a non-blocking send on a
  buffered(1) dirty channel.
- New `Service.StartTreeMaintenance(ctx)` (called from `main.go` when a
  tree indexer is configured) runs one background goroutine: on a dirty
  signal it opens a debounce window — **5s of quiet** resets on each
  new signal, capped at **60s max wait** — then executes the current
  `maybeReindexTree` body (load catalog, load current tree, index,
  `ReplaceTopicTree`). Failures are logged and swallowed; the previous
  tree stays in place, exactly as today.
- New `Service.FlushTreeReindex(ctx)`: synchronously runs the reindex
  now if dirty. Used by tests (the existing
  `tree_reindex_acceptance_test.go` switches to inject-then-flush) and
  available to the rebuild flow.
- Without a configured indexer nothing starts, matching today's
  optionality.

Accepted limitation: a process exit inside the debounce window drops
that pending rebuild; the tree stays stale until the next
catalog-changing ingest marks it dirty again. Acceptable because the
tree is a derived, rebuildable view.

### C. Per-stream failure backoff (`sessionconsumer/consumer.go`)

The Controller keeps an in-memory map keyed by
`scope/agent/session` holding `{failedHead, attempts, nextRetryAt}`:

- `scan` skips a stream when `stream.Head == failedHead` and
  `now < nextRetryAt`.
- On consume failure: `attempts++`, delay `= interval × 2^attempts`,
  capped at 10 minutes; failures log at warn with the attempt count.
- On success, or when the stream's head advances (new session events
  arrived): the entry is deleted and the stream is processed
  immediately.
- Manual `InjectSession` (API) bypasses the backoff and clears the
  entry — an explicit user request should always try now. `Rebuild`
  clears the whole map.
- The map is process-memory only; a restart resets all backoff, which
  is the desired behavior on a single-team workstation deployment.
- The Controller gains `now func() time.Time` (defaulting to
  `time.Now`) for deterministic tests.

## Invariants preserved

- Publish order equals brief order; run/target status semantics,
  stable IDs, and idempotency keys are untouched.
- Cursor advances only on `RunStatusSucceeded`, unchanged.
- Per-target failure isolation is unchanged (now guaranteed by
  slot-per-brief error capture instead of loop order).
- Repository conflict checks (`ExpectedBaseRevisionID` CAS at resolve,
  slug uniqueness and base-revision freshness at publish) are the
  authoritative conflict layer, unchanged.

## Expected effect

- Normal ingest: critical-path LLM latency per run drops from
  `1 + N + 1` sequential calls to `1 + 1` (edits run as one parallel
  batch; tree rebuild leaves the critical path).
- Reset-rebuild replay: additionally coalesces N tree rebuilds into
  one, and failing streams stop occupying the serial scan channel
  every 2 seconds.

## Testing

- All existing acceptance tests keep passing without semantic changes;
  `tree_reindex_acceptance_test.go` switches to inject-then-flush.
- New service tests: two scripted edits observed genuinely concurrent
  (barrier editor); one target's edit failure leaves siblings
  succeeded; duplicate-target briefs invoke the editor exactly once
  and fail the duplicate with a conflict.
- New reindex tests: two catalog-changing injects followed by one
  flush call the indexer exactly once; indexer failure keeps the old
  tree.
- New consumer tests: a failing stream is skipped inside its backoff
  window and retried after it; head advancement resets backoff; manual
  inject bypasses and clears backoff.

## Tunables (constants, no new env vars)

- `editConcurrency = 4`
- reindex debounce quiet period 5s, max wait 60s
- backoff base = consumer interval (2s), factor 2, cap 10 minutes
