# SaaS Plan: GCP + WorkOS + Cloudflare

Date: 2026-08-01
Status: Accepted direction; milestones land incrementally

This is the end-to-end plan for operating the multi-team SaaS distribution.
It composes three accepted ADRs and adds the edge layer, environment
model, security posture, operations, and a milestone rollout:

- [On-Prem Core with Multi-Team SaaS](./decisions/2026-07-31-onprem-saas-split.md) — core architecture and phases
- [SaaS Identity via WorkOS](./decisions/2026-08-01-saas-identity-workos.md) — human sign-in
- [GCP Hosting Topology](./decisions/2026-08-01-gcp-hosting-topology.md) — compute/data topology
- [LLM Token Metering](./decisions/2026-07-31-llm-token-metering.md) — spend observability and quota point

`<domain>` below stands for the production domain registered on
Cloudflare.

## 1. Topology at a glance

```
Browser
  │  https://app.<domain>          (prod; staging: app.stg.<domain>)
  ▼
Cloudflare (DNS + proxy: TLS, WAF, DDoS, edge cache for static assets)
  │  Full (strict) TLS to origin
  ▼
GCP Global External ALB ── /v1/*  ──► Cloud Run `api`  (autoscale)
  │                                        │
  │  everything else                       │ private IP
  ▼                                        ▼
GCS bucket (web/dist)                Cloud SQL Postgres (pgvector)
                                           ▲
                     Cloud Run `worker` ───┘  (min=max=1, always-on:
                     session-consumer scan, tree maintenance,
                     todo refresh, river extraction queue)

WorkOS AuthKit  ◄─ OIDC redirect flow ─►  Cloud Run `api`
LLM provider (DeepSeek et al.)  ◄─ outbound ─  api + worker
```

**One hostname per environment.** The SPA calls `/v1/*` with relative
paths and the session/CSRF cookies are `SameSite=Lax` on the same origin —
a single host per environment keeps CORS nonexistent and the cookie model
untouched. No `api.` subdomain.

## 2. Edge: Cloudflare

- **DNS**: zone for `<domain>` on Cloudflare. Records:
  - `app.<domain>` → ALB anycast IP (prod project), **proxied** (orange
    cloud).
  - `app.stg.<domain>` → staging ALB IP, proxied.
  - WorkOS domain-verification and email records as WorkOS onboarding
    requires (DNS-only entries).
- **TLS**: Cloudflare Full (strict). Origin cert on the ALB via GCP
  Certificate Manager with **DNS authorization** (a one-time CNAME in
  Cloudflare; works while proxied). Cloudflare min TLS 1.2, HSTS on once
  stable.
- **WAF / DDoS**: Cloudflare managed ruleset on; rate-limiting rules on
  `/v1/auth/*` (login/callback) and `POST /v1/wiki/rebuild` as a coarse
  outer guard — real quotas are enforced in-app (metering ADR). DDoS
  protection is the main reason the proxy is on from day one.
- **Cache**: cache-everything for `/assets/*` (hashed filenames from the
  Vite build → immutable, long TTL). HTML (`/`, SPA entry) bypasses cache
  so deploys are instant; `/v1/*` bypasses cache entirely.
- **Not used (yet)**: Cloudflare Pages/Workers. Static hosting stays on
  GCS behind the ALB so the whole deploy surface lives in one cloud; the
  edge only proxies and caches. Revisit only if ALB cost or latency ever
  matters.
