# Compaction Harness Protection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop episode compaction failures from dead-looping extraction: retry invalid model responses in-call, and degrade to deterministic truncation at the hard token limit.

**Architecture:** Two independent layers in `internal/teamnote/extractor`: (1) `classifyProviderFailure` treats `ErrInvalidModelResponse` as retryable so `executeProvider`'s existing `MaxAttempts`/`RetryBackoff` machinery covers transient empty-JSON flakes; (2) `prepareEpisode`'s hard-limit failure path records failure counters on the checkpoint and truncates the oldest episode messages instead of returning an error, so `advanceEpisodeAttempt` always proceeds.

**Tech Stack:** Go, `stretchr/testify/suite` (existing `openAISuite` in `internal/teamnote/extractor/openai_test.go`, package `extractor_test`, scripted via `roundTripFunc` HTTP transports).

**Spec:** `docs/superpowers/specs/2026-07-28-compaction-harness-protection-design.md`

## Global Constraints

- Branch `feat/compaction-harness-protection` (worktree exists; run everything from its root).
- Run `go test ./internal/teamnote/extractor/` while iterating and `go build ./... && go test ./...` before each task's final commit.
- No new required configuration; no model change; the summary path and the session consumer are untouched.
- Compaction requests are identified in scripted transports by the marker string `KNOWLEDGE CONTEXT CHECKPOINT COMPACTION` in the request body (the compaction prompt contains it).
- Test configs keep retries fast: `ExecutionPolicy: extractor.ExecutionPolicy{MaxAttempts: 2, RetryBackoff: time.Millisecond}`.
- `CompactionLastError` is truncated to 500 bytes.

---

### Task 1: Invalid model responses become retryable

**Files:**
- Modify: `internal/teamnote/extractor/execution.go` (`classifyProviderFailure`, the `ErrInvalidModelResponse` case)
- Test: `internal/teamnote/extractor/openai_test.go` (replace `TestProviderExecutionRecordsInvalidResponseWithoutRetry`)

**Interfaces:**
- Consumes: existing `classifyProviderFailure(attemptCtx context.Context, err error) (ProviderFailureClass, bool)`.
- Produces: `ErrInvalidModelResponse` now classifies as `(ProviderFailureInvalidResponse, true)`. Task 2's fallback tests rely on compaction making exactly `MaxAttempts` provider attempts per logical call before failing.

This deliberately flips behavior pinned by the existing test `TestProviderExecutionRecordsInvalidResponseWithoutRetry` (`openai_test.go:398`) — the spec mandates the flip; replace that test rather than keeping it.

- [ ] **Step 1: Replace the pinned test with the new contract**

In `internal/teamnote/extractor/openai_test.go`, delete `TestProviderExecutionRecordsInvalidResponseWithoutRetry` and add in its place:

```go
func (s *openAISuite) TestProviderExecutionRetriesInvalidResponse() {
	var calls []extractor.ProviderCall
	attempts := atomic.Int64{}
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return response(http.StatusOK, `{"choices":[{"message":{"content":"not-json"}}]}`), nil
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ExecutionPolicy:      extractor.ExecutionPolicy{MaxAttempts: 2, RetryBackoff: time.Millisecond},
		ProviderCallObserver: func(call extractor.ProviderCall) { calls = append(calls, call) },
	})
	s.Require().NoError(err)

	_, err = adapter.Extract(context.Background(), extractorSlice())

	s.Require().NoError(err)
	s.Require().Len(calls, 2)
	s.Equal(extractor.ProviderFailureInvalidResponse, calls[0].FailureClass)
	s.True(calls[0].Retryable)
	s.Empty(calls[1].FailureClass)
}

func (s *openAISuite) TestProviderExecutionStopsRetryingInvalidResponseAtMaxAttempts() {
	var calls []extractor.ProviderCall
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"not-json"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ExecutionPolicy:      extractor.ExecutionPolicy{MaxAttempts: 2, RetryBackoff: time.Millisecond},
		ProviderCallObserver: func(call extractor.ProviderCall) { calls = append(calls, call) },
	})
	s.Require().NoError(err)

	_, err = adapter.Extract(context.Background(), extractorSlice())

	s.Require().Error(err)
	s.Require().Len(calls, 2)
	s.True(calls[0].Retryable)
	s.True(calls[1].Retryable)
}
```

