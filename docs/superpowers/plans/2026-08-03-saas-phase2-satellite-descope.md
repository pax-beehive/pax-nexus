# SaaS Phase 2: Satellite De-Scoping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land Phase 2 of the on-prem/SaaS split ADR: the five remaining construction-time scope bindings (todo repository, todo note directory, lake reporter, pagewiki repository, PageWiki consumer store) become scope-per-call or scope-managed, and the two per-scope background loops (todo suggestion refresh, session-consumer scan) become scope-sweeping — one process correctly serving N scopes.

**Architecture:** Two different target shapes, dictated by the code. The **todoapp side** mirrors `NoteStore`/`evidencelake.Lake`: drop the `scopeID` field, add `scopeID string` as an explicit parameter to every method, thread `principal.ScopeID` from the transport. The **pagewiki side** cannot do that — `pagewikipostgres.Repository` hydrates a per-scope in-memory mirror (`memory.Repository`) at construction — so it gets a **lazy per-scope instance manager** (`ForScope(ctx, scopeID)`) for repository and service, and the `sessionconsumer.Controller` becomes multi-scope internally (all-scope scan, per-scope rebuild slots, scope-prefixed failure resets) resolving its per-scope dependencies through the managers. On-prem wiring eagerly resolves `LocalScopeID` at boot so startup behavior (hydrate-or-fail-fast) is unchanged.

**Tech Stack:** Go 1.25, pgx/pgxpool, Hertz, testify suites, golangci-lint v2 (depguard active from Phase 1).

## Global Constraints

- Module path: `github.com/pax-beehive/pax-nexus`.
- Zero behavior change for the on-prem distribution: same env vars, same startup sequence (pagewiki hydration still happens at boot and still fails fast), same HTTP semantics. With exactly one scope in the DB, every loop does the same work as today.
- One PR for the whole phase; branch `feat/saas-phase2-satellite-descope` off current main (`b155675` or later).
- CI gates: `make lint` (0 issues; depguard is active — core packages must not import `internal/deployment`), `make coverage` (≥80%), `make integration-test`.
- Postgres-backed tests: `export TEAM_MEMORY_POSTGRES_PORT=55499` (sibling worktrees own 55432) then `make db-up`, and `export TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55499/team_memory?sslmode=disable'`. Suites skip silently without the DSN — verify tests RAN.
- Scope values in tests: use `"local-team"` and `"other-scope"` string literals in test fixtures (never import `onprem` into core-package tests — depguard exempts tests but the convention from Phase 1 is plain literals).
- Commit style: `type(scope): summary`, ending every commit message with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- Line numbers below were surveyed at main `b155675`; re-locate with the given greps if drifted.

## Design decisions locked by this plan

1. **todoapp = parameter style.** `todoapppostgres.Repository`, `postgres.TodoNoteDirectory`, `todoapp.LakeReporter`, and `todoapp.Service` all take `scopeID string` as the first parameter after `ctx` on every method. The `todoapp.Repository`/`NoteDirectory`/`Reporter`/`Service`-consumed interfaces change accordingly.
2. **pagewiki = manager style.** `pagewikipostgres.RepositoryManager.ForScope(ctx, scopeID)` lazily constructs+hydrates one `*Repository` per scope (existing `Repository` internals unchanged). `pagewiki.ServiceManager.ForScope(ctx, scopeID)` lazily builds one `*Service` per scope over the per-scope repository plus the global planner/editor/navigator/curator, and starts that scope's tree/curation maintenance on first creation.
3. **Controller becomes multi-scope**, not per-scope-managed: `PendingStreams` returns all scopes' backlogs; rebuild state is `map[scopeID]rebuildState`; a successful rebuild resets only `scopeID+"/"`-prefixed failure entries; per-scope injectors/rebuilders are resolved via function types backed by the managers.
4. **Scope enumeration comes from data, not config.** The todo refresh sweep lists distinct scopes from `team_notes`; the consumer scan discovers scopes from pending stream rows. No scope-registry abstraction until the Phase 3 control plane provides one.
5. **`pagewikihttp` and `handler.WikiSettings` resolve scope per request.** `WikiSettings` methods gain a `scopeID` param (endpoints pass `principal.ScopeID`, exactly like `WikiControl` does since Phase 1). `pagewikihttp.Handler` gets a `Dependencies func(ctx) (Injector, Reader, error)` closure; on-prem wiring pins it to `LocalScopeID`. Full per-request auth on the pagewiki transport is Phase 3.

