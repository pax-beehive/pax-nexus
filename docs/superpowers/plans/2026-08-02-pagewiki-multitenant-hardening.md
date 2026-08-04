# PageWiki Multi-Tenant Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the three pre-multi-tenant gate fixes from PR #68's checklist: per-scope fairness in `PendingStreams`, per-scope single-flight hydration in the two pagewiki managers, and a per-scope-lock + bounded-worker-pool consumer replacing the process-global `Controller.mu`.

**Architecture:** Spec: `docs/superpowers/specs/2026-08-02-multitenant-hardening-design.md` (committed on this branch — read it first). Three independent fixes in the pagewiki session-consumer path. SQL fairness is a query-only change; manager single-flight is a two-tier lock scheme (global map lock + per-scope entry lock); the consumer rework happens in two reviewable stages: first decompose the global mutex into per-scope locks (scheduling unchanged), then replace the serial scan with scope jobs dispatched through a semaphore-bounded pool (K default 2, `TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY`).

**Tech Stack:** Go 1.25, pgx/pgxpool, testify suites, golangci-lint v2.

## Global Constraints

- Module path: `github.com/pax-beehive/pax-nexus`.
- Branch: `feat/pagewiki-multitenant-hardening` (already exists, carries the spec commit). Execute in an isolated worktree created from this branch (superpowers:using-git-worktrees).
- Worktree DB isolation: if another worktree's compose already holds port 55432, run `TEAM_MEMORY_POSTGRES_PORT=55499 make db-up` — the Makefile DSN follows the port automatically.
- Postgres-backed tests need `make db-up` first and `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable'` (adjust port if overridden). Tests skip silently when the env var is unset — a skipped suite is NOT a passing suite; always export the DSN.
- CI gates: `make lint`, `make coverage` (min 80%), `make integration-test`. Main has had intermittent flaky DB tests; record a baseline run on the branch tip before Task 1 and treat only NEW failures as blockers.
- Race detector: every `go test` command in this plan runs with `-race` (concurrency is the whole point of this PR).
- On-prem behavior invariant: a single-scope deployment must behave as today (one scope → one job → serial).
- Commit style: `type(scope): summary`, ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- Do not merge the PR — the user merges PRs in this repo.

---

### Task 1: `PendingStreams` per-scope quota

Kill the cross-tenant starvation: a scope with ≥100 permanently failing streams currently occupies every slot of the global `ORDER BY updated_at LIMIT 100` window each scan.

**Files:**
- Modify: `internal/platform/postgres/pagewiki_consumer.go` (the `PendingStreams` query, currently :57-74)
- Test: `internal/pagewiki/sessionconsumer/integration_test.go` (postgres-backed suite `postgresConsumerSuite`)

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: no signature change — `PendingStreams(ctx) ([]sessionconsumer.Stream, error)` as before; result now caps each scope at 20 rows per call and interleaves scopes (every scope's 1st stream sorts before any scope's 2nd). Later tasks rely only on the existing signature.

- [ ] **Step 1: Write the failing integration test**

Add to `internal/pagewiki/sessionconsumer/integration_test.go`. The suite already has `s.scopeID`/`s.otherScopeID` (unique per test, cleaned up in TearDownTest) and a `seedSession(scopeID)` helper that seeds a fixed `runtime-session`. Add a parameterized sibling helper next to `seedSession` (do NOT modify `seedSession` — other tests use it):

