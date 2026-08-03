-- Session audit projections (processor session_audit). Read-only derived
-- state: source evidence in session_events is never mutated. This file is
-- replayed on every Migrate() call, so every statement is idempotent.

CREATE TABLE IF NOT EXISTS audit_tool_calls (
    scope_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    call_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    input_summary TEXT NOT NULL DEFAULT '',
    risk_level TEXT NOT NULL DEFAULT 'low',
    risk_reasons JSONB NOT NULL DEFAULT '[]'::jsonb,
    approval_state TEXT NOT NULL DEFAULT 'unknown',
    occurred_at TIMESTAMPTZ NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, event_id)
);

CREATE INDEX IF NOT EXISTS audit_tool_calls_user_idx
    ON audit_tool_calls (scope_id, user_id, occurred_at);

CREATE INDEX IF NOT EXISTS audit_tool_calls_call_idx
    ON audit_tool_calls (scope_id, call_id);

CREATE TABLE IF NOT EXISTS audit_findings (
    finding_id BIGSERIAL PRIMARY KEY,
    scope_id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    severity TEXT NOT NULL,
    summary TEXT NOT NULL,
    evidence_event_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS audit_findings_scope_kind_idx
    ON audit_findings (scope_id, kind, created_at);

-- session_ids is kept alongside session_count so the upsert can merge a
-- correct distinct-session count across batches; session_count is maintained
-- as jsonb_array_length of the merged set.
CREATE TABLE IF NOT EXISTS audit_activity_daily (
    scope_id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    day DATE NOT NULL,
    event_count BIGINT NOT NULL DEFAULT 0,
    tool_call_count BIGINT NOT NULL DEFAULT 0,
    high_risk_count BIGINT NOT NULL DEFAULT 0,
    session_count BIGINT NOT NULL DEFAULT 0,
    session_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    tool_breakdown JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, user_id, agent_id, day)
);
