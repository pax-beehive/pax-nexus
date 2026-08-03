# Async Wiki Rebuild — Design

Date: 2026-08-02
Status: approved

## Problem

`POST /v1/wiki/rebuild` runs synchronously inside the request goroutine and
must acquire the session consumer's mutex `c.mu`
(`internal/pagewiki/sessionconsumer/consumer.go`). The consumer's `scan()`
holds that same mutex for an entire scan pass — every pending stream, each
including 1–3 minutes of LLM planner/editor calls. When a rebuild is requested
while the consumer is injecting, the request blocks past nginx's
`proxy_read_timeout` (60s), nginx returns 504, the hertz request context is
canceled, and the rebuild transaction rolls back. Net effect: the user sees an
error and nothing happens.

Observed on the workstation deployment on 2026-08-02 (see incident postmortem
in team memory: `pagewiki-rebuild-lock-504`).

## Decision

Make rebuild asynchronous. The request path only records intent and returns
202 Accepted; the consumer loop executes the rebuild. Rebuild progress and
failure are exposed through the existing ingestion status endpoint, which the
frontend already polls every 5 seconds.

Rejected alternatives:

- **Finer-grained locking / RWMutex** — mutual exclusion between rebuild and
  injection is semantically required (an in-flight injection finishing after
  the wipe would write stale pages and advance cursors incorrectly). Changing
  the lock type does not shorten the wait.
- **Raising nginx `proxy_read_timeout`** — treats the symptom; with a deep
  backlog even 300s is not enough. May still be applied operationally, out of
  scope here.

## 1. Controller state machine (`internal/pagewiki/sessionconsumer/consumer.go`)

New rebuild state on `Controller`, guarded by a **separate small mutex
`stateMu`** — never by `c.mu`, so status reads never wait on a scan:

- `rebuildState`: `idle | queued | running | failed`
- `rebuildSince time.Time` — the `since` recorded at queue time
- `lastRebuildError string` — set on failure
- `lastRebuildFinishedAt *time.Time` — set on success

Behavior:

- **`Rebuild(ctx, scopeID, since)`** (request path): validate scope; under
  `stateMu`, if state is `queued` or `running`, return the current status
  unchanged (idempotent merge — a later request's `since` is discarded);
  otherwise set `queued`, record `since`, signal the trigger channel, and
  return immediately. The request path no longer touches `c.mu` or the
  database.
- **Consumer loop**: on every wakeup (trigger or ticker), run
  `maybeRebuild(ctx)` before `scan(ctx)`. `maybeRebuild` checks for a queued
  rebuild; if present it sets `running`, acquires `c.mu`, and calls
  `rebuilder.RebuildPageWiki` **with the loop's context** (not a request
  context). On success: state → `idle`, record `lastRebuildFinishedAt`, clear
  the `failures` backoff map. On error: state → `failed` with the error
  message. The loop then proceeds to `scan` in the same wakeup, so injection
  resumes right after a successful rebuild.
- **Yielding**: `scan()` checks for a queued rebuild after finishing each
  stream and ends the pass early if one is waiting. This bounds the rebuild's
  wait to one in-flight session injection (~1–3 min) instead of the whole
  backlog. Remaining backlog is picked up by the next tick.
- **`Status()`** additionally returns a snapshot of the rebuild state fields
  (read under `stateMu`).
- **`InjectSession`** is unchanged this round. It has the same theoretical
  504 exposure, but it processes a single named session, which is bounded and
  operator-initiated; revisit only if it bites in practice.
- A new rebuild request is allowed from the `failed` state (it transitions
  `failed` → `queued`; `rebuild_error` is reported only while the state is
  `failed`, so queuing hides it).

State is process-memory only. A restart resets to `idle`; this matches the
existing `failureRecord` policy for the single-team workstation deployment.

## 2. HTTP layer

- `idl/team_memory.thrift`: add to **both** `WikiIngestionStatusResponse` and
  `RebuildWikiResponse`:
  - `optional string rebuild_state` (`idle | queued | running | failed`)
  - `optional string rebuild_error` (present only when `failed`)
  - `optional string last_rebuild_finished_at` (RFC3339, present after a
    successful rebuild)
- Regenerate the thriftgo models with the repo's existing codegen workflow.
- `RebuildWiki` handler returns **202 Accepted** with the extended response.
  Synchronous validation errors (bad `since`, unauthorized, non-owner) keep
  their current 4xx behavior.

## 3. Frontend (`web/`)

- `src/api/wiki.ts`: extend `WikiIngestionStatus` with the three rebuild
  fields; `rebuildWiki` (in `src/api/actions.ts`) keeps its shape.
- `WikiRebuildDialog`: on 202, close the dialog immediately.
- `WikiIngestionCard`: render rebuild state from the polled status — a
  "Rebuild queued / running" badge, and the failure message when `failed`.
  Disable the "Reset & rebuild" button while `queued` or `running`.
- No new polling: `WikiStatusPage` already polls
  `GET /v1/wiki/ingestion` every 5s via `usePolling`.

## 4. Error handling and edge cases

- Background failure surfaces as `failed` + message in status and stays until
  the next rebuild request transitions the state away from `failed`.
- The rebuild runs on the consumer loop's context, so client disconnects
  cannot cancel it mid-transaction.
- Concurrent `Rebuild` calls are serialized by `stateMu`; only the first
  enqueues.
- Process restart drops queued/failed state (acceptable; documented above).

## 5. Testing

- `consumer_test.go`:
  - `Rebuild` returns immediately while another goroutine holds `c.mu`.
  - Idempotent merge: second `Rebuild` while queued/running is a no-op.
  - `scan` yields between streams when a rebuild is queued.
  - Background failure appears in `Status()`; a later rebuild clears it.
  - Successful rebuild clears the failures map and passes the recorded
    `since` to the rebuilder.
- Handler tests: `RebuildWiki` returns 202 with the new fields; validation
  errors still 4xx.
- `web/tests/wiki-status.dom.test.tsx`: badge rendering and button disabling
  for queued/running/failed states.
