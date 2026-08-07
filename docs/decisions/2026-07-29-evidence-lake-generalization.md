# Evidence Lake generalization

Status: accepted

Date: 2026-07-29

## Context

Team memory extracts from agent sessions only, but the bulk of team
knowledge lives in chat and email. The session lake's mechanism —
append-only ordered events, replay dedup, extraction cursors, bounded
slices — is source-agnostic; only its identity model (`Actor{user, agent,
session}`) and its paxm-only ingest path were session-specific. Measured
extraction evidence on multi-author group chat (GroupMemBench, see
`doc/groupmembench-eval.md`) shows a +35.9% relative token-F1 lift from
Team Note on chat-shaped sources, strongest on user-implicit facts, with
temporal and multi-hop as known gaps — the mechanism is worth generalizing;
quality remains gated by eval.

Alternatives considered and rejected: one lake per source (triplicates
ingest/dedup/cursor machinery and forces cross-lake recall merges), and
masquerading chat/email as pseudo-sessions (corrupts Actor semantics,
leaves no home for a visibility model).

## Decision

Generalize the session lake in place into an Evidence Lake
(`internal/evidencelake`), keyed by `Stream{source, stream_id}`.

- **Connector boundary.** Connectors are external programs (like paxm) in
  their own repositories. This repository owns only the ingest contract,
  storage, ordering, dedup, slices, and cursors — never source acquisition
  or transcription.
- **Flat unified contract.** One event shape for every source; `source`,
  `kind` (modality), and `type` (semantic) are closed registries enforced
  at ingest; source-specific structure goes into metadata, which SQL never
  queries. Filterable fields are indexed columns.
- **Ingest-assigned ordering.** The generic path assigns per-stream
  sequences server-side under a stream row lock; source ordering is never
  trusted. The paxm session endpoint keeps client-supplied sequences and
  stays byte-compatible; its handler maps onto `source=agent-session`
  streams (`stream_id = agent_id:session_id`).
- **Path separation.** The generic endpoint (`POST /v1/stream-batches`)
  rejects `source=agent-session`: agent-session evidence enters only via
  the session path, which owns client sequencing. This closes the
  empty-actor leak into the PageWiki consumer and the mixed
  client/server-assigned sequence race.
- **Scope-global dedup.** Dedup stays keyed on `(scope_id, event_id)`,
  not per stream. Connectors must supply event ids unique within the
  scope (prefix platform-native ids with the stream id where the platform
  guarantees only per-channel uniqueness). Revisited in Plan 2 before the
  first connector ships.
- **Visibility phase-gating.** The contract carries `visibility`, but this
  phase ingest accepts only `team`; private sources (email, DMs) wait for
  participant-level ACL, so stored data never needs cleanup.
- **Identity.** `author.native_id` is immutable evidence; `author.user_id`
  is a resolved installation identity, backfillable without touching
  evidence rows. The mapping is On-prem-Identity-owned and
  administrator-maintained (Plan 3).
- **Media.** Connectors transcribe (ASR/OCR/caption into `content`); the
  lake stores original bytes behind a `BlobStore` port (Plan 4).
  Until then, non-`text` kinds are rejected rather than silently dropping
  originals.
- **Migration under the replay-every-boot runner.** Migration 021 wraps
  its backfill and PK swap in one catalog-gated DO block keyed on the
  current PK column set, so steady-state boots touch only catalogs.
- **Naming.** The Operations API's `session_lake` storage-component key is
  a wire contract consumed by the web frontend and stays unchanged;
  everything else renames to Evidence Lake.

Delivery is a four-plan series; each ships working software: (1) contract,
storage, generic endpoint, rename — implemented, PR #33; (2) stream-keyed
extraction of non-session sources, gated on eval acceptance (fix the v3
sweep `recall_observation` failures first, then IM sources run the v3
protocol); (3) identity mapping and ingest-time resolution; (4) media blob
storage.

## Consequences

- paxm clients observe no change: receipts, status codes, and cursor
  semantics are identical; legacy writes additionally populate the
  generalized columns.
- Legacy actor-keyed queries (session reads, extraction cursors, PageWiki
  pending-stream scans) are now scoped `source = 'agent-session'` because
  the actor triple is no longer a primary key.
- Non-session streams are stored and filterable but not yet extracted;
  Team Note and PageWiki consume exactly what they consumed before until
  Plan 2.
- New sources cannot claim product quality by construction: acceptance is
  bound to the eval pipeline (full-domain v3 baseline, then per-source
  protocols), per the spec's acceptance section.
- The theoretical `agent_id` colon-collision in the stream-id derivation
  fails migration loudly rather than corrupting data; it is documented in
  the migration file.
