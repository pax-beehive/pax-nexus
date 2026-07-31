# On-Prem Core with Multi-Team SaaS

Status: Accepted (direction); phases land incrementally

Date: 2026-07-31

Related:

- [Single-Team On-Prem Deployment](./2026-07-21-single-team-on-prem-deployment.md)
- [On-Prem Identity and Agent Registry](./2026-07-21-on-prem-identity-and-agent-registry.md)
- [LLM Token Metering](./2026-07-31-llm-token-metering.md)

## Context

We want two distributions of the same product: the existing single-team
on-prem deployment, and a multi-team SaaS where each team is a tenant and
teams are isolated from each other.

A coupling survey (2026-07-31) found three distinct layers:

- **Core domain (notes / recall / extraction) is already multi-tenant
  shaped**: `NoteStore` takes `scopeID` per call, scope travels in
  `context.Context` (`teamnote.WithScope` / `ScopeFromContext`), the
  extraction queue (river) carries `ScopeID` per job, and a
  `ScopeResolver` seam (`StaticAPIKeys`, `TEAM_MEMORY_API_KEYS`) already
  resolves arbitrary scopes per request — mutually exclusive with the
  on-prem identity mode by config validation.
- **Satellite subsystems bind scope at construction**: six constructors
  store a `scopeID` field (pagewiki repository, PageWiki consumer store,
  todo repository, todo note directory, lake reporter, and an
  `ExplorerStore` hardcoded to `"local-team"` in
  `platform/postgres/store.go`). Two background loops (session-consumer
  scan, todo suggestion refresh) are one-goroutine-per-scope under this
  wiring; the embedding backfill and extraction queue already sweep all
  scopes.
- **Identity (`internal/deployment/onprem`) is single-install by
  design**: 11 unscoped tables, a database-enforced
  `singleton_id = 1` row, 7 domain-level `!= LocalScopeID → forbidden`
  assertions, per-install cookies, one LLM key and one secret pepper for
  the whole process. Its package doc says "single-Team deployment" — this
  is its design point, not debt.

Semantically, `scope_id` **is** the team id (`LocalScopeID =
"local-team"`), so "multi-team SaaS" means: one process serving N scopes,
isolation = the row-level scoping the business tables already have.

## Decision

Keep one repository and one core; make deployment identity a swappable
adapter; build the SaaS control plane as new code rather than retrofitting
the on-prem identity package.

1. **Monorepo with two assembly entrypoints.** Extract the ~800-line
   `main.go` wiring into `internal/app` building blocks; add
   `cmd/team-memory-onprem` (current behavior) and `cmd/team-memory-saas`
   (control plane + `ScopeResolver` wiring). One core version must pass
   both profiles' tests in one CI run. Splitting repositories is deferred
   until core open-sourcing, a separate owning team, or divergent release
   cadence forces it.
2. **Dependency direction is law**: core packages never import
   `internal/deployment/*`; only `cmd/*` and `internal/app` wire. Enforced
   with a depguard rule in `.golangci.yml`.
3. **Phase 1 — centralize scope resolution** (small, zero behavior
   change): kill the stray `"local-team"` literals
   (`paxmprovider/provider.go`, `platform/postgres/store.go`, wiki
   handler pass-throughs); handlers resolve scope from the principal /
   context; `LocalScopeID` survives only inside the on-prem adapter.
4. **Phase 2 — de-scope the satellites** (mechanical): the six per-scope
   constructors move to scope-per-call like `NoteStore`; the two
   per-scope background loops become scope-sweeping (single goroutine
   serving all tenants, round-robin across scopes for fairness, keeping
   the existing per-stream failure backoff).
5. **Phase 3 — SaaS control plane** (new code): team directory,
   team-scoped memberships (global users via the existing
   `(issuer, subject)` identity, membership rows gain the team key),
   team-aware sessions ("current team" resolved per request), agent
   credentials issued per team — the existing `Principal.ScopeID` checks
   flip from "must equal LocalScopeID" to "must equal the requested
   scope", turning the single-tenant lock into the tenant-isolation
   check. The on-prem identity package stays as-is for the on-prem
   distribution (N=1 special case).
6. **Phase 4 — per-team runtime config**: LLM keys/models, quotas, rate
   limits per team (`pagewiki_generation_settings`'s scope-PK pattern is
   the template); LLM spend enforcement lands on the metering decorator
   (companion ADR). Audit events gain `scope_id`.
7. **Isolation hardening as needed**: Postgres row-level security as
   defense in depth on the shared-schema model; schema-per-tenant or
   database-per-tenant reserved for future large-tenant migration, not
   the starting point.

## Consequences

- On-prem distribution is untouched through phases 1-2 (pure refactor
  underneath it); its compose bundle and identity model remain the
  supported N=1 product.
- Phases 1-2 are moderate mechanical work that also pays existing debt
  (the `store.go` hardcode bypasses configuration entirely today).
- The real investment is the phase-3 control plane; it is new product
  surface, deliberately not entangled with the 11 singleton identity
  tables.
- Until phase 4, a single team's reset-rebuild can consume the whole
  process's LLM budget — acceptable on-prem, a noisy-neighbor incident in
  SaaS; quotas are therefore a launch prerequisite for SaaS, not polish.
- Web frontend stays single-build; profile differences (team switcher)
  ride runtime config.