---

### Task 1: todoapp stores go scope-per-call

**Files:**
- Modify: `internal/todoapp/postgres/repository.go` (struct :19-22, ctor :25, methods :33,:51,:72,:102,:121,:142,:173)
- Modify: `internal/platform/postgres/todoapp_notes.go` (struct :15-18, ctor :21, method :30)
- Modify: `internal/todoapp/dependencies.go` (or wherever `todoapp.Repository` and `todoapp.NoteDirectory` interfaces live — find with `grep -rn "SuggestionFingerprints\|ListOpenActionItems" internal/todoapp/*.go | grep interface -A2` or read `internal/todoapp/service.go` imports)
- Test: `internal/todoapp/postgres/repository_test.go`, the platform postgres test file covering `TodoNoteDirectory` (find: `grep -rln "NewTodoNoteDirectory" internal/platform/postgres/*_test.go`)

**Interfaces:**
- Consumes: nothing.
- Produces (Task 2 compiles against these):
  - `func NewRepository(ctx context.Context, pool *pgxpool.Pool) (*Repository, error)` — no scope param.
  - Every repository method gains `scopeID string` immediately after `ctx`: `SaveTodo(ctx, scopeID, todo)`, `TodoByID(ctx, scopeID, todoID)`, `ListTodos(ctx, scopeID, status)`, `SaveSuggestion(ctx, scopeID, suggestion)`, `SuggestionByID(ctx, scopeID, suggestionID)`, `ListSuggestions(ctx, scopeID, status)`, `SuggestionFingerprints(ctx, scopeID)`.
  - `func NewTodoNoteDirectory(pool *pgxpool.Pool) (*TodoNoteDirectory, error)`; `ListOpenActionItems(ctx, scopeID string, limit int)`.
  - The `todoapp.Repository` and `todoapp.NoteDirectory` interface definitions updated to match.

- [ ] **Step 1: Update the two interface definitions** in the todoapp package to the new signatures above. This breaks compilation of `service.go` — expected; Task 1 only lands the store side, so ALSO mechanically update `internal/todoapp/service.go` call sites to pass a scope through: for this task only, change `Service` methods minimally by adding `scopeID string` parameters and threading them (the full Service/transport treatment with tests is Task 2, but the package must compile per-commit). Service method call-site mapping (from the survey): `CreateTodo`→`SaveTodo` (:98), `CompleteTodo`→`TodoByID`(:110)/`SaveTodo`(:124), `ListTodos`→`ListTodos`(:149), `RefreshSuggestions`→`ListOpenActionItems`(:161)/`SuggestionFingerprints`(:167)/`SaveSuggestion`(:208), `PendingSuggestions`→`ListSuggestions`(:221), `AcceptSuggestion`→`SuggestionByID`(:230)/`SaveTodo`(:256)/`SaveSuggestion`(:263), `DismissSuggestion`→`SuggestionByID`(:291)/`SaveSuggestion`(:306). The transport (`internal/todoapp/transport/httpapi/endpoints.go`) passes `principal.ScopeID` at each `h.service.X(...)` call and its `Service` interface (`dependencies.go:15-23`) gains the params — again minimal compile-fixing here; Task 2 owns the tests and the reporter.
  Reporter calls (`s.reporter.Report` at :138/:277/:320): for this task, keep `Reporter` interface unchanged by constructing the reporter per… **no** — simplest compile-stable point: change `Reporter` interface to `Report(ctx, scopeID string, event ReportEvent) error` in this task too and have `LakeReporter` accept-and-use it (Task 2 verifies with tests). `LakeReporter`: drop `scopeID` field and the `scopeID == ""` ctor validation (:39-44), `New` becomes `NewLakeReporter(sink EvidenceSink, opts ...lakeReporterOption)`, `Report(ctx, scopeID, event)` does `session.WithScope(ctx, scopeID)` (:58) and validates `scopeID != ""` → error.
