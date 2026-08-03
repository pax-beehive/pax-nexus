# Async Wiki Rebuild Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `POST /v1/wiki/rebuild` asynchronous (202 + background execution + status polling) so it can no longer 504 behind the consumer's long-held injection lock.

**Architecture:** The request path only records rebuild intent in a small `stateMu`-guarded state machine on `sessionconsumer.Controller` and returns immediately; the consumer loop executes the rebuild before each scan, and `scan` yields between streams when a rebuild is queued. State is exposed through the existing ingestion-status endpoint, which the frontend already polls every 5s.

**Tech Stack:** Go (hertz + thriftgo codegen via `make generate`), testify suites, React + vitest.

Spec: `docs/superpowers/specs/2026-08-02-async-wiki-rebuild-design.md` — read it first.

## Global Constraints

- Branch: `fix/async-wiki-rebuild` (already created off origin/main; spec is committed on it).
- CI coverage gate: 80% (`COVERAGE_MIN := 80`); every new code path needs a test.
- Go lint: `make lint` (golangci-lint v2.11.3 via `.tools/bin`).
- Thrift models are generated — never hand-edit `internal/teamnote/transport/httpapi/model/**`; edit `idl/team_memory.thrift` and run `make generate`.
- Public HTTP field names are snake_case strings; rebuild states are exactly `idle | queued | running | failed`.
- The `WikiControl` interface in `internal/teamnote/transport/httpapi/handler/dependencies.go` must not change signatures (only the `sessionconsumer.Status` struct it returns grows).
- Concurrency-sensitive tests must pass under `go test -race`.

---

### Task 1: Controller async rebuild state machine

**Files:**
- Modify: `internal/pagewiki/sessionconsumer/consumer.go`
- Test: `internal/pagewiki/sessionconsumer/consumer_test.go`

**Interfaces:**
- Consumes: existing `Controller` internals (`c.mu`, `c.trigger`, `c.failures`, `c.rebuilder`, `c.now`).
- Produces (later tasks rely on these exact names):
  - `type RebuildState string` with consts `RebuildIdle`, `RebuildQueued`, `RebuildRunning`, `RebuildFailed` (values `"idle"`, `"queued"`, `"running"`, `"failed"`)
  - `type RebuildStatus struct { State RebuildState; Error string; FinishedAt *time.Time }`
  - `Status` gains field `Rebuild RebuildStatus`
  - `Rebuild(ctx, scopeID, since)` keeps its signature `(Status, error)` but returns immediately with `Rebuild.State == RebuildQueued` (or the in-flight state on merge)

- [ ] **Step 1: Write the failing tests**

In `consumer_test.go`, make the fakes safe for background use and add async tests.

1a. Add `"sync"` and `"strings"` to the test imports.

1b. Replace `recordingRebuilder` and its method with a mutex-guarded version plus a done channel and shared event log:

```go
// eventLog records cross-fake ordering so tests can assert what ran first.
type eventLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *eventLog) add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

type recordingRebuilder struct {
	mu               sync.Mutex
	calls            int
	scopeID          string
	processorName    string
	processorVersion string
	since            time.Time
	err              error
	done             chan struct{}
	log              *eventLog
}

func (r *recordingRebuilder) RebuildPageWiki(
	_ context.Context,
	scopeID string,
	processorName string,
	processorVersion string,
	since time.Time,
) error {
	r.mu.Lock()
	r.calls++
	r.scopeID = scopeID
	r.processorName = processorName
	r.processorVersion = processorVersion
	r.since = since
	err := r.err
	r.mu.Unlock()
	if r.log != nil {
		r.log.add("rebuild")
	}
	select {
	case r.done <- struct{}{}:
	default:
	}
	return err
}

func (r *recordingRebuilder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingRebuilder) lastCall() (string, string, string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scopeID, r.processorName, r.processorVersion, r.since
}
```

1c. Make `recordingInjector` mutex-guarded and blockable (replace the struct and `InjectSession`):