`atomic` is already imported by the neighboring retry test (`sync/atomic`); verify and add the import only if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/teamnote/extractor/ -run 'TestOpenAISuite/TestProviderExecutionRetriesInvalidResponse|TestOpenAISuite/TestProviderExecutionStopsRetryingInvalidResponseAtMaxAttempts' -v`
(Adjust the suite runner name if `go test -run TestOpenAI -list '.*'` shows a different top-level test function; find it with `grep -n "suite.Run" internal/teamnote/extractor/openai_test.go`.)
Expected: FAIL — the first test observes 1 call (no retry), the second observes 1 call.

- [ ] **Step 3: Implement**

In `internal/teamnote/extractor/execution.go`, change the classification:

```go
	if errors.Is(err, ErrInvalidModelResponse) {
		return ProviderFailureInvalidResponse, true
	}
```

(`ErrProviderResponseTooLarge` stays `false` — do not touch it.)

- [ ] **Step 4: Run the extractor package tests**

Run: `go test ./internal/teamnote/extractor/`
Expected: PASS — including every pre-existing test.

- [ ] **Step 5: Full gate and commit**

Run: `go build ./... && go test ./...`
Expected: PASS.

```bash
git add internal/teamnote/extractor/execution.go internal/teamnote/extractor/openai_test.go
git commit -m "fix(extractor): retry invalid model responses within the provider call"
```

---

### Task 2: Deterministic truncation fallback at the compaction hard limit

**Files:**
- Modify: `internal/teamnote/extractor/episode.go` (`Checkpoint` struct, after `SummaryLastError`)
- Modify: `internal/teamnote/extractor/rolling.go` (`prepareEpisode`, `applyCompaction`; new helpers `recordCompactionFailure`, `truncateEpisodeMessages`)
- Test: `internal/teamnote/extractor/openai_test.go`

**Interfaces:**
- Consumes: Task 1's retryable classification (each failed compaction logical call burns `MaxAttempts` provider attempts); existing `estimateEpisodeTokens(checkpoint Checkpoint, messages []EpisodeMessage) int` (`rolling.go:565`); existing `e.config.CompactStartTokens`.
- Produces: `Checkpoint.CompactionFailures int`, `Checkpoint.CompactionTruncations int`, `Checkpoint.CompactionLastError string` (JSON tags `compaction_failures`, `compaction_truncations`, `compaction_last_error`, all `omitempty`) — persisted via the normal episode save; hard-limit compaction failure no longer returns an error from `prepareEpisode`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/teamnote/extractor/openai_test.go`:

