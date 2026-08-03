# Audit

Read-only governance projections over session evidence: normalized tool-call
audit rows with deterministic risk grading, policy-violation findings, and
per-day work-pattern activity per user/agent.

## Language

**Tool Call**: a `tool_call` session event normalized into an audit row with a
risk level (`low|medium|high|critical`) and rule-based reasons.

**Approval State**: `unknown|approved|denied`, correlated by `tool.call_id`
and merged monotonically (`unknown` may resolve, resolved states never move).

**Finding**: a persisted policy observation. Kinds: `high_risk_unapproved`,
`denied_tool_executed`, `visibility_unknown`, `attribution_missing`.

**Activity Day**: the per-(scope, user, agent, day) aggregate of event
counts, tool-call counts, high-risk counts, session set, and tool breakdown.

**Visibility Policy**: the hardcoded first-iteration allow set for
agent-session evidence visibility (`""`, `team`, `team_note_eligible`,
`team_visible`). Anything else is a finding, never a rejection.

## Relationships

- Consumes Evidence Lake agent-session streams through its own cursor in the
  shared `session_processor_cursors` table (processor `session_audit`,
  version `v1`). It never touches `extraction_cursor` and never subscribes to
  extraction.
- Never mutates source evidence and never enforces: no blocking, no
  rewriting, no back-pressure on ingest.
- Boundary with identity audit events: admin API action audit
  (`onprem_audit_events`, owned by the platform identity/operations modules)
  records who called which administrative API. This module records what
  agents did inside sessions. The two never share tables or findings.
- Boundary with operations events: operations telemetry is emitted by the
  runtime about fleet health; audit findings are derived from immutable
  evidence after the fact.
- Postgres adapter lives in `internal/platform/postgres/audit.go`; the
  producer contract for out-of-repo event producers is
  `docs/session-audit-contract.md`.