- [ ] **Step 2: Migrate the two store types** — remove `scopeID` fields and ctor params; add `scopeID string` as first param after `ctx` to every method listed in Produces; inside each method replace `r.scopeID`/`d.scopeID` with the parameter (SQL text unchanged — the survey confirms scope always enters as `$1`/a bind arg, never interpolated). Update `internal/app/wiring.go:276,280,284` ctor calls (drop the scope arg) and the `buildTodoApp` plumbing so the on-prem scope now flows per-call from the transport (wiring itself no longer passes a scope here; if any wiring-level call still needs one — it shouldn't after Task 2's transport work — use `onprem.LocalScopeID`).
- [ ] **Step 3: Write the failing isolation tests.** In `internal/todoapp/postgres/repository_test.go` add (match the suite's existing fixture helpers):

```go
func (s *repositorySuite) TestScopeIsolationPerCall() {
	ctx := context.Background()
	s.Require().NoError(s.repo.SaveTodo(ctx, "local-team", todoapp.Todo{ID: "todo-a", Title: "A", Status: todoapp.TodoStatusOpen}))
	s.Require().NoError(s.repo.SaveTodo(ctx, "other-scope", todoapp.Todo{ID: "todo-b", Title: "B", Status: todoapp.TodoStatusOpen}))

	local, err := s.repo.ListTodos(ctx, "local-team", todoapp.TodoStatusOpen)
	s.Require().NoError(err)
	s.Require().Len(local, 1)
	s.Equal("todo-a", local[0].ID)

	other, err := s.repo.ListTodos(ctx, "other-scope", todoapp.TodoStatusOpen)
	s.Require().NoError(err)
	s.Require().Len(other, 1)
	s.Equal("todo-b", other[0].ID)
}
```

(Adapt `Todo` field names to the actual struct — read it first; the assertion pattern is the requirement.) Add the equivalent two-scope test for `ListOpenActionItems` in the `TodoNoteDirectory` test file, inserting `team_notes` fixtures under two scopes and asserting each scope sees only its own.
- [ ] **Step 4: Run and fix until green.** `make db-up` (with `TEAM_MEMORY_POSTGRES_PORT=55499`) then `go test ./internal/todoapp/... ./internal/platform/postgres -count=1 -run 'Todo|Repository'` with the DSN exported; then `go build ./...` and `go vet ./...`. Existing todoapp tests (service/transport) will need their fakes' signatures updated — mechanical, keep assertions intact.
- [ ] **Step 5: Commit** — `refactor(todoapp): stores and interfaces go scope-per-call` + trailer.

---

### Task 2: todoapp service, reporter, and transport carry the scope

Task 1 made it compile; this task makes it correct and tested.

**Files:**
- Modify: `internal/todoapp/service.go` (mutex :36, methods listed below), `internal/todoapp/report.go`
- Modify: `internal/todoapp/transport/httpapi/endpoints.go`, `internal/todoapp/transport/httpapi/dependencies.go`
- Test: `internal/todoapp/service_test.go`, `internal/todoapp/report_test.go`, `internal/todoapp/transport/httpapi/endpoints_test.go`

**Interfaces:**
- Consumes: Task 1's store/interface signatures.
- Produces: `Service` exported methods each take `scopeID string` after `ctx`: `CreateTodo(ctx, scopeID, userID, title, body)`, `CompleteTodo(ctx, scopeID, userID, todoID)`, `ListTodos(ctx, scopeID, status)`, `RefreshSuggestions(ctx, scopeID)`, `PendingSuggestions(ctx, scopeID)`, `AcceptSuggestion(ctx, scopeID, userID, suggestionID)`, `DismissSuggestion(ctx, scopeID, userID, suggestionID)`. Task 3 calls `RefreshSuggestions(ctx, scopeID)`.

- [ ] **Step 1: Per-scope refresh serialization.** Replace `mu sync.Mutex` (`service.go:36`) with a keyed mutex so scope A's refresh never blocks scope B:

```go
// refreshLocks serializes RefreshSuggestions per scope; two scopes must be
// able to refresh concurrently, while one scope's refresh stays single-flight.
refreshLocks sync.Map // scopeID -> *sync.Mutex
```

```go
func (s *Service) refreshLock(scopeID string) *sync.Mutex {
	lock, _ := s.refreshLocks.LoadOrStore(scopeID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}
```

`RefreshSuggestions(ctx, scopeID)` locks `s.refreshLock(scopeID)` where it locked `s.mu` before.
- [ ] **Step 2: Write the failing tests.**
  - Service: extend the existing service tests' fake repository to record the `scopeID` it receives per call; add a test that `CreateTodo(ctx, "other-scope", ...)` reaches the fake with `"other-scope"`, and a test that two concurrent `RefreshSuggestions` on different scopes both proceed (e.g. fake `ListOpenActionItems` for scope A blocks on a channel; assert scope B's refresh completes while A is blocked — use a `sync.WaitGroup` + buffered channel, no sleeps).
  - Reporter: in `report_test.go`, assert `Report(ctx, "other-scope", event)` produces a sink call whose context yields `"other-scope"` via `session.ScopeFromContext`, and `Report(ctx, "", event)` errors.
  - Transport: in `endpoints_test.go`, set the fake authenticator's principal `ScopeID: "other-scope"` and assert the fake Service receives `"other-scope"` (capture in the fake). This is the regression test Phase 1's final review asked for, now on the todoapp side.