```go
// seedStream seeds one pending stream with its own session ID, so a test
// can give a single scope a backlog wider than the per-scope quota.
func (s *postgresConsumerSuite) seedStream(scopeID, sessionID string) {
	_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_streams (
    scope_id, user_id, agent_id, session_id, last_sequence, complete
) VALUES ($1, 'owner', 'runtime-agent', $2, 1, TRUE)`, scopeID, sessionID)
	s.Require().NoError(err)
}
```

And the test:

```go
// TestPendingStreamsCapsEachScopeSoNoTenantStarvesAnother pins the
// multi-tenant fairness quota: one scope's huge (or permanently failing)
// backlog may take at most 20 of the 100 slots per scan, so every other
// scope's streams still surface.
func (s *postgresConsumerSuite) TestPendingStreamsCapsEachScopeSoNoTenantStarvesAnother() {
	for i := 0; i < 25; i++ {
		s.seedStream(s.scopeID, fmt.Sprintf("bulk-session-%02d", i))
	}
	s.seedSession(s.otherScopeID)
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool())
	s.Require().NoError(err)
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.scopeID, true))
	s.Require().NoError(consumerStore.SetAutoInjectEnabled(s.ctx, s.otherScopeID, true))

	streams, err := consumerStore.PendingStreams(s.ctx)

	s.Require().NoError(err)
	own := s.streamsForScope(streams, s.scopeID)
	other := s.streamsForScope(streams, s.otherScopeID)
	s.Len(own, 20, "a single scope must be capped at 20 slots per scan")
	s.Require().Len(other, 1, "the second scope must not be starved")
	s.Equal("runtime-session", other[0].Actor.SessionID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make db-up && TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test -race ./internal/pagewiki/sessionconsumer -run 'TestPostgresConsumerSuite/TestPendingStreamsCapsEachScope' -count=1 -v`
Expected: FAIL — `own` has 25 rows (no cap yet). Verify the test RAN (not skipped).

- [ ] **Step 3: Rewrite the query**

In `internal/platform/postgres/pagewiki_consumer.go`, replace the SQL inside `PendingStreams` (keep the surrounding Go unchanged — `queryStreams` scans exactly these five columns in this order):

```sql
SELECT scope_id, user_id, agent_id, session_id, last_sequence
FROM (
  SELECT stream.scope_id, stream.user_id, stream.agent_id,
         stream.session_id, stream.last_sequence, stream.updated_at,
         ROW_NUMBER() OVER (
           PARTITION BY stream.scope_id ORDER BY stream.updated_at
         ) AS scope_rank
  FROM session_streams AS stream
  JOIN pagewiki_ingestion_settings AS setting
    ON setting.scope_id = stream.scope_id AND setting.auto_inject = TRUE
  LEFT JOIN session_processor_cursors AS cursor
    ON cursor.processor_name = $1
   AND cursor.processor_version = $2
   AND cursor.scope_id = stream.scope_id
   AND cursor.agent_id = stream.agent_id
   AND cursor.session_id = stream.session_id
  WHERE stream.last_sequence > COALESCE(cursor.committed_sequence, 0)
    AND stream.source = 'agent-session'
    AND stream.agent_id <> ''
) AS ranked
WHERE scope_rank <= 20
ORDER BY scope_rank, updated_at
LIMIT 100
```

Update the doc comment above the method: add that each scope is capped at 20 rows per call and `ORDER BY scope_rank` interleaves scopes round-robin so no tenant's backlog can starve another (20 is a fairness constant, not a throughput knob). Note the deliberately untouched same-shaped query in `internal/platform/postgres/audit.go` (read-only risk classification; a delayed round is harmless).

- [ ] **Step 4: Run the new test and the whole consumer + platform suites**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test -race -p 1 ./internal/pagewiki/sessionconsumer ./internal/platform/postgres -count=1`
Expected: PASS, including the existing multi-scope tests (`TestPendingStreamsSpanEveryScopeWithAutoInject` etc.).

- [ ] **Step 5: Commit**

```bash
git add internal/platform/postgres/pagewiki_consumer.go internal/pagewiki/sessionconsumer/integration_test.go
git commit -m "fix(pagewiki): cap PendingStreams at 20 rows per scope so no tenant starves another"
```

---

### Task 2: `RepositoryManager` per-scope single-flight hydration

`ForScope` currently holds the manager-wide mutex across full wiki-mirror hydration (`internal/pagewiki/postgres/manager.go:40-50`), so a cold scope's first touch blocks every other scope.

**Files:**
- Modify: `internal/pagewiki/postgres/manager.go`
- Create: `internal/pagewiki/postgres/manager_internal_test.go` (in-package, no DB)
- Test (existing, must stay green): `internal/pagewiki/postgres/manager_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `ForScope(ctx, scopeID) (*Repository, error)` — signature unchanged. New guarantees Task 4/5 rely on: different scopes never block each other; same-scope concurrent first-touch hydrates exactly once; a failed hydration is not cached (next call retries). Internal shape (Task 3 mirrors it): `entries map[string]*repositoryEntry`, `hydrate func(context.Context, string) (*Repository, error)`.

- [ ] **Step 1: Write the failing in-package tests**

Create `internal/pagewiki/postgres/manager_internal_test.go` (package `postgres` — in-package so tests can construct the manager with a fake hydrator, no pool, no DB):

```go
package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testManager(hydrate func(context.Context, string) (*Repository, error)) *RepositoryManager {
	return &RepositoryManager{entries: make(map[string]*repositoryEntry), hydrate: hydrate}
}

func TestForScopeColdHydrationDoesNotBlockOtherScopes(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	manager := testManager(func(_ context.Context, scopeID string) (*Repository, error) {
		if scopeID == "cold" {
			close(entered)
			<-release
		}
		return &Repository{scopeID: scopeID}, nil
	})
	go func() { _, _ = manager.ForScope(context.Background(), "cold") }()
	<-entered

	done := make(chan struct{})
	go func() {
		if _, err := manager.ForScope(context.Background(), "hot"); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hot scope blocked behind cold scope's hydration")
	}
	close(release)
}

func TestForScopeHydratesConcurrentFirstTouchExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	manager := testManager(func(_ context.Context, scopeID string) (*Repository, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		return &Repository{scopeID: scopeID}, nil
	})
	var wait sync.WaitGroup
	results := make([]*Repository, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			repository, err := manager.ForScope(context.Background(), "scope-a")
			if err != nil {
				t.Error(err)
				return
			}
			results[index] = repository
		}(i)
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("hydrate ran %d times, want 1", calls.Load())
	}
	if results[0] == nil || results[0] != results[1] {
		t.Fatal("concurrent first-touch must return the same cached instance")
	}
}

func TestForScopeRetriesAfterFailedHydration(t *testing.T) {
	var calls atomic.Int32
	manager := testManager(func(_ context.Context, scopeID string) (*Repository, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("database offline")
		}
		return &Repository{scopeID: scopeID}, nil
	})
	if _, err := manager.ForScope(context.Background(), "scope-a"); err == nil {
		t.Fatal("first hydration must surface its error")
	}
	repository, err := manager.ForScope(context.Background(), "scope-a")
	if err != nil {
		t.Fatalf("second hydration must retry, got %v", err)
	}
	if repository == nil {
		t.Fatal("second hydration must return the repository")
	}
}
```

- [ ] **Step 2: Run to verify they fail to compile**

Run: `go test -race ./internal/pagewiki/postgres -run 'TestForScope(ColdHydration|Hydrates|Retries)' -count=1`
Expected: compile FAIL — `entries`, `repositoryEntry`, and `hydrate` don't exist yet.

- [ ] **Step 3: Implement the two-tier scheme**

Rewrite `internal/pagewiki/postgres/manager.go`'s manager (keep `NewRepositoryManager`'s nil-pool validation and the file's package/import shape):

```go
// repositoryEntry is one scope's hydration slot. Its mutex serializes
// hydration of that scope only; the manager-wide mutex is held just long
// enough to look the entry up, so a cold scope's (expensive, full-mirror)
// hydration never blocks any other scope.
type repositoryEntry struct {
	mu         sync.Mutex
	repository *Repository // nil until hydrated; errors are not cached
}

type RepositoryManager struct {
	mu      sync.Mutex
	entries map[string]*repositoryEntry
	hydrate func(ctx context.Context, scopeID string) (*Repository, error)
}

func NewRepositoryManager(pool *pgxpool.Pool, options ...memory.Option) (*RepositoryManager, error) {
	if pool == nil {
		return nil, fmt.Errorf("create pagewiki repository manager: pool is required")
	}
	return &RepositoryManager{
		entries: make(map[string]*repositoryEntry),
		hydrate: func(ctx context.Context, scopeID string) (*Repository, error) {
			return NewRepository(ctx, pool, scopeID, options...)
		},
	}, nil
}

