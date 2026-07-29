# Extraction compaction harness protection design

Date: 2026-07-28
Status: approved by user (truncation fallback accepted; no model switch)

## Problem

The rolling extractor's episode compaction depends on the extraction model
returning a valid checkpoint JSON. `deepseek-v4-flash` occasionally returns an
empty or invalid JSON body. Two stacked defects turn that transient flake into
a per-episode death loop (observed: 27 accumulated failures on one episode):

1. `classifyProviderFailure` (`internal/teamnote/extractor/execution.go`)
   classifies `ErrInvalidModelResponse` as **not retryable**, so a single
   empty-JSON response fails the whole provider call without using the
   remaining `MaxAttempts` in-call retries.
2. At the hard token limit (`episode.EstimatedTokens + appendTokens >=
   CompactTokens`), `prepareEpisode` (`internal/teamnote/extractor/rolling.go`)
   makes compaction mandatory: when it fails, the whole slice extraction
   errors, the session consumer retries later, the episode is still over the
   limit, compaction runs and fails again — indefinitely. There is no failure
   counter, no degradation path, and the episode never advances.

The user explicitly does not want to switch the extractor to
`deepseek-v4-pro`; protection must live in the harness.

## Goal

An episode can never loop on compaction failure. Transient invalid responses
get retried; persistent failure degrades deterministically and extraction
proceeds. No new required configuration; soft-path (background compaction)
behavior is unchanged when compaction succeeds.

## Design

### Layer 1 — invalid model responses become retryable

`classifyProviderFailure` returns `retryable = true` for
`ErrInvalidModelResponse` (class stays `ProviderFailureInvalidResponse`). This
applies to all call types (primary, summary, compaction): an invalid body is a
transient model flake in practice, and the retry is already bounded by
`ExecutionPolicy.MaxAttempts` and paced by `RetryBackoff`.
`ErrProviderResponseTooLarge` stays non-retryable (deterministic outcome).
Layer 1 requires `ExecutionPolicy.MaxAttempts >= 2` (env
`TEAM_MEMORY_EXTRACTION_PROVIDER_MAX_ATTEMPTS`); with the historical default
of 1 the retry never fires and only Layer 2 protects.

### Layer 2 — deterministic truncation fallback at the hard limit

In `prepareEpisode`, the current hard-limit failure path (`computeCompaction`
error → return error) instead degrades:

1. Record the failure on the episode checkpoint (see counters below).
2. Truncate deterministically: drop episode messages from the OLDEST end until
   `estimateEpisodeTokens(checkpoint, remaining) < CompactStartTokens` or only
   one message remains. The checkpoint (which carries the knowledge distilled
   from earlier messages) and the newest tail are preserved.
3. Log a warning (`slog`) with the episode key, dropped message count, and the
   compaction error.
4. Return success (zero usage) so `advanceEpisodeAttempt` proceeds; the
   episode save that follows persists both the truncation and the counters.

The soft path (below hard limit) keeps its current behavior — a failed
background flight is dropped and extraction proceeds uncompacted — but now
also increments the failure counters when the flight's failure is consumed.

### Checkpoint counters

`Checkpoint` (`internal/teamnote/extractor/episode.go`) gains three fields,
mirroring the existing `Summary*` bookkeeping:

```go
CompactionFailures    int    `json:"compaction_failures,omitempty"`
CompactionTruncations int    `json:"compaction_truncations,omitempty"`
CompactionLastError   string `json:"compaction_last_error,omitempty"`
```

`applyCompaction` must carry these over from the pre-compaction checkpoint
exactly as it already carries `SourceCursors` and the `Summary*` fields —
otherwise a later successful compaction would erase the history.

`CompactionLastError` is truncated to 500 bytes to bound checkpoint growth.

## Non-goals

- No parked/halted episode state (rejected in favor of truncation).
- No configuration switch for the fallback (always on).
- No changes to the summary path, primary extraction admission, or the
  session consumer's retry semantics.

## Testing

- `classifyProviderFailure`: invalid-response now `(ProviderFailureInvalidResponse, true)`;
  a provider that returns empty JSON once then a valid checkpoint succeeds
  within one `executeProvider` call.
- Hard-limit fallback: a scripted provider that always returns invalid JSON on
  compaction lets `advanceEpisode` succeed; the saved episode is truncated
  below `CompactStartTokens`, keeps the checkpoint and newest message, and
  carries `CompactionFailures >= 1`, `CompactionTruncations == 1`, and a
  non-empty `CompactionLastError`.
- Loop regression: two consecutive `advanceEpisode` calls with an
  always-failing compaction provider both succeed (no accumulating failure
  loop).
- Carry-over: a later successful compaction preserves the counters.