- [ ] **Step 3: Run to verify the new assertions fail** against any not-yet-threaded path, implement the remaining threading (transport `authorize` already returns the principal; pass `principal.ScopeID` at every `h.service.X` call — routes at `endpoints.go:72,92,113,130,142,154,171`), and re-run until green: `go test ./internal/todoapp/... -count=1`.
- [ ] **Step 4: Commit** — `refactor(todoapp): thread principal scope through service, reporter, transport` + trailer.

---

### Task 3: todo suggestion refresh sweeps all scopes

**Files:**
- Modify: `internal/todoapp/scheduler.go` (:12-47)
- Create: scope lister in `internal/platform/postgres/todoapp_notes.go` (same file as the note directory — it is the same data source)
- Modify: `internal/app/wiring.go` (`buildTodoApp` :309 area)
- Test: `internal/todoapp/scheduler_test.go` (create if absent), platform postgres test for the lister

**Interfaces:**
- Consumes: Task 2's `RefreshSuggestions(ctx, scopeID)`.
- Produces: `type ScopeLister interface { ListScopes(ctx context.Context) ([]string, error) }` (defined in `internal/todoapp`); `func StartSuggestionRefresh(ctx context.Context, service *Service, scopes ScopeLister, interval time.Duration, logger *slog.Logger) func()`; `func (d *TodoNoteDirectory) ListScopes(ctx context.Context) ([]string, error)`.

- [ ] **Step 1: Failing lister test** (platform postgres suite): insert `team_notes` rows under `"local-team"` and `"other-scope"`, assert `ListScopes` returns exactly both (order-insensitive: sort before compare).
- [ ] **Step 2: Implement the lister** on `TodoNoteDirectory`:

```go
// ListScopes enumerates the scopes that currently have team notes — the
// population the suggestion-refresh sweep serves. Scope discovery is
// data-driven until the control plane provides a team registry (Phase 3).
func (d *TodoNoteDirectory) ListScopes(ctx context.Context) ([]string, error) {
	rows, err := d.pool.Query(ctx, `SELECT DISTINCT scope_id FROM team_notes`)
	...
}
```

(Standard rows-scan-close; wrap errors `fmt.Errorf("list todo scopes: %w", err)`.)
- [ ] **Step 3: Failing scheduler test** (`scheduler_test.go`, package todoapp, fake service? — `StartSuggestionRefresh` takes `*Service`, so build a real `Service` over fakes): fake lister returns `["scope-a","scope-b"]`; fake repository records which scopes' `SuggestionFingerprints`/`ListOpenActionItems` were called; run `StartSuggestionRefresh` with a tiny interval, wait for ≥1 sweep via channel signaling from the fake (never `time.Sleep` polling), assert both scopes were refreshed and that a scope-a error (fake returns an error for scope-a only) did not prevent scope-b's refresh in the same sweep.
- [ ] **Step 4: Implement the sweep.** `refreshSuggestions` (scheduler.go:40) becomes:

```go
func refreshSuggestions(ctx context.Context, service *Service, scopes ScopeLister, logger *slog.Logger) {
	scopeIDs, err := scopes.ListScopes(ctx)
	if err != nil {
		logger.WarnContext(ctx, "todo suggestion refresh: list scopes failed", "error", err)
		return
	}
	for _, scopeID := range scopeIDs {
		created, err := service.RefreshSuggestions(ctx, scopeID)
		if err != nil {
			logger.WarnContext(ctx, "todo suggestion refresh failed", "scope_id", scopeID, "error", err)
			continue
		}
		if created > 0 {
			logger.InfoContext(ctx, "todo suggestions refreshed", "scope_id", scopeID, "created", created)
		}
	}
}
```

Loop/ticker/stop structure unchanged. Wiring passes `noteDirectory` as the lister.
- [ ] **Step 5: Green + commit** — `go test ./internal/todoapp/... ./internal/platform/postgres -count=1` (DSN exported); `refactor(todoapp): suggestion refresh sweeps all scopes round-robin` + trailer.