```go
type recordingInjector struct {
	mu       sync.Mutex
	requests []pagewiki.InjectSessionRequest
	contexts []context.Context
	err      error
	status   pagewiki.RunStatus
	entered  chan struct{} // when non-nil, signaled as each call starts
	release  chan struct{} // when non-nil, each call blocks on one receive
	log      *eventLog
}

func (i *recordingInjector) InjectSession(
	ctx context.Context,
	request pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	i.mu.Lock()
	i.requests = append(i.requests, request)
	i.contexts = append(i.contexts, ctx)
	entered, release, err, status := i.entered, i.release, i.err, i.status
	i.mu.Unlock()
	if i.log != nil {
		i.log.add("inject")
	}
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return pagewiki.InjectResult{}, err
	}
	if status == "" {
		status = pagewiki.RunStatusSucceeded
	}
	return pagewiki.InjectResult{
		Run: pagewiki.MaintenanceRun{Status: status},
	}, nil
}

func (i *recordingInjector) requestCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.requests)
}
```

1d. In `SetupTest`, construct the rebuilder as `&recordingRebuilder{done: make(chan struct{}, 4)}`.

1e. Existing tests that read the fakes' fields directly stay valid because those tests run synchronously (no `Start`); do NOT rewrite them beyond what this plan says.

1f. Replace `TestRebuildResetsDerivedWikiStateAndSchedulesFreshConsumption` with:

```go
func (s *consumerSuite) TestRebuildQueuesAndRunsInBackground() {
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	status, err := s.consumer.Rebuild(context.Background(), "local-team", cutoff)

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	s.Zero(s.rebuilder.callCount())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)

	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("background rebuild did not run")
	}
	scopeID, name, version, since := s.rebuilder.lastCall()
	s.Equal("local-team", scopeID)
	s.Equal(sessionconsumer.ProcessorName, name)
	s.Equal(sessionconsumer.ProcessorVersion, version)
	s.Equal(cutoff, since)
	s.Eventually(func() bool {
		st, statusErr := s.consumer.Status(context.Background(), "local-team")
		return statusErr == nil &&
			st.Rebuild.State == sessionconsumer.RebuildIdle &&
			st.Rebuild.FinishedAt != nil
	}, time.Second, 10*time.Millisecond)
}
```

1g. Add the lock-independence + idempotent-merge test:

```go
func (s *consumerSuite) TestRebuildReturnsImmediatelyAndMergesWhileInjectionHoldsLock() {
	s.injector.entered = make(chan struct{}, 4)
	s.injector.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	select {
	case <-s.injector.entered:
	case <-time.After(time.Second):
		s.Fail("injection did not start")
	}

	// The scan goroutine now holds c.mu inside InjectSession. Rebuild must
	// not touch that lock: if it did, these calls would hang and the suite
	// would time out.
	first := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	status, err := s.consumer.Rebuild(context.Background(), "local-team", first)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	status, err = s.consumer.Rebuild(context.Background(), "local-team", second)
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)

	close(s.injector.release)
	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("background rebuild did not run")
	}
	s.Equal(1, s.rebuilder.callCount())
	_, _, _, since := s.rebuilder.lastCall()
	s.Equal(first, since) // the merged second request's since was discarded
}
```

1h. Add the failure-surfacing test:

```go
func (s *consumerSuite) TestBackgroundRebuildFailureSurfacesInStatusAndCanRequeue() {
	s.rebuilder.err = errors.New("rebuild unavailable")
	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)

	s.Eventually(func() bool {
		st, statusErr := s.consumer.Status(context.Background(), "local-team")
		return statusErr == nil &&
			st.Rebuild.State == sessionconsumer.RebuildFailed &&
			strings.Contains(st.Rebuild.Error, "rebuild unavailable")
	}, time.Second, 10*time.Millisecond)

	status, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)
	s.Equal(sessionconsumer.RebuildQueued, status.Rebuild.State)
	s.Empty(status.Rebuild.Error)
}
```

1i. Add the backoff-clearing test (spec section 5: "Successful rebuild clears the failures map"):