func (m *RepositoryManager) ForScope(ctx context.Context, scopeID string) (*Repository, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("resolve pagewiki repository: scope ID is required")
	}
	m.mu.Lock()
	entry, ok := m.entries[scopeID]
	if !ok {
		entry = &repositoryEntry{}
		m.entries[scopeID] = entry
	}
	m.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.repository != nil {
		return entry.repository, nil
	}
	repository, err := m.hydrate(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("hydrate pagewiki repository for scope %s: %w", scopeID, err)
	}
	entry.repository = repository
	return repository, nil
}
```

Update the type's doc comment: per-scope single-flight (same scope hydrates once; different scopes independent); failed hydrations retry; idle-scope eviction still deliberately deferred to Phase 3. Adjust struct-literal construction sites if any exist (`grep -rn "RepositoryManager{" internal/ cmd/`).

- [ ] **Step 4: Run unit tests, then the DB-backed manager suite**

Run: `go test -race ./internal/pagewiki/postgres -run 'TestForScope(ColdHydration|Hydrates|Retries)' -count=1 -v`
Expected: PASS.
Run: `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test -race ./internal/pagewiki/postgres -count=1`
Expected: PASS — the existing `repositoryManagerSuite` (same-instance caching, per-scope isolation) ran and passed, not skipped.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/postgres/manager.go internal/pagewiki/postgres/manager_internal_test.go
git commit -m "fix(pagewiki): repository manager hydrates per-scope single-flight, scopes no longer block each other"
```

---

### Task 3: `ServiceManager` per-scope single-flight

Same disease, one layer up: `ServiceManager.ForScope` (`internal/pagewiki/service_manager.go:61-81`) holds the manager mutex across `m.repositories(ctx, scopeID)` — which is exactly the hydration Task 2 unblocked — so the win is lost unless this layer is fixed too.

**Files:**
- Modify: `internal/pagewiki/service_manager.go`
- Test: locate the existing tests first: `grep -rln "NewServiceManager" internal/pagewiki --include='*_test.go'`. Add the new tests to that file (matching its package clause); if none exists, create `internal/pagewiki/service_manager_test.go` with package `pagewiki_test`.

**Interfaces:**
- Consumes: Task 2's guarantee that `RepositoryResolver` (the func wrapping `RepositoryManager.ForScope`) blocks only same-scope callers.
- Produces: `ForScope(ctx, scopeID) (*Service, error)` — signature unchanged. Guarantees: different scopes never block each other; same-scope concurrent first-touch builds one Service; failed resolution not cached; `Start`'s exactly-once maintenance contract preserved (services created before `Start` get their loops started by `Start`; services created after start them at creation).

- [ ] **Step 1: Write the failing tests**

Using the existing fakes in the located test file where possible (a `RepositoryResolver` is just a func; `pagewiki.SessionDocumentPlanner{}`/`pagewiki.SessionDocumentEditor{}` are valid zero-config Planner/Editor). Qualify names per the file's package clause — the code below assumes external `pagewiki_test`; drop the `pagewiki.` qualifier if the file is in-package. A fake `Repository` implementation may already exist in the package's tests (`grep -n "Repository interface" internal/pagewiki/*.go` for the method set; `grep -rn "pagewiki.Repository = " internal/pagewiki --include='*_test.go'` for existing fakes) — reuse it; the resolver below calls it `fakeRepository{}`.

```go
func TestServiceForScopeColdResolutionDoesNotBlockOtherScopes(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	manager, err := pagewiki.NewServiceManager(
		func(_ context.Context, scopeID string) (pagewiki.Repository, error) {
			if scopeID == "cold" {
				close(entered)
				<-release
			}
			return &fakeRepository{}, nil
		},
		pagewiki.ServiceManagerConfig{
			Planner: pagewiki.SessionDocumentPlanner{},
			Editor:  pagewiki.SessionDocumentEditor{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = manager.ForScope(context.Background(), "cold") }()
	<-entered

	done := make(chan struct{})
	go func() {
		if _, err := manager.ForScope(context.Background(), "hot"); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hot scope blocked behind cold scope's repository resolution")
	}
	close(release)
}

func TestServiceForScopeResolvesConcurrentFirstTouchExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	manager, err := pagewiki.NewServiceManager(
		func(context.Context, string) (pagewiki.Repository, error) {
			calls.Add(1)
			time.Sleep(50 * time.Millisecond)
			return &fakeRepository{}, nil
		},
		pagewiki.ServiceManagerConfig{
			Planner: pagewiki.SessionDocumentPlanner{},
			Editor:  pagewiki.SessionDocumentEditor{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	services := make([]*pagewiki.Service, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			service, forErr := manager.ForScope(context.Background(), "scope-a")
			if forErr != nil {
				t.Error(forErr)
				return
			}
			services[index] = service
		}(i)
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("resolver ran %d times, want 1", calls.Load())
	}
	if services[0] == nil || services[0] != services[1] {
		t.Fatal("concurrent first-touch must return the same cached Service")
	}
}
```

- [ ] **Step 2: Run to verify the blocking test fails**

Run: `go test -race ./internal/pagewiki -run 'TestServiceForScope' -count=1 -timeout 60s`
Expected: `TestServiceForScopeColdResolutionDoesNotBlockOtherScopes` FAILS ("hot scope blocked…") — today's code holds `m.mu` across resolution. (The exactly-once test passes today; it pins behavior the rework must keep.)

- [ ] **Step 3: Implement**

Rewrite `ServiceManager` with the Task 2 pattern plus the `Start` contract. The subtle part: registration into `m.services` and the `started` snapshot must happen under one `m.mu` hold, so a Service is started exactly once whether `Start` lands before or after its creation:

```go
// serviceEntry is one scope's construction slot: its mutex serializes
// building that scope's Service (including the repository resolution, which
// may hydrate) so different scopes never wait on each other.
type serviceEntry struct {
	mu      sync.Mutex
	service *Service // nil until built; resolution errors are not cached
}

type ServiceManager struct {
	repositories RepositoryResolver
	config       ServiceManagerConfig

	mu             sync.Mutex
	entries        map[string]*serviceEntry
	services       map[string]*Service // for Start: services built before it ran
	started        bool
	maintenanceCtx context.Context
}
```