- **Origin lockdown**: ALB accepts traffic from Cloudflare IP ranges only
  (Cloud Armor policy with Cloudflare's published ranges), so nobody
  bypasses the WAF by hitting the ALB IP directly.

## 3. Identity: WorkOS

- AuthKit is the **sole OIDC issuer** for the SaaS profile; the existing
  go-oidc RP code is pointed at it. Separate WorkOS environments for
  staging and prod, each with its own client id/secret (Secret Manager)
  and redirect URI (`https://app.<domain>/v1/auth/callback` — match the
  existing route naming when wiring).
- Social connections enabled at launch: Google, GitHub. Email/password
  and MFA policy live in WorkOS, not in our code.
- **What stays ours**: session cookies, CSRF, membership roles
  (owner/admin/member per team), invitations, agent credentials. WorkOS
  answers "who is this human"; the control plane answers "which teams,
  which role".
- Sign-up flow (control-plane milestone): login via WorkOS → if the user
  has no team: create one (becomes owner) or accept a pending invitation
  by email match. Domain-capture auto-join is deferred.
- Enterprise SSO (customer's Okta/Azure AD, SAML) and SCIM: per-customer
  WorkOS Organization mapped to a team, switched on when the first
  enterprise customer needs it. Connection fees priced into an
  enterprise tier.

## 4. GCP: projects, services, data

Per the hosting ADR — region `asia-northeast1`, projects
`<product>-stg` and `<product>-prod`, identical shape:

| Piece | Choice | Notes |
|---|---|---|
| API | Cloud Run `api` | autoscaling, stateless; serves `/v1/*` |
| Workers | Cloud Run `worker` | `min=max=1`, CPU always allocated; scan loops are in-process state |
| DB | Cloud SQL Postgres + pgvector | private IP, automated backups, PITR |
| Migrations | Cloud Run Job in deploy pipeline | add advisory lock to `store.Migrate`; on-prem keeps migrate-on-boot |
| Static | GCS bucket, synced by CI | ALB default backend |
| Secrets | Secret Manager | LLM keys, pepper, WorkOS client secret, bootstrap secret |
| Images | Artifact Registry | one image, two services + job |
| Egress | direct (public LLM APIs) | no NAT needed unless a provider requires IP allow-listing |

Both Cloud Run services set ingress "internal and Cloud Load Balancing"
so the ALB (and thus Cloudflare) is the only path in.

## 5. Application profile: what the SaaS binary needs

Mapped to the split ADR's phases; this is the order of code work.

1. **Phase 1 — scope hygiene** (prereq, small): kill stray `"local-team"`
   literals; handlers resolve scope from principal/context; depguard rule
   for the core→deployment dependency direction; extract `internal/app`
   assembly from `main.go`; `cmd/team-memory-onprem` keeps today's
   behavior byte-for-byte.
2. **Phase 2 — satellite de-scoping**: the six per-scope constructors go
   scope-per-call; session-consumer scan and todo refresh become
   scope-sweeping single loops (round-robin across scopes, keep
   per-stream backoff). Until this lands, the SaaS alpha simply runs with
   one scope (see M1 below) — phase 2 is required for *multi*-team, not
   for launch-on-GCP.
3. **Phase 3 — control plane** (`cmd/team-memory-saas`): teams table,
   memberships with `scope_id`, invitations, "current team" resolution
   per request (session → team → scope into context), team switcher in
   the portal shell, agent credentials issued per team (`Principal.ScopeID`
   checks flip from "== LocalScopeID" to "== requested scope").
4. **Phase 4 — quotas and billing readiness**: per-team LLM budgets
   enforced in `MeteredChatClient` (check before call, record after);
   budget alerts from `llm_usage_events`; per-team rate limits at the
   API layer. Payment integration (Stripe) is explicitly out of scope
   until there is a paying customer shape; the quota plumbing is the
   prerequisite either way.

Runtime config additions for the SaaS profile: JSON slog handler,
`cookieSecure=true`, WorkOS issuer settings, and (phase 4) default team
budgets.

## 6. Environments, CI/CD, promotion

- **staging** (`app.stg.<domain>`): auto-deploys on merge to main —
  migration job → `api`/`worker` rollout → `web/dist` sync → smoke test
  (login redirect reachable, `/healthz`, one authenticated read).
  Staging is also where reset-rebuild replays against imported corpora
  (e.g. pi-mono) run, so LLM spend experiments never touch prod quotas.
- **prod** (`app.<domain>`): manual promotion of the staging-verified
  image digest (no rebuild between environments).
- GitHub Actions with Workload Identity Federation; no service-account
  keys. Existing CI gates (lint, coverage ≥ 80%, integration, web tests)
  stay the merge gate; deploy jobs are additive.
- Rollback = redeploy previous image digest + (if schema moved) PITR
  decision. Migrations stay backward-compatible for one release
  (expand/contract) so image rollback alone is normally enough.

## 7. Security posture

- **Transport**: Cloudflare strict TLS end-to-end; HSTS; origin locked to
  Cloudflare ranges via Cloud Armor.
- **Secrets**: Secret Manager only; per-project isolation; pepper and
  WorkOS secrets never shared between staging and prod.
- **Data**: Cloud SQL private IP; team isolation by `scope_id` row
  scoping (phases 1-3), Postgres RLS as later defense-in-depth per the
  split ADR; automated backups + PITR; a periodic cross-region backup
  export once there is real customer data.
- **AuthN/AuthZ**: WorkOS for humans; existing digest-based agent
  credentials for agents; CSRF and SameSite=Lax cookies unchanged;
  `onprem_audit_events` gains `scope_id` with the control plane.
- **IAM**: deploy SA can deploy and nothing else; runtime SAs get
  Cloud SQL client + Secret Manager accessor on their own secrets only.
- **LLM egress**: provider keys are per-environment; per-team keys are a
  phase-4 option the settings-table pattern already supports.

## 8. Operations

- **Alerts** (Cloud Monitoring):
  - `api` 5xx rate and p95 latency;
  - **`worker` liveness** — a single-instance service whose death stalls
    all ingestion: alert on instance count == 0 and on
    "Page Wiki session injected" log silence beyond an hour of nonzero
    backlog (`pending_sessions > 0`);
  - Cloud SQL: connections, storage, replication of backups;
  - **LLM budget**: scheduled query over `llm_usage_events` per scope per
    day; alert at soft thresholds long before phase-4 hard quotas exist;
  - uptime checks on `https://app.<domain>/healthz` from outside
    Cloudflare.
- **Logs**: JSON slog → Cloud Logging; the existing structured fields
  (scope_id, component, tokens) make per-team debugging queryable.
- **Dashboards**: reuse the in-app wiki status + LLM usage cards for
  product-level visibility; Cloud Monitoring for infra.
- **Runbooks to write with M1**: deploy/rollback, worker restart,
  Cloud SQL failover drill, WorkOS outage posture (existing sessions
  keep working — only new logins fail — because sessions are ours).

## 9. Milestones

| Milestone | Deliverable | Depends on |
|---|---|---|
| **M0 — infra bootstrap** | Both projects, Cloud SQL, Artifact Registry, ALB + GCS + Cloudflare zone wired, WIF deploy pipeline green with a hello image | nothing |
| **M1 — cloud alpha (single team)** | Today's binary (on-prem profile) on Cloud Run with WorkOS as its OIDC issuer, our own team dogfooding at `app.stg.<domain>`; migration job; runbooks | M0; zero core code change — the on-prem profile already accepts any OIDC issuer |
| **M2 — multi-tenant core** | Phase 1 + 2 refactors landed; one process serving N scopes correctly (verified by a second scope in staging via `TEAM_MEMORY_API_KEYS` before the control plane exists) | M1 |
| **M3 — teams GA shape** | Control plane: sign-up, teams, invitations, team switcher, per-team agent credentials; `cmd/team-memory-saas` is the deployed image | M2 |
| **M4 — quotas & readiness** | Per-team LLM budgets enforced at the metering decorator; budget alerts; enterprise SSO onboarding path documented | M3 |

M1 is deliberately a near-zero-code milestone: it proves the entire
GCP + Cloudflare + WorkOS spine with the product as it exists today,
before any multi-tenancy refactor risk is taken.

### M3 status (2026-08-04)

**Deployed to staging**: `nexus-stg.paxtech.net` runs the `saas` profile
(image carries both binaries, terraform `profile` variable swaps the Cloud
Run command and drops the bootstrap secret; migration 029 applied on
boot). Rollback: `-var="profile=onprem"` plus the previous image.

Landed:

- Control plane storage: `team_*` tables (migration 029), audit rows carry
  `scope_id` (on-prem rows default `local-team`), Postgres adapters under
  `internal/platform/postgres/saas_*.go`.
- Domain services in `internal/deployment/saas` (`ControlPlane`, `Registry`,
  `Credentials`) reusing the on-prem types and handler interfaces; devices
  and channel deliberately return `ErrUnsupportedInSaaS` (501).
- HTTP surface: `POST/GET /v1/teams`, `POST /v1/me/current-team`, `/v1/me`
  carries `teams` + `current_team_id`; session-audit filters use the
  principal's scope.
- Wiring: `app.RunSaaS` + `cmd/team-memory-saas` (`make build-saas`);
  per-request scope resolution for operations/explorer and the Page Wiki
  transport (human session or agent credential); OIDC-only auth mode with
  legacy keys and bootstrap rejected at config load.
- Verification: two-team HTTP isolation e2e
  (`internal/app/saas_isolation_test.go`) — members, agents, invitations,
  notes write/recall, audit events, team switching, 501 surfaces.
- Frontend (single build, profile detected at runtime): `OnboardingPage`
  (create team / join with invitation), `TeamSwitcher` in the portal
  sidebar, `/team` settings page; on-prem `EntryPage`/bootstrap unchanged.
  Static mockups kept under `design/m3-teams/`.

Deferred / known gaps:

- Operations *recorder*, extraction observer, and operations maintenance
  still attribute to `local-team` in the SaaS profile (read models are
  per-request scoped); LLM-usage attribution for request-driven wiki
  maintenance follows context scope or the default.
- Team rename/delete endpoints, SaaS device enrollment, `CreateTeam`
  server-side current-team switch (the portal switches explicitly).
- Postgres RLS, scope-sweep backed by the `teams` registry, M4 quotas,
  domain-capture auto-join.

## 10. Cost (steady-state, early stage, per environment)

| Item | Monthly (USD, approx) |
|---|---|
| Cloud SQL (small, non-HA staging / HA prod) | 30 / 90 |
| Cloud Run `worker` (1 vCPU always-on) | 15-30 |
| Cloud Run `api` (low traffic) | < 10 |
| ALB + Certificate Manager | ~ 20 |
| GCS + egress | < 5 |
| Cloudflare (Free or Pro) | 0 / 20 |
| WorkOS (AuthKit, pre-enterprise) | 0 |
| **Infra total** | **≈ $70-175** |

LLM spend dominates and is tracked per team in `llm_usage_events`;
staging replay experiments are the main variable.

## 11. Risks and open items

- **Squash-merge + stacked PRs** already bit once (#47/#48): CI should
  gain a guard that fails a PR whose base branch is already merged.
- **Advisory lock in `store.Migrate`** must land before the migration
  job runs concurrently with anything (M1 item).
- **Worker singleton** is a deliberate availability trade-off: a crash
  loses up to a scan interval of progress (cursors make catch-up
  automatic) but a stuck instance stalls ingestion — hence the liveness
  alert. Moving scan loops onto river removes the constraint when scale
  demands it.
- **Region bet** (Tokyo) is cheap to revisit before data gravity builds.
- **DeepSeek reachability from GCP** should be latency-tested in M0;
  provider choice is config, not architecture.
- Actual `<domain>` purchase/transfer into Cloudflare, WorkOS account
  and environment setup, and GCP org/billing accounts are M0 human
  prerequisites.
