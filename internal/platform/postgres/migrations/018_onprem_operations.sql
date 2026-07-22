CREATE TABLE IF NOT EXISTS onprem_operation_events (
    operation_event_id BIGSERIAL PRIMARY KEY,
    attempt_id TEXT NOT NULL UNIQUE CHECK (btrim(attempt_id) <> ''),
    operation_kind TEXT NOT NULL CHECK (operation_kind IN (
        'observation.observe', 'memory.search', 'memory.get',
        'team_note.recall', 'extraction.run',
        'channel.send', 'channel.accept', 'channel.archive',
        'system.retention'
    )),
    outcome TEXT NOT NULL CHECK (outcome IN (
        'succeeded', 'rejected', 'failed', 'timed_out', 'cancelled'
    )),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('agent', 'human', 'system')),
    actor_user_id TEXT,
    actor_membership_id TEXT,
    actor_agent_id TEXT,
    actor_credential_id TEXT,
    session_id TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    input_items BIGINT NOT NULL DEFAULT 0 CHECK (input_items >= 0),
    accepted_items BIGINT NOT NULL DEFAULT 0 CHECK (accepted_items >= 0),
    duplicate_items BIGINT NOT NULL DEFAULT 0 CHECK (duplicate_items >= 0),
    result_items BIGINT NOT NULL DEFAULT 0 CHECK (result_items >= 0),
    delivered_items BIGINT NOT NULL DEFAULT 0 CHECK (delivered_items >= 0),
    evidence_items BIGINT NOT NULL DEFAULT 0 CHECK (evidence_items >= 0),
    hint_items BIGINT NOT NULL DEFAULT 0 CHECK (hint_items >= 0),
    reference_items BIGINT NOT NULL DEFAULT 0 CHECK (reference_items >= 0),
    input_tokens BIGINT CHECK (input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens >= 0),
    detail_kind TEXT,
    detail_id TEXT,
    error_code TEXT NOT NULL DEFAULT '',
    CHECK (completed_at >= started_at),
    CHECK ((detail_kind IS NULL) = (detail_id IS NULL))
);

CREATE INDEX IF NOT EXISTS onprem_operation_events_time_idx
    ON onprem_operation_events (started_at DESC, operation_event_id DESC);

CREATE INDEX IF NOT EXISTS onprem_operation_events_kind_outcome_idx
    ON onprem_operation_events (
        operation_kind, outcome, started_at DESC, operation_event_id DESC
    );

CREATE INDEX IF NOT EXISTS onprem_operation_events_agent_idx
    ON onprem_operation_events (
        actor_agent_id, started_at DESC, operation_event_id DESC
    ) WHERE actor_agent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS onprem_storage_snapshots (
    snapshot_id BIGSERIAL PRIMARY KEY,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    captured_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('complete', 'partial')),
    warning_codes TEXT[] NOT NULL DEFAULT '{}',
    database_physical_bytes BIGINT NOT NULL CHECK (database_physical_bytes >= 0),
    other_physical_bytes BIGINT NOT NULL CHECK (other_physical_bytes >= 0),
    components JSONB NOT NULL CHECK (jsonb_typeof(components) = 'array')
);

CREATE INDEX IF NOT EXISTS onprem_storage_snapshots_time_idx
    ON onprem_storage_snapshots (captured_at DESC, snapshot_id DESC);