`NewServiceManager` initializes both maps (keep its nil-resolver validation). `ForScope`:

```go
func (m *ServiceManager) ForScope(ctx context.Context, scopeID string) (*Service, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("resolve pagewiki service: scope ID is required")
	}
	m.mu.Lock()
	entry, ok := m.entries[scopeID]
	if !ok {
		entry = &serviceEntry{}
		m.entries[scopeID] = entry
	}
	m.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.service != nil {
		return entry.service, nil
	}
	repository, err := m.repositories(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("resolve pagewiki repository for scope %s: %w", scopeID, err)
	}
	service := NewService(repository, m.config.Planner, m.config.Editor, m.config.Options...)

	// Register and snapshot the started flag under one mu hold: if Start has
	// not run yet it will start this service's maintenance (it is in
	// m.services); if it has, we start it here. Exactly one side fires.
	m.mu.Lock()
	m.services[scopeID] = service
	started, maintenanceCtx := m.started, m.maintenanceCtx
	m.mu.Unlock()
	if started {
		service.StartTreeMaintenance(maintenanceCtx)
		service.StartCurationMaintenance(maintenanceCtx)
	}
	entry.service = service
	return service, nil
}
```

`Start` keeps its current body (set `started`/`maintenanceCtx`, start loops for everything in `m.services`, all under `m.mu`). Update the type's doc comment: per-scope single-flight, mirroring `postgres.RepositoryManager`; `Start` contract unchanged.

- [ ] **Step 4: Run the package tests**

