# GCP Hosting Topology for the SaaS Distribution

Status: Accepted

Date: 2026-08-01

Related:

- [On-Prem Core with Multi-Team SaaS](./2026-07-31-onprem-saas-split.md)
- [SaaS Identity via WorkOS](./2026-08-01-saas-identity-workos.md)
- [LLM Token Metering](./2026-07-31-llm-token-metering.md)

## Context

The SaaS profile needs managed hosting. The process today is one Go binary
(HTTP API + several background loops) against one Postgres (pgvector
required: `vector(384)` note embeddings, migration 005), with a static
React bundle served by a reverse proxy. Two background loops are
single-instance by construction (session-consumer scan every 2s and the
todo suggestion refresh hold in-process state; duplicate instances would
double-consume and double LLM spend, guarded only by idempotency keys);
the river extraction queue and embedding backfill are multi-instance safe.

## Decision

Region **asia-northeast1 (Tokyo)**, two GCP projects (**staging**,
**prod**), same topology in each:

- **Compute — Cloud Run, two services from one image**, matching the
  split ADR's `cmd/` layout:
  - `api`: stateless HTTP; autoscales horizontally (sessions live in
    Postgres).
  - `worker`: background loops; `min-instances=1`, `max-instances=1`,
    CPU always allocated. Scaling workers beyond 1 later means moving the
    scan loops onto river jobs — a known path, not a rewrite.
- **Database — Cloud SQL for PostgreSQL** with pgvector, private IP,
  automated backups + PITR. Smallest tier to start; AlloyDB only if
  measured load demands it.
- **Migrations — a Cloud Run Job in the deploy pipeline**, replacing
  migrate-on-boot for SaaS (overlapping instance boots must not race the
  migrator; verify/add an advisory lock in `store.Migrate` as part of
  this move). On-prem keeps migrate-on-boot.
- **Frontend — GCS bucket + Cloud CDN behind a Global External ALB**;
  path routing sends `/v1/*` to Cloud Run `api`, everything else to the
  bucket. CI builds `web/dist` and syncs the bucket, eliminating the
  stale-bundle failure mode seen on the workstation deployment. Managed
  TLS certificates on the ALB.
- **Secrets — Secret Manager** (LLM keys, secret pepper, WorkOS client
  secret, bootstrap secret), mounted into Cloud Run; never baked into
  images or plain env files.
- **CI/CD — GitHub Actions** with Workload Identity Federation (no
  service-account keys), pushing to Artifact Registry, deploying staging
  on merge to main and prod on manual promotion.
- **Observability** — switch slog to the JSON handler under the SaaS
  profile so Cloud Logging ingests structured records; Cloud Monitoring
  alerts on error rate/latency; LLM spend dashboards and alerts query our
  own `llm_usage_events` (metering ADR) rather than an external APM.

## Consequences

- Steady-state infra cost at launch is modest (≈$100-150/month across
  Cloud SQL, ALB, and the always-on worker); LLM spend dominates and is
  now observable per team.
- The `api`/`worker` split hard-codes the single-worker constraint into
  topology instead of hoping replicas behave — correctness by
  configuration until the scan loops move to river.
- Tokyo is a bet on an APAC-centric early user base; if measured user or
  LLM-provider latency argues otherwise, moving regions before there is
  meaningful data gravity is cheap (Cloud SQL export/import plus DNS).
- Two projects double the quota/IAM bookkeeping but keep staging
  experiments (e.g. rebuild replays against imported corpora) from
  touching prod quotas or secrets.
- The workstation/on-prem compose distribution is unchanged; GCP assets
  are additive under `deploy/saas/` when phase 3 begins.