---

### Task 4: pagewiki RepositoryManager (lazy per-scope hydration)

**Files:**
- Create: `internal/pagewiki/postgres/manager.go`
- Test: `internal/pagewiki/postgres/manager_test.go`
- Modify: `internal/app/wiring.go:51-53` (construct manager; eager `ForScope(ctx, onprem.LocalScopeID)` at boot)

**Interfaces:**
- Consumes: existing `NewRepository(ctx, pool, scopeID, options...)` (unchanged).
- Produces (Tasks 5-7 depend on):

```go
func NewRepositoryManager(pool *pgxpool.Pool, options ...memory.Option) (*RepositoryManager, error)
func (m *RepositoryManager) ForScope(ctx context.Context, scopeID string) (*Repository, error)
```

- [ ] **Step 1: Failing manager test** (postgres suite pattern, real DB): `ForScope(ctx, "local-team")` twice returns the SAME `*Repository` pointer (identity check `s.Same(a, b)`); `ForScope(ctx, "other-scope")` returns a different instance; publish a page via scope A's repository, assert scope B's `PageCatalog(ctx)` does not contain it (mirror isolation); `ForScope(ctx, "")` errors.
- [ ] **Step 2: Implement** `manager.go`:

```go
// RepositoryManager hands out one hydrated Repository per scope. Each
// Repository carries a per-scope in-memory mirror hydrated at first use, so
// instances are cached for the process lifetime; eviction of idle scopes is
// deliberately deferred until the SaaS control plane exists.
type RepositoryManager struct {
	pool    *pgxpool.Pool
	options []memory.Option

	mu           sync.Mutex
	repositories map[string]*Repository
}

func NewRepositoryManager(pool *pgxpool.Pool, options ...memory.Option) (*RepositoryManager, error) {
	if pool == nil {
		return nil, fmt.Errorf("create pagewiki repository manager: pool is required")
	}
	return &RepositoryManager{pool: pool, options: options, repositories: make(map[string]*Repository)}, nil
}

func (m *RepositoryManager) ForScope(ctx context.Context, scopeID string) (*Repository, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("resolve pagewiki repository: scope ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if repository, ok := m.repositories[scopeID]; ok {
		return repository, nil
	}
	repository, err := NewRepository(ctx, m.pool, scopeID, m.options...)
	if err != nil {
		return nil, fmt.Errorf("hydrate pagewiki repository for scope %s: %w", scopeID, err)
	}
	m.repositories[scopeID] = repository
	return repository, nil
}
```

(Holding `mu` across hydration is intentional: it gives single-flight hydration per process; hydration is a startup-class cost and concurrent first-touch of two different scopes is rare until Phase 3 — note this in the doc comment.)
- [ ] **Step 3: Wire it.** `buildPageWikiHTTPHandler` constructs the manager, then eagerly `repository, err := manager.ForScope(ctx, onprem.LocalScopeID)` — preserving today's boot-time hydration and fail-fast. Downstream wiring keeps using `repository` for now (Tasks 5-7 move consumers onto the manager).
- [ ] **Step 4: Green + commit** — run the new suite + `go build ./...`; `feat(pagewiki): per-scope repository manager with lazy hydration` + trailer.

---

### Task 5: pagewiki ServiceManager + scope-aware WikiSettings

**Files:**
- Create: `internal/pagewiki/service_manager.go`
- Test: `internal/pagewiki/service_manager_test.go`
- Modify: `internal/teamnote/transport/httpapi/handler/dependencies.go:141-144` (WikiSettings interface), `internal/teamnote/transport/httpapi/handler/wiki_settings_endpoints.go:18,:38`, its test file
- Modify: `internal/app/wiring.go` (`buildPageWikiHTTPHandler`; the `wikiSettings` plumbing through `buildApplicationHTTPHandlers`/`buildHTTPHandler`/`onPremHandlerOptions`)

**Interfaces:**
- Consumes: Task 4's `RepositoryManager.ForScope`.
- Produces:

```go
// internal/pagewiki/service_manager.go
type ServiceManagerConfig struct { // the global (scope-independent) collaborators
	Planner Planner; Editor Editor
	Options []ServiceOption // same options wiring builds today (navigator, curator, ...)
}
func NewServiceManager(repositories RepositoryResolver, config ServiceManagerConfig) (*ServiceManager, error)
type RepositoryResolver func(ctx context.Context, scopeID string) (Repository, error)
func (m *ServiceManager) ForScope(ctx context.Context, scopeID string) (*Service, error)
func (m *ServiceManager) Start(ctx context.Context) // records the maintenance root ctx for per-scope loop startup
```

