CREATE TABLE IF NOT EXISTS session_events (
    scope_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL,
    content TEXT NOT NULL,
    task_ref TEXT NOT NULL DEFAULT '',
    thread_ref TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, event_id),
    UNIQUE (scope_id, agent_id, session_id, sequence)
);

CREATE INDEX IF NOT EXISTS session_events_stream_idx
    ON session_events (scope_id, agent_id, session_id, sequence);

CREATE TABLE IF NOT EXISTS session_streams (
    scope_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    last_sequence BIGINT NOT NULL DEFAULT 0,
    extraction_cursor BIGINT NOT NULL DEFAULT 0,
    complete BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, agent_id, session_id)
);

CREATE TABLE IF NOT EXISTS extraction_runs (
    scope_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    from_sequence BIGINT NOT NULL,
    to_sequence BIGINT NOT NULL,
    input_checksum TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (scope_id, run_id),
    UNIQUE (scope_id, agent_id, session_id, input_checksum)
);

CREATE TABLE IF NOT EXISTS note_candidates (
    scope_id TEXT NOT NULL,
    candidate_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    action TEXT NOT NULL,
    kind TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    task_ref TEXT NOT NULL DEFAULT '',
    thread_ref TEXT NOT NULL DEFAULT '',
    origin_user_id TEXT NOT NULL,
    origin_agent_id TEXT NOT NULL,
    origin_session_id TEXT NOT NULL,
    audience_agent_ids TEXT[] NOT NULL DEFAULT '{}',
    evidence_event_ids TEXT[] NOT NULL,
    admission_status TEXT NOT NULL DEFAULT 'pending',
    rejection_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, candidate_id),
    FOREIGN KEY (scope_id, run_id) REFERENCES extraction_runs(scope_id, run_id)
);

CREATE TABLE IF NOT EXISTS team_notes (
    scope_id TEXT NOT NULL,
    note_id TEXT NOT NULL,
    note_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    task_ref TEXT NOT NULL DEFAULT '',
    thread_ref TEXT NOT NULL DEFAULT '',
    origin_user_id TEXT NOT NULL,
    origin_agent_id TEXT NOT NULL,
    origin_session_id TEXT NOT NULL,
    audience_agent_ids TEXT[] NOT NULL DEFAULT '{}',
    state TEXT NOT NULL,
    current_revision INTEGER NOT NULL,
    soft_expires_at TIMESTAMPTZ NOT NULL,
    hard_expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope_id, note_id),
    UNIQUE (scope_id, note_key)
);

CREATE INDEX IF NOT EXISTS team_notes_recall_idx
    ON team_notes (scope_id, state, task_ref, thread_ref, soft_expires_at);

CREATE TABLE IF NOT EXISTS note_revisions (
    scope_id TEXT NOT NULL,
    note_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    candidate_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope_id, note_id, revision),
    UNIQUE (scope_id, candidate_id),
    FOREIGN KEY (scope_id, note_id) REFERENCES team_notes(scope_id, note_id),
    FOREIGN KEY (scope_id, candidate_id) REFERENCES note_candidates(scope_id, candidate_id)
);

CREATE TABLE IF NOT EXISTS note_evidence (
    scope_id TEXT NOT NULL,
    note_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    event_id TEXT NOT NULL,
    PRIMARY KEY (scope_id, note_id, revision, event_id),
    FOREIGN KEY (scope_id, note_id, revision)
        REFERENCES note_revisions(scope_id, note_id, revision),
    FOREIGN KEY (scope_id, event_id) REFERENCES session_events(scope_id, event_id)
);

CREATE TABLE IF NOT EXISTS note_deliveries (
    scope_id TEXT NOT NULL,
    note_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    recipient_user_id TEXT NOT NULL,
    recipient_agent_id TEXT NOT NULL,
    recipient_session_id TEXT NOT NULL,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    context_tokens INTEGER NOT NULL,
    PRIMARY KEY (scope_id, note_id, revision, recipient_session_id),
    FOREIGN KEY (scope_id, note_id, revision)
        REFERENCES note_revisions(scope_id, note_id, revision)
);