```go
func (s *openAISuite) TestRollingContextTruncatesEpisodeWhenCompactionKeepsFailing() {
	store := newMemoryEpisodeStore()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		if strings.Contains(string(body), "KNOWLEDGE CONTEXT CHECKPOINT COMPACTION") {
			return response(http.StatusOK, `{"choices":[{"message":{"content":"not-json"}}]}`), nil
		}
		eventID := "event-1"
		for _, candidate := range []string{"event-2", "event-3"} {
			if strings.Contains(string(body), candidate) {
				eventID = candidate
			}
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","identity_ref":"decision/release","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		CompactionEnabled: true, CompactStartTokens: 1, CompactTokens: 1,
		ExecutionPolicy: extractor.ExecutionPolicy{MaxAttempts: 2, RetryBackoff: time.Millisecond},
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-truncate")

	_, err = adapter.Extract(ctx, extractorSlice())
	s.Require().NoError(err)

	second := extractorSlice()
	second.InputChecksum = "checksum-2"
	second.NewEventIDs = []string{"event-2"}
	second.Events[0].ID = "event-2"
	_, err = adapter.Extract(ctx, second)
	s.Require().NoError(err, "hard-limit compaction failure must degrade, not fail the slice")

	third := extractorSlice()
	third.InputChecksum = "checksum-3"
	third.NewEventIDs = []string{"event-3"}
	third.Events[0].ID = "event-3"
	_, err = adapter.Extract(ctx, third)
	s.Require().NoError(err, "the episode must never loop on compaction failure")

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-truncate", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.Require().True(ok)
	s.Zero(episode.CompactionCount)
	s.GreaterOrEqual(episode.Checkpoint.CompactionFailures, 2)
	s.Equal(2, episode.Checkpoint.CompactionTruncations)
	s.Contains(episode.Checkpoint.CompactionLastError, "compact extraction episode")
	s.Require().NotEmpty(episode.Messages)
	s.Equal(1+2, len(episode.Messages), "each truncation keeps one message; one exchange appends two")
}

func (s *openAISuite) TestRollingContextKeepsCompactionCountersAfterLaterSuccess() {
	store := newMemoryEpisodeStore()
	var failCompaction atomic.Bool
	failCompaction.Store(true)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		if strings.Contains(string(body), "KNOWLEDGE CONTEXT CHECKPOINT COMPACTION") {
			if failCompaction.Load() {
				return response(http.StatusOK, `{"choices":[{"message":{"content":"not-json"}}]}`), nil
			}
			checkpoint := `{"active_knowledge":[{"memory_id":"decision/release","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":["event-2"]}],"resolved_knowledge":[],"open_questions":[],"evidence_index":{"decision/release":["event-2"]},"source_cursors":{}}`
			return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, checkpoint)), nil
		}
		eventID := "event-1"
		for _, candidate := range []string{"event-2", "event-3"} {
			if strings.Contains(string(body), candidate) {
				eventID = candidate
			}
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","identity_ref":"decision/release","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		CompactionEnabled: true, CompactStartTokens: 1, CompactTokens: 1,
		ExecutionPolicy: extractor.ExecutionPolicy{MaxAttempts: 2, RetryBackoff: time.Millisecond},
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-carryover")

	_, err = adapter.Extract(ctx, extractorSlice())
	s.Require().NoError(err)

	second := extractorSlice()
	second.InputChecksum = "checksum-2"
	second.NewEventIDs = []string{"event-2"}
	second.Events[0].ID = "event-2"
	_, err = adapter.Extract(ctx, second)
	s.Require().NoError(err)

	failCompaction.Store(false)
	third := extractorSlice()
	third.InputChecksum = "checksum-3"
	third.NewEventIDs = []string{"event-3"}
	third.Events[0].ID = "event-3"
	_, err = adapter.Extract(ctx, third)
	s.Require().NoError(err)

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-carryover", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal(1, episode.CompactionCount)
	s.GreaterOrEqual(episode.Checkpoint.CompactionFailures, 1)
	s.Equal(1, episode.Checkpoint.CompactionTruncations)
}
```

Notes for the implementer:
- `extractorSlice()`, `newMemoryEpisodeStore()`, `roundTripFunc`, `response` already exist in this test file; `io`, `strings`, `fmt`, `time` are already imported; `sync/atomic` may need adding.
- The exact message-count arithmetic in the first test (`1+2`) assumes: truncation keeps exactly one message, and each successful exchange appends two episode messages (user prompt + assistant reply). Verify the append count by reading `advanceEpisodeAttempt` before relying on it; if an exchange appends a different number, adjust the expected count and say so in the report — the load-bearing assertions are the counters and the three `NoError`s.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/teamnote/extractor/ -run 'TestOpenAISuite' -v 2>&1 | grep -E "Truncates|KeepsCompaction|FAIL|ok"`
Expected: both new tests FAIL — today the second `Extract` returns an error (hard-limit compaction failure propagates).

- [ ] **Step 3: Implement the checkpoint fields**

In `internal/teamnote/extractor/episode.go`, extend `Checkpoint` after `SummaryLastError`:

```go
	CompactionFailures    int    `json:"compaction_failures,omitempty"`
	CompactionTruncations int    `json:"compaction_truncations,omitempty"`
	CompactionLastError   string `json:"compaction_last_error,omitempty"`
```

- [ ] **Step 4: Implement the fallback in rolling.go**

In `internal/teamnote/extractor/rolling.go`:

1. Carry the new fields across successful compactions — in `applyCompaction`, next to the existing `Summary*` carry-over lines:

```go
	result.checkpoint.CompactionFailures = episode.Checkpoint.CompactionFailures
	result.checkpoint.CompactionTruncations = episode.Checkpoint.CompactionTruncations
	result.checkpoint.CompactionLastError = episode.Checkpoint.CompactionLastError
