# Multi-Tenant Hardening: PageWiki Session Consumer — Design

Date: 2026-08-02
Status: approved (design review with user)
Scope: the three pre-multi-tenant gate items from PR #68's checklist. The
gate: the moment any multi-scope data appears (a multi-key
`TEAM_MEMORY_API_KEYS` deployment reaches it immediately), these must be
fixed. This work lands them before that gate opens.

## Problems

All three live in the pagewiki session-consumer path
(`internal/pagewiki/sessionconsumer`, `internal/pagewiki`,
`internal/pagewiki/postgres`, `internal/platform/postgres`):

1. **Cross-tenant starvation in `PendingStreams`**
   (`internal/platform/postgres/pagewiki_consumer.go`): the query orders by
   `stream.updated_at` globally with `LIMIT 100`. A scope holding ≥100
   permanently failing streams (whose `updated_at` never advances, so they
   stay oldest) occupies every slot each scan, permanently starving all
   other tenants.
2. **Cold-scope hydration blocks everyone**:
   `postgres.RepositoryManager.ForScope` and `pagewiki.ServiceManager.ForScope`
   hold their manager-wide mutex across full wiki-mirror hydration.
   Additionally, the consumer's `consume` resolves the injector *inside*
   `Controller.mu`, so a cold-scope first touch stalls the whole consumer and
   every scope's pagewiki HTTP that contends on those locks — the #66 504
   shape revived on the manager/controller locks.
3. **Single `Controller.mu` serializes tenants**: one process-wide mutex
   covers the entire scan pass (up to 100 LLM injections, minutes each),
   manual `InjectSession`, and rebuilds. `maybeRebuild` also drains *all*
   queued scope rebuilds before the scan resumes. One tenant's long
   operation blocks every other tenant's operations.

## Design

### 1. `PendingStreams` fairness: per-scope quota in SQL

Rewrite the query with a window function: partition by `scope_id`, order by
`updated_at`, take `ROW_NUMBER()` as `rn`; outer query filters `rn <= 20`
and orders `ORDER BY rn, updated_at LIMIT 100`.

- A single scope takes at most 20 of the 100 slots per scan.
- `ORDER BY rn` interleaves scopes round-robin style: every scope's first
  stream sorts before any scope's second.
- 20 is a constant, not configuration — it is a fairness parameter, not a
  throughput parameter.
- The same-shaped query in `internal/platform/postgres/audit.go` is
  deliberately untouched: the audit consumer is read-only risk
  classification where a delayed round is harmless. Note it in the PR body.

### 2. Manager hydration: per-scope single-flight

`RepositoryManager` (`internal/pagewiki/postgres/manager.go`) and
`ServiceManager` (`internal/pagewiki/service_manager.go`) switch from
"hold the manager mutex across hydration" to a two-tier scheme:

- The manager-wide mutex only looks up / creates the per-scope entry
  (instant).
- Hydration runs under the entry's own lock (or once), outside the global
  lock.
- Concurrent first-touch of the *same* scope stays single-flight (hydrates
  once); different scopes no longer block each other.
- A failed hydration is not cached: the next `ForScope` retries.
- `ServiceManager.Start`'s contract (maintenance loops start exactly once
  per Service, whether created before or after `Start`) is preserved.

### 3. Controller concurrency: per-scope locks + bounded worker pool

`Controller.mu` (process-global) is removed. In its place:

- **Per-scope locks** serialize, per scope: scan-driven injection, manual
  `InjectSession`, and rebuild. Scopes never contend with each other.
  Within a scope injection stays strictly serial — the wiki mirror is
  per-scope in-memory state; concurrent or out-of-order writes would race.
- **Tick dispatches scope jobs**: `PendingStreams` results are grouped by
  scope (preserving order); each group — together with that scope's queued
  rebuild, if any — becomes one job submitted to a worker pool of capacity
  K (default 2). A scope with a queued rebuild but no pending streams still
  gets a job (rebuild-only). A job runs the rebuild first (if queued), then
  injects the scope's streams in order, checking between streams whether a
  *new* rebuild was queued for *this* scope — if so the job stops there
  (yields); the next tick's job for that scope runs the rebuild. Yield is
  per-scope now, not global.
- **In-flight dedup**: an in-flight set guarantees at most one job per
  scope at a time; a tick that fires while a scope's job is still running
  skips that scope instead of queueing behind it.
- **Rebuilds no longer batch-drain**: scope A's rebuild occupies one worker
  slot while scope B's injection proceeds in another. This removes both
  sub-problems of checklist item 3 (cross-tenant serialization, and
  "rebuilds all drain before scan resumes").
- The `failures` (backoff) map gets its own small mutex. `stateMu`, the
  rebuild state machine, the 202 contract, and status endpoint semantics
  are unchanged.
- Injector/rebuilder resolution (which may hydrate) moves *before* taking
  the per-scope lock; combined with §2, hydration never runs under any
  lock.

**Configuration**: `TEAM_MEMORY_PAGEWIKI_INJECT_CONCURRENCY` sets K.
Unset → 2. Explicit values must be ≥ 1 (0 or negative is a startup config
error). Parsed in `internal/app` wiring following the existing
`parseNonNegativeEnvironment` pattern.

**On-prem invariant**: a single-scope deployment only ever produces one
job, so behavior matches today's serial consumer regardless of K.

## Testing

- **Consumer unit tests** (fake store, two scopes): B can inject/rebuild
  while A's injection is blocked; same-scope operations stay serial;
  per-scope rebuild yield semantics; in-flight dedup (no job pile-up).
  Existing async-rebuild tests (202 contract, backoff clearing on rebuild)
  are adapted to the new lock structure with assertions unchanged.
- **Postgres integration test**: scope A with 25+ pending streams plus
  scope B with 1; assert B appears in `PendingStreams` results.
- **Manager unit tests**: with a blocked fake hydrator holding scope A's
  hydration open, `ForScope` for scope B returns immediately; concurrent
  first-touch of one scope hydrates exactly once; failed hydration retries.
- **Gates**: `make lint`, `make coverage` (≥ 80%), `make integration-test`
  as usual.

## Out of scope (Phase 3)

- Manager idle-scope eviction.
- Scope registry abstraction.
- PageWiki transport per-request tenant auth (still pinned to
  `local-team`).
- Audit-consumer query fairness.