- `handler.WikiSettings` becomes `GenerationSettings(context.Context, string) (pagewiki.GenerationDirectives, error)` / `SetGenerationSettings(context.Context, string, pagewiki.GenerationDirectives) (pagewiki.GenerationDirectives, error)`.

- [ ] **Step 1: Failing manager test** (unit, fake repository resolver): same-pointer identity per scope; different scopes → different `*Service`; `ForScope` after `Start(ctx)` starts tree/curation maintenance exactly once per scope (assert via a fake repository whose method calls signal — reuse whatever fake `internal/pagewiki`'s existing service tests use); `ForScope` with blank scope errors.
- [ ] **Step 2: Implement** — mirror the RepositoryManager shape (mutex + `map[string]*Service`). On first creation of a scope's service: `service := NewService(repository, m.config.Planner, m.config.Editor, m.config.Options...)`, then if `Start` has been called, `service.StartTreeMaintenance(m.maintenanceCtx)` and `service.StartCurationMaintenance(m.maintenanceCtx)`. `Start(ctx)` stores `m.maintenanceCtx = ctx` and starts maintenance for any already-created services (on-prem boot order: manager created → wiring resolves LocalScopeID service → `Start(ctx)` at the point wiring today calls `service.StartTreeMaintenance` (`wiring.go:87-89`)).
- [ ] **Step 3: WikiSettings gains the scope param.** Update the interface (dependencies.go:141-144); endpoints pass `principal.ScopeID` (wiki_settings_endpoints.go:18,:38 — capture the principal exactly as wiki_ingestion_endpoints does since Phase 1); the concrete implementer wired in `internal/app` becomes a tiny adapter over the ServiceManager:

```go
// wikiSettingsAdapter satisfies handler.WikiSettings by resolving the
// per-scope pagewiki service on every call.
type wikiSettingsAdapter struct{ services *pagewiki.ServiceManager }

func (a wikiSettingsAdapter) GenerationSettings(ctx context.Context, scopeID string) (pagewiki.GenerationDirectives, error) {
	service, err := a.services.ForScope(ctx, scopeID)
	if err != nil {
		return pagewiki.GenerationDirectives{}, err
	}
	return service.GenerationSettings(ctx)
}
// SetGenerationSettings: same shape.
```

Handler tests: fakes gain the param; add the assertion that `principal.ScopeID` set to `"other-scope"` reaches the fake (same pattern as Phase 1's wiki-control tests).
- [ ] **Step 4: Green + commit** — `go test ./internal/pagewiki/... ./internal/teamnote/transport/httpapi/handler -count=1` (DSN for handler suite); `feat(pagewiki): per-scope service manager; wiki settings resolve scope per request` + trailer.

---

### Task 6: sessionconsumer goes multi-scope

**Files:**
- Modify: `internal/platform/postgres/pagewiki_consumer.go` (ctor :20, `PendingStreams` :51-73)
- Modify: `internal/pagewiki/sessionconsumer/consumer.go` (struct :97-113, `New` :115, `Rebuild` :187, `maybeRebuild` :208-248, `scan` :276)
- Test: `internal/pagewiki/sessionconsumer/consumer_test.go`, `internal/platform/postgres/pagewiki_consumer_test.go` (find actual name via `grep -rln "PageWikiConsumerStore" internal/platform/postgres/*_test.go`), `internal/pagewiki/sessionconsumer/backoff_test.go`
- Modify: `internal/app/wiring.go:75-79`

**Interfaces:**
- Consumes: Tasks 4-5 managers (wiring builds the resolvers from them).
- Produces:

```go
func NewPageWikiConsumerStore(pool *pgxpool.Pool) (*PageWikiConsumerStore, error) // scope param dropped
// PendingStreams now returns every scope's pending streams (rows carry ScopeID).
type InjectorFor func(ctx context.Context, scopeID string) (Injector, error)
type RebuilderFor func(ctx context.Context, scopeID string) (Rebuilder, error)
func New(store Store, injectorFor InjectorFor, rebuilderFor RebuilderFor, logger *slog.Logger, interval time.Duration) (*Controller, error)
```

Public `Controller` methods keep their Phase-1 signatures (`Status/SetAutoInject/InjectSession/Rebuild(ctx, scopeID, ...)`) — the transport contract is untouched.

- [ ] **Step 1: Store first (failing test).** Consumer-store suite: seed pending sessions under `"local-team"` AND `"other-scope"` (reuse the suite's fixture helpers), assert `PendingStreams(ctx)` returns both scopes' streams with correct `ScopeID` on each, and that a scope with auto-inject disabled is excluded while the other still appears (per-scope settings honored). Implement: drop the `scopeID` field/ctor param; remove the `s.scopeID` predicate from the `PendingStreams` query (:68) — the per-stream `scope_id` join against `pagewiki_ingestion_settings` already carries per-scope auto-inject; verify the SQL's remaining bind args shift correctly.
- [ ] **Step 2: Controller state (failing tests first, in consumer_test.go using its existing fake store/injector pattern):**
  - Two scopes with pending streams both get consumed in one scan (fake injector records scope-tagged injections).
  - `Rebuild(ctx, "scope-a", since)` then `Rebuild(ctx, "scope-b", since)` before the first executes: BOTH scopes report `RebuildQueued`/`RebuildRunning`→ terminal states independently; scope-b is not swallowed (this is the single-slot bug — the test must fail against current code).
  - After scope-a's rebuild succeeds, a pre-seeded failure/backoff entry for scope-b (`"scope-b/agent/session"` key) SURVIVES, while scope-a's entries are cleared.
- [ ] **Step 3: Implement.**
  - Struct: `rebuilds map[string]*scopeRebuild` (fields: `status RebuildStatus`, `since time.Time`) guarded by the existing `stateMu`; drop `rebuild`, `rebuildScope`, `rebuildSince`.
  - `Rebuild(_, scopeID, since)`: create-or-update that scope's entry exactly as the single slot did (preserve the "already queued/running → return current status, don't re-arm" semantics PER SCOPE, and the 202-contract statuses from PR #66); ping `c.trigger`.
  - `maybeRebuild(ctx)`: under `stateMu`, collect scopes whose state is Queued; for each (deterministic order — sort scope IDs), resolve `rebuilder, err := c.rebuilderFor(ctx, scopeID)` and run the rebuild exactly as today (same `c.mu` hold, same status transitions, same log fields + `scope_id`); on success clear only that scope's failures: `for key := range c.failures { if strings.HasPrefix(key, scopeID+"/") { delete(c.failures, key) } }`.
  - `consume`/`scan`: resolve the injector per stream — `injector, err := c.injectorFor(ctx, stream.ScopeID)`; resolution error = per-stream failure (existing backoff path), not fatal to the scan.
  - `Status/SetAutoInject/InjectSession`: for `InjectSession`, resolve the injector for the given scope the same way. `Status` reads that scope's rebuild entry (absent → zero-state, same as today's idle status).
- [ ] **Step 4: Wiring.** `buildPageWikiHTTPHandler`: `consumerStore, err := postgres.NewPageWikiConsumerStore(store.Pool())`; controller gets closures over the managers:

```go
controller, err := sessionconsumer.New(consumerStore,
	func(ctx context.Context, scopeID string) (sessionconsumer.Injector, error) { return services.ForScope(ctx, scopeID) },
	func(ctx context.Context, scopeID string) (sessionconsumer.Rebuilder, error) { return repositories.ForScope(ctx, scopeID) },
	logger, 2*time.Second)
```

- [ ] **Step 5: Green + commit.** `go test ./internal/pagewiki/... ./internal/platform/postgres -count=1` (DSN) — pay attention to the postgres-side async-rebuild integration test adapted in PR #66; it must still pass unmodified semantics. Commit `refactor(pagewiki): session consumer sweeps and rebuilds per scope` + trailer.

---

### Task 7: pagewikihttp resolves its dependencies per request

**Files:**
- Modify: `internal/pagewiki/transport/httpapi/dependencies.go` (:39 `New`, handler struct), `internal/pagewiki/transport/httpapi/endpoints.go` (each `h.reader.X`/`h.injector.X` call site)
- Test: the package's existing handler tests (`grep -rln "pagewikihttp.New\|httpapi.New" internal/pagewiki/transport/httpapi/*_test.go`)
- Modify: `internal/app/wiring.go:83`

**Interfaces:**
- Consumes: Tasks 4-5 managers.
- Produces: `type Dependencies func(ctx context.Context) (Injector, Reader, error)`; `func New(dependencies Dependencies) (*Handler, error)`. The `Injector`/`Reader` interfaces themselves are unchanged.

- [ ] **Step 1: Failing test:** construct the handler with a `Dependencies` closure that counts invocations and returns fakes; two requests → resolver called twice (per-request resolution, no caching in the handler); resolver error → the endpoint answers 500-equivalent per the package's existing error convention (read how endpoints report reader errors today and assert that same convention).
- [ ] **Step 2: Implement.** Handler stores the closure; every endpoint begins `injector, reader, err := h.dependencies(ctx)` (or just the one it needs — match per endpoint) and proceeds as before. On-prem wiring:

```go
configured, err := pagewikihttp.New(func(ctx context.Context) (pagewikihttp.Injector, pagewikihttp.Reader, error) {
	service, err := services.ForScope(ctx, onprem.LocalScopeID)
	if err != nil {
		return nil, nil, err
	}
	repository, err := repositories.ForScope(ctx, onprem.LocalScopeID)
	if err != nil {
		return nil, nil, err
	}
	return service, repository, nil
})
```

(The fixed `LocalScopeID` here is the deliberate on-prem profile pin — per-request tenant resolution on this transport arrives with Phase 3 auth; the seam is what Phase 2 owes.)
- [ ] **Step 3: Green + commit** — package tests + `go build ./...`; `refactor(pagewiki): wiki transport resolves per-scope dependencies per request` + trailer.

---

### Task 8: two-scope verification, gates, PR

**Files:**
- Test: extend the handler integration suite (`internal/teamnote/transport/httpapi/handler/`) or `tests/onprem-e2e` — wherever `TEAM_MEMORY_API_KEYS`/`StaticAPIKeys` mode is already exercised (find: `grep -rln "StaticAPIKeys" --include='*_test.go' internal tests`); add the two-scope flow there.
- No production files except what review fallout requires.

**Interfaces:** consumes everything above.

- [ ] **Step 1: The M2 acceptance test.** In API-key mode with two keys → two scopes (`{"key-a":"local-team","key-b":"other-scope"}`): observe a session under each key; assert recall/read APIs under key-a never surface key-b's data and vice versa; assert todo suggestions and (where the harness reaches it) wiki ingestion status stay scope-separated. Follow the existing API-key-mode test harness idioms exactly — this is an extension of an existing suite, not a new framework.
- [ ] **Step 2: Full gates.** `make lint && make coverage && make integration-test` (port 55499 + DSN). Also `grep -rn "onprem.LocalScopeID" internal/app/wiring.go` — every remaining hit must be one of the deliberate on-prem profile pins (todoapp transport does NOT appear; pagewiki transport closure and eager boot hydration DO).
- [ ] **Step 3: Manual compose check (document output in the PR):** `docker compose up` locally, confirm boot logs show pagewiki hydration for local-team only, UI works as before.
- [ ] **Step 4: Push + single PR** titled `refactor: SaaS phase 2 — satellites go scope-per-call, loops sweep all scopes`, body summarizing the two target shapes (parameter style vs manager style), the Controller multi-scope fixes (per-scope rebuild slots, scoped backoff reset, all-scope scan), the M2 two-scope test, and the deliberate on-prem pins. End with the 🤖 footer. Do not merge — the user merges.

---

## Self-Review Notes

- ADR Phase-2 coverage: five constructors de-scoped (todo repo T1, note directory T1, lake reporter T1/T2, pagewiki repo T4, consumer store T6) ✅; two named loops scope-sweeping (todo refresh T3, consumer scan T6) ✅; round-robin fairness = sorted/sequential iteration per sweep with per-scope error isolation ✅; per-stream backoff preserved (Controller keeps `failures`, now scope-reset-safe) ✅; M2 verification via `TEAM_MEMORY_API_KEYS` second scope ✅ (T8).
- Beyond the ADR letter but required for correctness (discovered in survey, in scope): per-scope rebuild slots + scoped failure reset in Controller; tree/curation maintenance made per-scope via ServiceManager; WikiSettings + pagewikihttp scope seams (without them a second scope corrupts the first's settings/content).
- Deliberately deferred: idle-scope eviction in the managers; per-request tenant auth on pagewikihttp; scope registry abstraction (Phase 3); depguard coverage for internal/llmwiki + internal/session.
- Known risk: Task 6 touches the PR #66 async-rebuild semantics — its postgres integration test is the guard; any change to the 202/status contract is a defect, not an adaptation.