```

2. Add the helpers (near `applyCompaction`):

```go
func recordCompactionFailure(episode *Episode, err error) {
	episode.Checkpoint.CompactionFailures++
	message := err.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	episode.Checkpoint.CompactionLastError = message
}

func truncateEpisodeMessages(episode *Episode, limit int) int {
	dropped := 0
	for len(episode.Messages) > 1 &&
		estimateEpisodeTokens(episode.Checkpoint, episode.Messages) >= limit {
		episode.Messages = episode.Messages[1:]
		dropped++
	}
	episode.Checkpoint.CompactionTruncations++
	episode.EstimatedTokens = estimateEpisodeTokens(episode.Checkpoint, episode.Messages)
	return dropped
}
```

3. Rewrite the failure paths in `prepareEpisode`. The current tail of the function is:

```go
	result, flightErr := e.consumeCompaction(key, flight)
	if flightErr == nil {
		if applyErr := applyCompaction(episode, result); applyErr == nil {
			return result.usage, nil
		} else {
			flightErr = applyErr
		}
	}
	if !hardLimit {
		return Usage{}, nil
	}
	result, err = e.computeCompaction(ctx, *episode)
	if err != nil {
		return Usage{}, errors.Join(flightErr, err)
	}
	if err := applyCompaction(episode, result); err != nil {
		return Usage{}, err
	}
	return result.usage, nil
```

Replace it with:

```go
	result, flightErr := e.consumeCompaction(key, flight)
	if flightErr == nil {
		if applyErr := applyCompaction(episode, result); applyErr == nil {
			return result.usage, nil
		} else {
			flightErr = applyErr
		}
	}
	recordCompactionFailure(episode, flightErr)
	if !hardLimit {
		return Usage{}, nil
	}
	result, err = e.computeCompaction(ctx, *episode)
	if err == nil {
		if applyErr := applyCompaction(episode, result); applyErr == nil {
			return result.usage, nil
		} else {
			err = applyErr
		}
	}
	recordCompactionFailure(episode, err)
	dropped := truncateEpisodeMessages(episode, e.config.CompactStartTokens)
	slog.Warn("extraction compaction failed; truncated episode deterministically",
		"scope_id", key.ScopeID, "task_ref", key.TaskRef,
		"dropped_messages", dropped, "error", err)
	return Usage{}, nil
```

Add `"log/slog"` to the imports. Note `recordCompactionFailure` is called for the soft path too (flight failed, below hard limit) — that is intentional per the spec.

- [ ] **Step 5: Run the new tests**

Run: `go test ./internal/teamnote/extractor/ -run 'TestOpenAISuite' -v 2>&1 | tail -20`
Expected: PASS, including all pre-existing compaction/conflict tests (`TestRollingContextCompactsIntoStructuredCheckpoint`, the async soft/hard-limit test, and the conflict-retry tests must stay green — they prove the success path is untouched).

- [ ] **Step 6: Full gate and commit**

Run: `gofmt -l internal/teamnote/ && go build ./... && go test ./...`
Expected: gofmt prints nothing; PASS.

```bash
git add internal/teamnote/extractor/episode.go internal/teamnote/extractor/rolling.go internal/teamnote/extractor/openai_test.go
git commit -m "feat(extractor): degrade to deterministic truncation when compaction keeps failing"
```

---

## Self-Review Notes

- Spec coverage: Layer 1 → Task 1; Layer 2 (fallback, counters, carry-over, 500-byte cap, slog warning, soft-path counting) → Task 2; loop-regression and carry-over tests → Task 2 Step 1. The spec's non-goals require no tasks.
- Type consistency: `recordCompactionFailure(*Episode, error)` and `truncateEpisodeMessages(*Episode, int) int` are defined and used only within Task 2; checkpoint field names match between episode.go, applyCompaction carry-over, and the test assertions.
- Known judgment call: `slog.Warn` on the default logger rather than a new `Logger` config field — the extractor has no logger today and `ProviderCallObserver` already captures per-attempt failures for operations; a config field would be scope creep.