```go
func (s *consumerSuite) TestSuccessfulRebuildClearsInjectionBackoff() {
	s.injector.err = errors.New("planner unavailable")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	s.Eventually(func() bool {
		return s.injector.requestCount() > 0
	}, time.Second, 10*time.Millisecond)

	s.injector.mu.Lock()
	s.injector.err = nil
	s.injector.mu.Unlock()
	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)

	// Without the failures reset the stream stays backed off for
	// interval<<1 = 2h (interval is time.Hour in SetupTest); the
	// post-rebuild scan must inject immediately instead.
	select {
	case <-s.store.advanced:
	case <-time.After(time.Second):
		s.Fail("injection did not resume after rebuild cleared backoff")
	}
}
```

(Deterministic: with a 1h ticker the only wakeups are the initial scan — which records the backoff — and the Rebuild trigger, so there is no window for a second attempt in between.)

1j. In `TestStoreFailuresAreReported`, delete the `"rebuild"` table entry (Rebuild no longer returns the rebuilder's error synchronously). Keep the `Rebuild(ctx, "", …)` validation assertion in `TestValidationRejectsIncompleteConfigurationAndInput` — empty scope must still error.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && go test -race ./internal/pagewiki/sessionconsumer/`
Expected: compile errors (`RebuildQueued`, `status.Rebuild` undefined) — that is the failing state for type-driven TDD.

- [ ] **Step 3: Implement the state machine in `consumer.go`**

3a. Add types next to `Status` (after the `Progress` struct):

```go
type RebuildState string

const (
	RebuildIdle    RebuildState = "idle"
	RebuildQueued  RebuildState = "queued"
	RebuildRunning RebuildState = "running"
	RebuildFailed  RebuildState = "failed"
)

// RebuildStatus is the in-memory rebuild state machine snapshot. Error is
// set only while State is RebuildFailed; FinishedAt records the last
// successful completion. It lives only in process memory: a restart resets
// it, matching the failureRecord policy above.
type RebuildStatus struct {
	State      RebuildState
	Error      string
	FinishedAt *time.Time
}
```

Add `Rebuild RebuildStatus` as a field on `Status`.

3b. Add fields to `Controller` (after `now func() time.Time`):

```go
	// stateMu guards the rebuild fields below. It is separate from mu so
	// status reads never wait behind a minutes-long injection scan.
	stateMu      sync.Mutex
	rebuild      RebuildStatus
	rebuildScope string
	rebuildSince time.Time
```

In `New`, add `rebuild: RebuildStatus{State: RebuildIdle},` to the constructed `Controller` literal.

3c. Replace the body of `Rebuild`:

```go
func (c *Controller) Rebuild(_ context.Context, scopeID string, since time.Time) (Status, error) {
	if strings.TrimSpace(scopeID) == "" {
		return Status{}, fmt.Errorf("rebuild Page Wiki: scope is required")
	}
	c.stateMu.Lock()
	if c.rebuild.State != RebuildQueued && c.rebuild.State != RebuildRunning {
		c.rebuild = RebuildStatus{State: RebuildQueued, FinishedAt: c.rebuild.FinishedAt}
		c.rebuildScope = scopeID
		c.rebuildSince = since
	}
	snapshot := c.rebuild
	c.stateMu.Unlock()
	select {
	case c.trigger <- struct{}{}:
	default:
	}
	return Status{AutoInject: true, Rebuild: snapshot}, nil
}
```

(`AutoInject: true` matches today's behavior: the rebuild re-enables auto-inject when it executes.)

3d. Add `maybeRebuild` and snapshot helpers (place after `Rebuild`):

```go
// maybeRebuild executes a queued rebuild on the consumer goroutine, using
// the loop's context so a disconnected HTTP client cannot cancel it.
func (c *Controller) maybeRebuild(ctx context.Context) {
	c.stateMu.Lock()
	if c.rebuild.State != RebuildQueued {
		c.stateMu.Unlock()
		return
	}
	c.rebuild.State = RebuildRunning
	scopeID, since := c.rebuildScope, c.rebuildSince
	c.stateMu.Unlock()

	c.mu.Lock()
	err := c.rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion, since)
	if err == nil {
		c.failures = make(map[string]failureRecord)
	}
	c.mu.Unlock()

	c.stateMu.Lock()
	if err != nil {
		c.rebuild = RebuildStatus{State: RebuildFailed, Error: err.Error(), FinishedAt: c.rebuild.FinishedAt}
	} else {
		finished := c.now()
		c.rebuild = RebuildStatus{State: RebuildIdle, FinishedAt: &finished}
	}
	c.stateMu.Unlock()
	if err != nil {
		c.logger.ErrorContext(ctx, "Page Wiki rebuild failed", "scope_id", scopeID, "error", err)
	}
}

func (c *Controller) rebuildSnapshot() RebuildStatus {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.rebuild
}
```

3e. Route the loop through a shared tick. In `Start`, replace the goroutine body's three `c.scan(ctx)` calls with `c.tick(ctx)` and add:

```go
func (c *Controller) tick(ctx context.Context) {
	c.maybeRebuild(ctx)
	c.scan(ctx)
}
```

3f. In `Status`, build the base status with the rebuild snapshot:

```go
	status := Status{AutoInject: enabled, Rebuild: c.rebuildSnapshot()}
```

(the rest of the function is unchanged).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && go test -race ./internal/pagewiki/sessionconsumer/`
Expected: PASS (all suite tests, including the untouched synchronous ones).

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/sessionconsumer/consumer.go internal/pagewiki/sessionconsumer/consumer_test.go
git commit -m "feat(pagewiki): make wiki rebuild asynchronous in the session consumer"
```

---

### Task 2: scan yields to a queued rebuild between streams

**Files:**
- Modify: `internal/pagewiki/sessionconsumer/consumer.go`
- Test: `internal/pagewiki/sessionconsumer/consumer_test.go`

**Interfaces:**
- Consumes: Task 1's `RebuildQueued` state, `stateMu`, `eventLog`, blockable fakes.
- Produces: unexported `rebuildQueued() bool` on `Controller`; `scan` ends its pass early when it returns true.

- [ ] **Step 1: Write the failing test**

```go
func (s *consumerSuite) TestScanYieldsToQueuedRebuildBetweenStreams() {
	log := &eventLog{}
	s.injector.log = log
	s.rebuilder.log = log
	s.store.streams = append(s.store.streams, sessionconsumer.Stream{
		ScopeID: "local-team",
		Actor:   session.Actor{UserID: "owner", AgentID: "agent-2", SessionID: "second-demo"},
		Head:    5,
	})
	s.injector.entered = make(chan struct{}, 8)
	s.injector.release = make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.consumer.Start(ctx)
	select {
	case <-s.injector.entered:
	case <-time.After(time.Second):
		s.Fail("first injection did not start")
	}

	_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
	s.Require().NoError(err)
	s.injector.release <- struct{}{} // let the in-flight stream finish

	select {
	case <-s.rebuilder.done:
	case <-time.After(time.Second):
		s.Fail("rebuild did not run after the in-flight stream")
	}
	// scan yielded after the first stream: exactly one injection happened
	// before the rebuild.
	s.Equal([]string{"inject", "rebuild"}, log.snapshot()[:2])
	cancel()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && go test -race ./internal/pagewiki/sessionconsumer/ -run 'TestConsumerSuite/TestScanYieldsToQueuedRebuildBetweenStreams' -v`
Expected: FAIL — without yielding, scan injects `second-demo` before the loop reaches `maybeRebuild`, so the log starts `["inject", "inject"]` (the test hangs on `s.rebuilder.done` until the second `release` send would be needed — the 1s timeout turns that into a Fail, not a hang).

- [ ] **Step 3: Implement the yield**

Add to `consumer.go`:

```go
func (c *Controller) rebuildQueued() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.rebuild.State == RebuildQueued
}
```

In `scan`, at the top of the `for _, stream := range streams` loop body, before the `backedOff` check:

```go
		// A queued rebuild wipes everything this pass would build; yield
		// the lock now instead of after the whole backlog. The remaining
		// streams are picked up by the next tick.
		if c.rebuildQueued() {
			return
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && go test -race ./internal/pagewiki/sessionconsumer/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/sessionconsumer/consumer.go internal/pagewiki/sessionconsumer/consumer_test.go
git commit -m "feat(pagewiki): scan yields to a queued rebuild between streams"
```

---

### Task 3: IDL + handler — 202 Accepted and rebuild fields in status

**Files:**
- Modify: `idl/team_memory.thrift`
- Modify: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints.go`
- Test: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints_test.go`
- Regenerated: `internal/teamnote/transport/httpapi/model/**` (via `make generate` — never hand-edit)

**Interfaces:**
- Consumes: Task 1's `sessionconsumer.RebuildStatus` (fields `State RebuildState`, `Error string`, `FinishedAt *time.Time`) now present on `sessionconsumer.Status.Rebuild`.
- Produces (Task 4 relies on these JSON fields): `rebuild_state` (`"idle" | "queued" | "running" | "failed"`, always present), `rebuild_error` (only when failed), `last_rebuild_finished_at` (RFC3339, only after a success) on both `GET /v1/wiki/ingestion` and `POST /v1/wiki/rebuild` responses; rebuild now answers **202**.

- [ ] **Step 1: Edit the IDL**

In `idl/team_memory.thrift` replace the two structs:

```thrift
struct WikiIngestionStatusResponse {
  1: required bool auto_inject
  2: optional i32 pending_sessions
  3: optional string last_processed_at
  4: optional string rebuild_state
  5: optional string rebuild_error
  6: optional string last_rebuild_finished_at
}
```

```thrift
struct RebuildWikiResponse {
  1: required bool auto_inject
  2: optional string rebuild_state
  3: optional string rebuild_error
  4: optional string last_rebuild_finished_at
}
```

- [ ] **Step 2: Regenerate models**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && make generate`
Expected: `internal/teamnote/transport/httpapi/model/teammemory/api/team_memory.go` gains `RebuildState/RebuildError/LastRebuildFinishedAt *string` on both structs. Inspect `git status` — if hz touched files unrelated to these two structs (other IDLs regenerate too), leave them out of the commit unless they are pure regeneration noise from THIS repo's committed IDLs; if anything looks wrong, stop and report.

- [ ] **Step 3: Write the failing handler tests**

Follow the file's existing suite helpers (`s.perform`, `s.performWithBody`, the `wikiControl` stub). Update/add:

3a. The stub's `Rebuild` must return a queued snapshot — in the stub (around line 212):

```go
	return sessionconsumer.Status{
		AutoInject: true,
		Rebuild:    sessionconsumer.RebuildStatus{State: sessionconsumer.RebuildQueued},
	}, nil
```

3b. Every existing assertion that a successful `POST /v1/wiki/rebuild` returns `http.StatusOK` changes to `http.StatusAccepted` (the owner test near line 54 and the `since` variants near lines 113–142). Non-2xx cases (forbidden, bad since, internal error) keep their current codes.

3c. Extend the owner rebuild test to pin the body:

```go
	s.Equal(http.StatusAccepted, response.Code)
	s.Contains(response.Body.String(), `"rebuild_state":"queued"`)
```

3d. Add a status-exposure test next to the existing ingestion-status tests. Give the stub a settable status field if it does not have one; have `Status` return it:

```go
func (s *wikiEndpointsSuite) TestIngestionStatusExposesRebuildFailure() {
	finished := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	s.wikiControl.status = sessionconsumer.Status{
		AutoInject: true,
		Rebuild: sessionconsumer.RebuildStatus{
			State:      sessionconsumer.RebuildFailed,
			Error:      "rebuild unavailable",
			FinishedAt: &finished,
		},
	}

	response := s.perform(http.MethodGet, "/v1/wiki/ingestion", true)

	s.Equal(http.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"rebuild_state":"failed"`)
	s.Contains(response.Body.String(), `"rebuild_error":"rebuild unavailable"`)
	s.Contains(response.Body.String(), `"last_rebuild_finished_at":"2026-08-01T10:00:00Z"`)
}
```

(Adapt the suite type name and stub wiring to what the file actually uses; the suite name above is illustrative, the assertions are not.)

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && go test ./internal/teamnote/transport/httpapi/handler/`
Expected: FAIL — 200 vs 202, missing rebuild fields.

- [ ] **Step 5: Implement the handler changes**

In `wiki_ingestion_endpoints.go`:

5a. Add a mapping helper:

```go
func applyRebuildStatus(rebuild sessionconsumer.RebuildStatus) (state, message, finishedAt *string) {
	value := string(rebuild.State)
	if value == "" {
		value = string(sessionconsumer.RebuildIdle)
	}
	state = &value
	if rebuild.Error != "" {
		errorCopy := rebuild.Error
		message = &errorCopy
	}
	if rebuild.FinishedAt != nil {
		formatted := rebuild.FinishedAt.UTC().Format(time.RFC3339)
		finishedAt = &formatted
	}
	return state, message, finishedAt
}
```

5b. In `GetWikiIngestionStatus`, after constructing `response`:

```go
	response.RebuildState, response.RebuildError, response.LastRebuildFinishedAt =
		applyRebuildStatus(status.Rebuild)
```

5c. In `RebuildWiki`, replace the final `c.JSON` line:

```go
	response := &api.RebuildWikiResponse{AutoInject: status.AutoInject}
	response.RebuildState, response.RebuildError, response.LastRebuildFinishedAt =
		applyRebuildStatus(status.Rebuild)
	c.JSON(consts.StatusAccepted, response)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && go test ./internal/teamnote/transport/httpapi/handler/ ./internal/pagewiki/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add idl/team_memory.thrift internal/teamnote/transport/httpapi/model internal/teamnote/transport/httpapi/handler
git commit -m "feat(api): wiki rebuild answers 202 and exposes rebuild state in status"
```

---

### Task 4: Frontend — rebuild badge, disabled button, queued messaging

**Files:**
- Modify: `web/src/api/wiki.ts`
- Modify: `web/src/pages/WikiStatusPage.tsx`
- Modify: `web/src/pages/wiki-status/WikiIngestionCard.tsx`
- Test: `web/tests/wiki-status.dom.test.tsx`

**Interfaces:**
- Consumes: Task 3's JSON fields `rebuild_state`, `rebuild_error`, `last_rebuild_finished_at`; existing 5s `usePolling` of `GET /v1/wiki/ingestion`.
- Produces: `WikiIngestionCardProps` gains `rebuildState?: WikiRebuildState` and `rebuildError?: string`; user-visible strings `"Rebuild queued…"`, `"Rebuild in progress…"`, `"Rebuild failed: <error>"`, and the confirm message `"Reset & rebuild queued. The wiki will be cleared and rebuilt in the background."`.

- [ ] **Step 1: Write the failing tests**

In `web/tests/wiki-status.dom.test.tsx`:

1a. In the existing owner-rebuild test (`"lets an owner confirm a full Wiki rebuild without deleting Session Lake"`), change the rebuild response to `jsonResponse({ auto_inject: true, rebuild_state: "queued" })` and the awaited message to:

```ts
    await screen.findByText(
      "Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.",
    );
```

1b. Same message replacement (`"Wiki cleared. Rebuilding from Session Lake…"` → the queued message above) in `"closes the rebuild dialog on confirm before the server responds"`, `"sends the lookback cutoff when a rebuild date is picked"`, and `"omits since when the rebuild date is left empty"`. In the closes-dialog test also change the deferred response payload to `jsonResponse({ auto_inject: true, rebuild_state: "queued" })`.

1c. Add two new cases to the `"wiki status page ingestion controls"` describe block:

```ts
  it("disables Reset & rebuild and shows progress while a rebuild runs", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "owner" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({ auto_inject: true, rebuild_state: "running" });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("Rebuild in progress…");
    const rebuildButton = screen.getByRole("button", { name: "Reset & rebuild" });
    expect(rebuildButton.hasAttribute("disabled")).toBe(true);
  });

  it("surfaces a failed rebuild with its error", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "owner" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({
            auto_inject: false,
            rebuild_state: "failed",
            rebuild_error: "database unavailable",
          });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("Rebuild failed: database unavailable");
    const rebuildButton = screen.getByRole("button", { name: "Reset & rebuild" });
    expect(rebuildButton.hasAttribute("disabled")).toBe(false);
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory/web && npm test -- tests/wiki-status.dom.test.tsx`
Expected: FAIL on the new cases and the message changes.

- [ ] **Step 3: Implement**

3a. `web/src/api/wiki.ts`:

```ts
export type WikiRebuildState = "idle" | "queued" | "running" | "failed";

export interface WikiIngestionStatus {
  auto_inject: boolean;
  pending_sessions?: number;
  last_processed_at?: string;
  rebuild_state?: WikiRebuildState;
  rebuild_error?: string;
  last_rebuild_finished_at?: string;
}
```

3b. `WikiIngestionCard.tsx` — add props and render the state line inside the owner-only `wiki-reset` block, before the button:

```tsx
import type { WikiRebuildState } from "../../api/wiki";
```

Props additions:

```tsx
  rebuildState?: WikiRebuildState;
  rebuildError?: string;
```

Inside the `wiki-reset` `<div>` (after the copy `<div>`, before the `<Button>`), with `const rebuildActive = rebuildState === "queued" || rebuildState === "running";` computed at the top of the component:

```tsx
          {rebuildActive && (
            <span className="muted small" role="status">
              {rebuildState === "queued" ? "Rebuild queued…" : "Rebuild in progress…"}
            </span>
          )}
          {rebuildState === "failed" && rebuildError && (
            <span className="small wiki-rebuild-error" role="alert">
              Rebuild failed: {rebuildError}
            </span>
          )}
```

Change the button's `disabled={busy}` to `disabled={busy || rebuildActive}`.

3c. `WikiStatusPage.tsx`:

- Pass the new props where `WikiIngestionCard` is rendered:

```tsx
        rebuildState={status?.rebuild_state}
        rebuildError={status?.rebuild_error}
```

- In `confirmRebuild`, replace the post-await success message:

```tsx
      setMessage("Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.");
```

The pre-await `"Reset & rebuild triggered…"` message and the close-before-await comment stay: the endpoint now answers fast, but the pattern is still correct. Update that comment to match reality:

```tsx
    // Close before awaiting; the endpoint answers 202 quickly, and rebuild
    // progress is reported by the polled ingestion status.
```

3d. Styling: keep the queued/running line on the existing `muted small` classes only. For the failure line, grep the web stylesheets for an existing error text class (`grep -rn "error" web/src/*.css web/src/**/*.css`) and reuse the first match that styles inline error text; if none exists, drop the `wiki-rebuild-error` class from the JSX and use `muted small` there too — do not add new CSS in this task.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory/web && npm test -- tests/wiki-status.dom.test.tsx`
Expected: PASS.

Run the full web suite: `cd /Users/toddzheng/Workspace/golang/team-memory/web && npm test`
Expected: PASS (no other test greps the old message strings; if one does, update it the same way).

- [ ] **Step 5: Commit**

```bash
git add web/src/api/wiki.ts web/src/pages/WikiStatusPage.tsx web/src/pages/wiki-status/WikiIngestionCard.tsx web/tests/wiki-status.dom.test.tsx
git commit -m "feat(web): show async wiki rebuild progress and guard the rebuild button"
```

---

### Task 5: Full verification gates

**Files:** none new — verification only.

- [ ] **Step 1: Go gates**

Run: `cd /Users/toddzheng/Workspace/golang/team-memory && make lint`
Expected: clean. (Known pre-existing main issues, if any, must not be in files this branch touched.)

Run: `go test -race ./internal/pagewiki/... ./internal/teamnote/...`
Expected: PASS. (Repo-known flaky DB tests live outside these packages; if one appears here, rerun once and report if persistent.)

- [ ] **Step 2: Web gates**

Run: `cd web && npm test && npm run build`
Expected: tests PASS, `tsc --noEmit` clean.

- [ ] **Step 3: Fix anything found, then re-run the failing gate until clean. Commit any fixes with a `fix:` message scoped to the task they belong to.**