Run: `go test -race ./internal/pagewiki -count=1`
Expected: PASS — new tests plus every existing ServiceManager/Service test.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/service_manager.go internal/pagewiki/*_test.go
git commit -m "fix(pagewiki): service manager builds per-scope single-flight, scopes no longer block each other"
```

---

### Task 4: Controller lock decomposition — per-scope locks, scheduling unchanged

Split the process-global `Controller.mu` into per-scope locks so one tenant's minutes-long LLM injection no longer blocks another tenant's manual `InjectSession` or rebuild. The tick still runs `maybeRebuild` + serial scan on the consumer goroutine — the pool arrives in Task 5.

**Files:**
- Modify: `internal/pagewiki/sessionconsumer/consumer.go`
- Test: `internal/pagewiki/sessionconsumer/consumer_test.go`

**Interfaces:**
- Consumes: Tasks 2-3 (resolution outside locks is only safe because managers are now per-scope single-flight).
- Produces: unchanged public API (`New`, `Start`, `Status`, `SetAutoInject`, `Rebuild`, `InjectSession`). Internal shape Task 5 builds on: `scopeLock(scopeID string) *sync.Mutex`, `rebuildQueuedFor(scopeID string) bool`, `clearFailure(stream Stream)`, `clearScopeFailures(scopeID string)`, `failuresMu sync.Mutex`, and `consume` that resolves its injector BEFORE taking the scope lock.

- [ ] **Step 1: Write the failing cross-scope tests**

Add to `consumer_test.go`. The suite's fakes: `scopeResolver.setInjector(scopeID, injector)` registers a per-scope injector; `recordingInjector` supports `entered`/`release` channels (see `TestRebuildReturnsImmediatelyAndMergesWhileInjectionHoldsLock` for the blocking pattern). First check the fake store's `StreamsBySessionID` filters by BOTH scope and session (`grep -n "StreamsBySessionID" internal/pagewiki/sessionconsumer/consumer_test.go`); if it ignores scope, fix the fake to filter `stream.ScopeID == scopeID && stream.Actor.SessionID == sessionID` — Task 4's tests put streams for two scopes in the store.

```go
// TestManualInjectionOfOneScopeDoesNotWaitForAnother pins the per-scope
// lock split: while scope-a's injection sits inside a slow LLM call,
// scope-b's manual injection must complete instead of queueing behind a
// process-global mutex.
func (s *consumerSuite) TestManualInjectionOfOneScopeDoesNotWaitForAnother() {
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
		{ScopeID: "scope-b", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-b"}, Head: 2},
	}
	blocked := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.resolver.setInjector("scope-a", blocked)

	go func() { _, _ = s.consumer.InjectSession(context.Background(), "scope-a", "session-a") }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a injection did not start")
		return
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.consumer.InjectSession(context.Background(), "scope-b", "session-b")
		done <- err
	}()
	select {
	case err := <-done:
		s.Require().NoError(err)
	case <-time.After(time.Second):
		s.Fail("scope-b's manual injection blocked behind scope-a's")
	}
	close(blocked.release)
}

// TestRebuildOfOneScopeDoesNotWaitForAnotherScopesInjection pins the same
// split for rebuilds: scope-b's queued rebuild must execute while scope-a
// holds its own injection lock.
func (s *consumerSuite) TestRebuildOfOneScopeDoesNotWaitForAnotherScopesInjection() {
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
	}
	blocked := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.resolver.setInjector("scope-a", blocked)
	go func() { _, _ = s.consumer.InjectSession(context.Background(), "scope-a", "session-a") }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a injection did not start")
		return
	}

	_, err := s.consumer.Rebuild(context.Background(), "scope-b", time.Time{})
	s.Require().NoError(err)
	rebuildDone := make(chan struct{})
	go func() {
		s.consumer.RunQueuedRebuildForTest(context.Background())
		close(rebuildDone)
	}()
	select {
	case <-rebuildDone:
		s.Require().Len(s.rebuilder.callsForScope("scope-b"), 1)
	case <-time.After(time.Second):
		s.Fail("scope-b's rebuild blocked behind scope-a's injection")
	}
	close(blocked.release)
}
```

If `recordingInjector`'s `entered`/`release` fields differ from this shape, adapt the test to the fake's actual API rather than changing the fake.

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/pagewiki/sessionconsumer -run 'TestConsumerSuite/(TestManualInjectionOfOneScope|TestRebuildOfOneScope)' -count=1 -timeout 60s`
Expected: both FAIL by timeout message — today one `c.mu` serializes everything.

- [ ] **Step 3: Implement the lock split**

In `consumer.go`:

1. Controller struct: delete `mu sync.Mutex`; add:

```go
	// scopeLocks serializes, per scope, the three operations that mutate a
	// scope's wiki: scan-driven injection, manual InjectSession, and rebuild.
	// Scopes never contend with each other; within a scope injection stays
	// strictly serial (the wiki mirror is per-scope in-memory state).
	scopeLocksMu sync.Mutex
	scopeLocks   map[string]*sync.Mutex
	// failuresMu guards failures; it is its own small lock so backoff
	// bookkeeping never waits behind an in-flight injection.
	failuresMu sync.Mutex
```

Initialize `scopeLocks: make(map[string]*sync.Mutex)` in `New`.

2. Add helpers:

```go
func (c *Controller) scopeLock(scopeID string) *sync.Mutex {
	c.scopeLocksMu.Lock()
	defer c.scopeLocksMu.Unlock()
	lock, ok := c.scopeLocks[scopeID]
	if !ok {
		lock = &sync.Mutex{}
		c.scopeLocks[scopeID] = lock
	}
	return lock
}

// rebuildQueuedFor reports whether scopeID's own rebuild is waiting. The
// scan yields only that scope's streams to it; other scopes are unaffected.
func (c *Controller) rebuildQueuedFor(scopeID string) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	entry, found := c.rebuilds[scopeID]
	return found && entry.status.State == RebuildQueued
}

func (c *Controller) clearFailure(stream Stream) {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	delete(c.failures, streamKey(stream))
}

func (c *Controller) clearScopeFailures(scopeID string) {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	prefix := scopeID + "/"
	for key := range c.failures {
		if strings.HasPrefix(key, prefix) {
			delete(c.failures, key)
		}
	}
}
```

Delete the now-global `rebuildQueued()`.

3. `backedOff` and `recordFailure` take `c.failuresMu` internally (lock at the top of each, defer unlock; bodies otherwise unchanged).

4. `consume`: resolve the injector BEFORE taking the scope lock, then hold that scope's lock for the read-inject-advance critical section:

```go
func (c *Controller) consume(ctx context.Context, stream Stream) error {
	ctx = session.WithScope(ctx, stream.ScopeID)
	// Resolve before locking: resolution may hydrate a cold scope (a
	// startup-class cost), and per-scope single-flight in the managers means
	// only this scope's callers wait on it — never the lock's other holders.
	injector, err := c.injectorFor(ctx, stream.ScopeID)
	if err != nil {
		return fmt.Errorf("resolve Page Wiki injector: %w", err)
	}
	lock := c.scopeLock(stream.ScopeID)
	lock.Lock()
	defer lock.Unlock()
	events, err := c.store.SessionEvents(ctx, stream)
	...
}
```

(Rest of the body verbatim from today's `consume`, minus the old mid-body resolution and `session.WithScope` line, which moved up.)

5. `InjectSession`: drop `c.mu.Lock()/defer Unlock()`; the loop body becomes `c.clearFailure(stream)` followed by `c.consume(ctx, stream)` — each `consume` takes the scope lock itself.

6. `scan`: drop `c.mu`; the per-stream yield check becomes per-scope-and-continue (skip a rebuilding scope's streams, keep sweeping the others):

```go
	for _, stream := range streams {
		// A queued rebuild wipes everything this scope's injections would
		// build; skip that scope's streams and keep sweeping the others.
		// The rebuild itself runs on the next tick's maybeRebuild pass.
		if c.rebuildQueuedFor(stream.ScopeID) {
			continue
		}
		if c.backedOff(stream, now) {
			continue
		}
		if err := c.consume(ctx, stream); err != nil { ... continue }
		c.clearFailure(stream)
	}
```

(`delete(c.failures, streamKey(stream))` on success becomes `c.clearFailure(stream)`.)

7. `rebuildScope`: replace `c.mu.Lock()/defer` with the scope's lock, and the inline prefix-delete loop with `c.clearScopeFailures(scopeID)`:

```go
	lock := c.scopeLock(scopeID)
	lock.Lock()
	defer lock.Unlock()
	if err := rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion, since); err != nil {
		return err
	}
	c.clearScopeFailures(scopeID)
	return nil
```

- [ ] **Step 4: Run the full package with race detector**

Run: `go test -race ./internal/pagewiki/sessionconsumer -count=1 -timeout 300s`
Expected: PASS — the two new tests plus every existing test (same-scope serialization pins like `TestRebuildReturnsImmediatelyAndMergesWhileInjectionHoldsLock` must still hold: same scope still queues behind its own lock). Postgres-backed tests in the package skip without the DSN — run them too:
`TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test -race -p 1 ./internal/pagewiki/sessionconsumer -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/sessionconsumer/consumer.go internal/pagewiki/sessionconsumer/consumer_test.go
git commit -m "fix(pagewiki): split the consumer's global mutex into per-scope locks"
```

---

### Task 5: Bounded worker pool — scope jobs, K default 2

Replace the serial tick with dispatch: pending streams group into per-scope jobs that run through a semaphore of capacity K (default 2). Scope A's rebuild occupies one slot while scope B injects in another; rebuilds no longer batch-drain before scanning resumes.

**Files:**
- Modify: `internal/pagewiki/sessionconsumer/consumer.go`
- Modify: `internal/pagewiki/sessionconsumer/export_test.go`
- Test: `internal/pagewiki/sessionconsumer/consumer_test.go`

**Interfaces:**
- Consumes: Task 4's `scopeLock`, `rebuildQueuedFor`, `clearFailure`, per-scope `consume`.
- Produces: `type Option func(*Controller) error` and `WithInjectConcurrency(k int) Option`; `New(store, injectorFor, rebuilderFor, logger, interval, options ...Option)` — variadic, so every existing call site compiles unchanged; default concurrency 2, `k < 1` → `New` returns an error. Test hooks: `RunQueuedRebuildForTest(ctx)` KEEPS its rebuild-only semantics (integration tests depend on it not injecting); new `DispatchTickForTest(ctx)` (dispatch only) and `WaitJobsForTest()` (drain).

- [ ] **Step 1: Write the failing pool tests**

Add to `consumer_test.go`:

```go
// TestTwoScopesInjectInParallelWithinTheConcurrencyCap pins the K=2 pool:
// both scopes' injections must be in flight at the same time.
func (s *consumerSuite) TestTwoScopesInjectInParallelWithinTheConcurrencyCap() {
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
		{ScopeID: "scope-b", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-b"}, Head: 2},
	}
	blockedA := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	blockedB := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.resolver.setInjector("scope-a", blockedA)
	s.resolver.setInjector("scope-b", blockedB)

	s.consumer.DispatchTickForTest(context.Background())

	for name, entered := range map[string]chan struct{}{"scope-a": blockedA.entered, "scope-b": blockedB.entered} {
		select {
		case <-entered:
		case <-time.After(time.Second):
			s.Failf("not parallel", "%s's injection never started while the other scope held a slot", name)
		}
	}
	close(blockedA.release)
	close(blockedB.release)
	s.consumer.WaitJobsForTest()
}

// TestConcurrencyOfOneKeepsScopesSerial pins WithInjectConcurrency(1): the
// pool structure exists but only one scope's job may run at a time.
func (s *consumerSuite) TestConcurrencyOfOneKeepsScopesSerial() {
	consumer, err := sessionconsumer.New(
		s.store, s.resolver.injectorFor, s.resolver.rebuilderFor,
		slog.New(slog.DiscardHandler), time.Hour,
		sessionconsumer.WithInjectConcurrency(1),
	)
	s.Require().NoError(err)
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
		{ScopeID: "scope-b", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-b"}, Head: 2},
	}
	blockedA := &recordingInjector{entered: make(chan struct{}, 1), release: make(chan struct{})}
	recordedB := &recordingInjector{entered: make(chan struct{}, 1)}
	s.resolver.setInjector("scope-a", blockedA)
	s.resolver.setInjector("scope-b", recordedB)

	consumer.DispatchTickForTest(context.Background())
	select {
	case <-blockedA.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a's injection never started")
		return
	}
	select {
	case <-recordedB.entered:
		s.Fail("K=1 must not run a second scope while the first holds the slot")
	case <-time.After(100 * time.Millisecond):
	}
	close(blockedA.release)
	consumer.WaitJobsForTest()
	select {
	case <-recordedB.entered:
	case <-time.After(time.Second):
		s.Fail("scope-b's injection never ran after the slot freed")
	}
}

// TestTickSkipsAScopeWhoseJobIsStillInFlight pins the dedup: a tick firing
// while a scope's job runs must not pile a second job onto that scope.
func (s *consumerSuite) TestTickSkipsAScopeWhoseJobIsStillInFlight() {
	s.store.streams = []sessionconsumer.Stream{
		{ScopeID: "scope-a", Actor: session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-a"}, Head: 2},
	}
	blocked := &recordingInjector{entered: make(chan struct{}, 4), release: make(chan struct{})}
	s.resolver.setInjector("scope-a", blocked)

	s.consumer.DispatchTickForTest(context.Background())
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		s.Fail("scope-a's injection never started")
		return
	}
	s.consumer.DispatchTickForTest(context.Background())
	s.consumer.DispatchTickForTest(context.Background())
	select {
	case <-blocked.entered:
		s.Fail("a second job entered scope-a while the first was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(blocked.release)
	s.consumer.WaitJobsForTest()
}

func (s *consumerSuite) TestNewRejectsNonPositiveConcurrency() {
	_, err := sessionconsumer.New(
		s.store, s.resolver.injectorFor, s.resolver.rebuilderFor,
		slog.New(slog.DiscardHandler), time.Hour,
		sessionconsumer.WithInjectConcurrency(0),
	)
	s.Require().ErrorContains(err, "concurrency")
}
```

Note on the fakes: `consumerStore.SessionEvents`/`AdvanceCursor` may be written for one stream at a time; make sure they are safe under two concurrent jobs (guard the fake's mutable fields with a mutex if it doesn't have one — `-race` will tell you).

- [ ] **Step 2: Run to verify they fail to compile**

Run: `go test -race ./internal/pagewiki/sessionconsumer -run 'TestConsumerSuite/(TestTwoScopes|TestConcurrencyOfOne|TestTickSkips|TestNewRejects)' -count=1`
Expected: compile FAIL — `WithInjectConcurrency`, `DispatchTickForTest`, `WaitJobsForTest` don't exist.

- [ ] **Step 3: Implement the pool**

In `consumer.go`:

1. Controller gains:

```go
	// slots is the injection worker pool: a job (one scope's rebuild + its
	// pending streams, in order) holds one slot for its whole run. Capacity
	// is WithInjectConcurrency's K — the process-wide ceiling on concurrent
	// LLM injection spend. Within a scope everything stays serial.
	slots chan struct{}
	// inFlight dedups jobs per scope: a tick firing while a scope's job is
	// still running skips that scope instead of queueing behind it.
	inFlightMu sync.Mutex
	inFlight   map[string]bool
	jobs       sync.WaitGroup
```

2. Options and `New`:

```go
type Option func(*Controller) error

// WithInjectConcurrency caps how many scope jobs run at once (default 2).
// K is a spend ceiling, not a fairness knob: a single-scope deployment
// only ever produces one job regardless of K.
func WithInjectConcurrency(k int) Option {
	return func(c *Controller) error {
		if k < 1 {
			return fmt.Errorf("create Page Wiki session consumer: inject concurrency must be >= 1, got %d", k)
		}
		c.slots = make(chan struct{}, k)
		return nil
	}
}
```

`New` grows a trailing `options ...Option`, sets `slots: make(chan struct{}, 2)` and `inFlight: make(map[string]bool)` in the literal, then applies options in order, returning the first error.

3. Replace `tick`/`scan`/`maybeRebuild` scheduling (the old `scan` body was absorbed into `runScopeJob` in Task 4 form; `maybeRebuild` survives only as the test hook's rebuild-only driver):

```go
type scopeJob struct {
	scopeID string
	streams []Stream
}

func (c *Controller) tick(ctx context.Context) {
	streams, err := c.store.PendingStreams(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "Page Wiki scan failed", "error", err)
		// Queued rebuilds must still run even when the scan query fails.
		streams = nil
	}
	for _, job := range c.buildScopeJobs(streams) {
		if !c.markInFlight(job.scopeID) {
			continue
		}
		c.jobs.Add(1)
		go c.runScopeJob(ctx, job)
	}
}

// buildScopeJobs groups the pending streams by scope, preserving the
// store's fairness-interleaved order both across jobs (first appearance)
// and within each job. Scopes with a queued rebuild but no pending streams
// get a rebuild-only job.
func (c *Controller) buildScopeJobs(streams []Stream) []scopeJob {
	jobs := make([]scopeJob, 0, len(streams))
	index := make(map[string]int)
	for _, stream := range streams {
		at, seen := index[stream.ScopeID]
		if !seen {
			at = len(jobs)
			index[stream.ScopeID] = at
			jobs = append(jobs, scopeJob{scopeID: stream.ScopeID})
		}
		jobs[at].streams = append(jobs[at].streams, stream)
	}
	for _, scopeID := range c.queuedRebuildScopes() {
		if _, seen := index[scopeID]; !seen {
			jobs = append(jobs, scopeJob{scopeID: scopeID})
		}
	}
	return jobs
}

func (c *Controller) markInFlight(scopeID string) bool {
	c.inFlightMu.Lock()
	defer c.inFlightMu.Unlock()
	if c.inFlight[scopeID] {
		return false
	}
	c.inFlight[scopeID] = true
	return true
}

func (c *Controller) clearInFlight(scopeID string) {
	c.inFlightMu.Lock()
	defer c.inFlightMu.Unlock()
	delete(c.inFlight, scopeID)
}

// runScopeJob is one scope's turn: rebuild first if queued, then the
// scope's streams in order. It holds one pool slot for its whole run and
// yields to a rebuild queued mid-pass — the trigger re-arm at the end
// guarantees the yielded-to rebuild is picked up promptly even with a long
// tick interval.
func (c *Controller) runScopeJob(ctx context.Context, job scopeJob) {
	defer c.jobs.Done()
	defer c.clearInFlight(job.scopeID)
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-c.slots }()

	if c.rebuildQueuedFor(job.scopeID) {
		c.runScopeRebuild(ctx, job.scopeID)
	}
	now := c.now()
	for _, stream := range job.streams {
		if ctx.Err() != nil {
			return
		}
		if c.rebuildQueuedFor(job.scopeID) {
			break
		}
		if c.backedOff(stream, now) {
			continue
		}
		if err := c.consume(ctx, stream); err != nil {
			record := c.recordFailure(stream)
			c.logger.WarnContext(ctx, "Page Wiki session injection failed",
				"scope_id", stream.ScopeID,
				"agent_id", stream.Actor.AgentID,
				"session_id", stream.Actor.SessionID,
				"attempts", record.attempts,
				"next_retry_at", record.nextRetryAt,
				"error", err,
			)
			continue
		}
		c.clearFailure(stream)
	}
	if c.rebuildQueuedFor(job.scopeID) {
		c.ping()
	}
}

// ping wakes the consumer loop; the one-slot buffer means a wakeup during
// an in-flight tick is remembered, not lost.
func (c *Controller) ping() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}
```

Delete the standalone `scan` (its body now lives in `runScopeJob`) and rewrite `maybeRebuild` as the rebuild-only pass the export hook keeps using:

```go
// maybeRebuild drains every queued scope rebuild on the caller's
// goroutine. Production ticks fold rebuilds into scope jobs; this serial
// path remains for the deterministic test driver.
func (c *Controller) maybeRebuild(ctx context.Context) {
	for _, scopeID := range c.queuedRebuildScopes() {
		c.runScopeRebuild(ctx, scopeID)
	}
}
```

Replace `Rebuild`'s inline trigger-send with `c.ping()`.

4. `export_test.go`:

```go
// RunQueuedRebuildForTest drives queued rebuilds only — no injection — so
// DB-backed tests can assert post-rebuild state without racing a scan.
func (c *Controller) RunQueuedRebuildForTest(ctx context.Context) {
	c.maybeRebuild(ctx)
}

// DispatchTickForTest runs one tick (dispatch only; jobs run async).
func (c *Controller) DispatchTickForTest(ctx context.Context) {
	c.tick(ctx)
}

// WaitJobsForTest blocks until every dispatched scope job has finished.
func (c *Controller) WaitJobsForTest() {
	c.jobs.Wait()
}
```

- [ ] **Step 4: Reconcile the existing suite with async ticks**

Run: `go test -race ./internal/pagewiki/sessionconsumer -count=1 -timeout 300s`

Expected trouble spots — fix the tests' synchronization, not their assertions:
- `TestRebuildQueuesAndRunsInBackground` asserts `entries[0] == "rebuild"` (rebuild before that scope's injection) — still true inside one scope's job; keep as is.
- `TestRebuildReturnsImmediatelyAndMergesWhileInjectionHoldsLock`: the scan job yields to the mid-pass queued rebuild and re-arms the trigger; the rebuild then runs on the next tick's job. The existing `s.rebuilder.done` wait still works because Start's loop consumes the re-armed trigger. If the suite's `interval` (`time.Hour`) makes any existing Start-based test hang, the trigger re-arm is broken — fix the code, not the test.
- Any test that previously relied on `tick` being synchronous must use `DispatchTickForTest` + `WaitJobsForTest` (or the channels it already waits on).

Then the DB-backed suites:
`TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test -race -p 1 ./internal/pagewiki/sessionconsumer -count=1`
Expected: PASS — `TestRebuildClearsDerivedWikiAndMakesSessionPendingAgain` in particular (it depends on `RunQueuedRebuildForTest` NOT injecting).

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/sessionconsumer
git commit -m "feat(pagewiki): consumer dispatches per-scope jobs through a bounded worker pool"
```

---

### Task 6: Wire `TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY`

**Files:**
- Modify: `internal/app/config.go` (config struct ~:77, `loadConfig` env reads ~:91)
- Modify: `internal/app/wiring.go` (`sessionconsumer.New` call at ~:102)
- Modify: `.env.example`
- Test: `internal/app` — add to the existing config/wiring test file (locate: `grep -rln "parseNonNegativeEnvironment\|loadConfig" internal/app --include='*_test.go'`; add to that file, matching its package clause)

**Interfaces:**
- Consumes: Task 5's `WithInjectConcurrency(k int) Option`.
- Produces: `parsePageWikiInjectConcurrency(raw string) (int, error)` in `internal/app` (unexported); env contract: unset/blank → 2, integer ≥ 1 → that value, anything else → startup error naming the variable.

- [ ] **Step 1: Write the failing parse tests**

```go
func TestParsePageWikiInjectConcurrency(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "unset defaults to two", raw: "", want: 2},
		{name: "blank defaults to two", raw: "   ", want: 2},
		{name: "explicit value", raw: "3", want: 3},
		{name: "one is valid", raw: "1", want: 1},
		{name: "zero rejected", raw: "0", wantErr: true},
		{name: "negative rejected", raw: "-2", wantErr: true},
		{name: "garbage rejected", raw: "many", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePageWikiInjectConcurrency(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parse %q: want error, got %d", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parse %q = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
```

(If the located test file is external package `app_test`, the helper must instead be exercised through `loadConfig`/`Run` — in that case put this test in a new in-package file `internal/app/wiring_internal_test.go` with package `app`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app -run TestParsePageWikiInjectConcurrency -count=1`
Expected: compile FAIL — helper undefined.

- [ ] **Step 3: Implement**

`config.go`: add field `pagewikiInjectConcurrency string` to the config struct and `pagewikiInjectConcurrency: os.Getenv("TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY"),` to `loadConfig`, next to the other pagewiki settings.

`wiring.go`: next to `parseNonNegativeEnvironment`:

```go
// parsePageWikiInjectConcurrency reads the injection worker-pool cap.
// Unset means 2 — the conservative multi-tenant default; single-scope
// deployments only ever produce one job so the value is inert on-prem.
func parsePageWikiInjectConcurrency(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 2, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY must be a positive integer, got %q", raw)
	}
	return value, nil
}
```

At the `sessionconsumer.New` call site:

```go
	injectConcurrency, err := parsePageWikiInjectConcurrency(config.pagewikiInjectConcurrency)
	if err != nil {
		return nil, nil, nil, err
	}
	controller, err := sessionconsumer.New(
		consumerStore,
		func(ctx context.Context, scopeID string) (sessionconsumer.Injector, error) {
			return serviceManager.ForScope(ctx, scopeID)
		},
		func(ctx context.Context, scopeID string) (sessionconsumer.Rebuilder, error) {
			return repositoryManager.ForScope(ctx, scopeID)
		},
		logger, 2*time.Second,
		sessionconsumer.WithInjectConcurrency(injectConcurrency),
	)
```

`.env.example`: add (commented, since the default stands alone):

```
# Max concurrent Page Wiki injections across scopes (default 2; >=1).
# TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY=2
```

- [ ] **Step 4: Verify**

Run: `go test ./internal/app -count=1 && go build ./... && go vet ./...`
Expected: PASS / clean.

- [ ] **Step 5: Commit**

```bash
git add internal/app/config.go internal/app/wiring.go internal/app/*_test.go .env.example
git commit -m "feat(app): TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY caps the injection worker pool"
```

---

### Task 7: Full gate and PR

- [ ] **Step 1: Run the full gate**

Run: `make lint && make db-up && make coverage && make integration-test`
Expected: all green, or failing only in ways matching the baseline recorded before Task 1. For DB-backed packages prefer `-p 1` if parallel suites contend.

- [ ] **Step 2: Push and open the PR**

```bash
git push -u origin feat/pagewiki-multitenant-hardening
gh pr create --title "fix(pagewiki): multi-tenant hardening — per-scope fairness, single-flight hydration, bounded injection pool" --body "$(cat <<'EOF'
The three pre-multi-tenant gate items from PR #68's checklist, per
docs/superpowers/specs/2026-08-02-multitenant-hardening-design.md:

- PendingStreams caps each scope at 20 of the 100 slots per scan and
  interleaves scopes, so a scope with >=100 permanently failing streams can
  no longer starve every other tenant. (The same-shaped query in
  internal/platform/postgres/audit.go is deliberately untouched: the audit
  consumer is read-only risk classification where a delayed round is
  harmless.)
- RepositoryManager and ServiceManager hydrate per-scope single-flight: a
  cold scope's full-mirror hydration no longer blocks hot scopes' HTTP or
  the consumer. Failed hydrations retry.
- The consumer's process-global mutex is gone: per-scope locks serialize a
  scope's injection/manual-inject/rebuild, and ticks dispatch per-scope
  jobs through a bounded worker pool (TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY,
  default 2). Rebuilds ride in their scope's job instead of batch-draining
  before the scan. Injector/rebuilder resolution happens before any lock.

Single-scope on-prem behavior is unchanged: one scope produces one job, so
injection stays serial regardless of K.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Do not merge — the user merges PRs in this repo.

---

## Self-Review Notes

- Spec coverage: §1 fairness → Task 1; §2 manager single-flight → Tasks 2-3; §3 per-scope locks / pool / in-flight dedup / rebuild-in-job / resolution-before-lock → Tasks 4-5; configuration → Task 6 (spec's "parseNonNegativeEnvironment pattern" is honored in placement, but the helper is bespoke because ≥1 ≠ non-negative); spec testing section → each task's Step 1 plus Task 7. Out-of-scope list respected (no eviction, no scope registry, no transport auth, audit query untouched).
- The trigger re-arm in `runScopeJob` (absent from the spec's prose but implied by its yield semantics) is load-bearing: without it, a yield with a long tick interval strands the queued rebuild — pinned by `TestRebuildReturnsImmediatelyAndMergesWhileInjectionHoldsLock` under Start's 1-hour interval.
- `RunQueuedRebuildForTest` keeps rebuild-only semantics deliberately: `TestRebuildClearsDerivedWikiAndMakesSessionPendingAgain` asserts zero cursors right after the rebuild, which a tick-driven injection would break.
- Type consistency: `Option func(*Controller) error` (Task 5) matches Task 6's `WithInjectConcurrency` usage; `repositoryEntry`/`serviceEntry` shapes are parallel by design; helpers named in Task 4's Produces block are the ones Task 5's code calls.
