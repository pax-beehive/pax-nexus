-- Generalize session storage into evidence streams keyed by (source, stream_id).
-- Legacy agent-session rows derive stream_id = agent_id || ':' || session_id.

ALTER TABLE session_events
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'text',
    ADD COLUMN IF NOT EXISTS author_kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS author_native_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS author_user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS media JSONB;

UPDATE session_events
SET stream_id = agent_id || ':' || session_id,
    author_native_id = agent_id,
    author_user_id = user_id
WHERE stream_id = '';

ALTER TABLE session_events
    DROP CONSTRAINT IF EXISTS session_events_scope_id_agent_id_session_id_sequence_key;

CREATE UNIQUE INDEX IF NOT EXISTS session_events_stream_sequence_key
    ON session_events (scope_id, source, stream_id, sequence);

CREATE INDEX IF NOT EXISTS session_events_source_kind_type_idx
    ON session_events (scope_id, source, kind, event_type);

CREATE INDEX IF NOT EXISTS session_events_author_user_idx
    ON session_events (scope_id, author_user_id);

CREATE INDEX IF NOT EXISTS session_events_occurred_at_idx
    ON session_events (scope_id, occurred_at);

CREATE INDEX IF NOT EXISTS session_events_thread_ref_idx
    ON session_events (scope_id, thread_ref);

ALTER TABLE session_streams
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'team';

UPDATE session_streams
SET stream_id = agent_id || ':' || session_id
WHERE stream_id = '';

ALTER TABLE session_streams
    DROP CONSTRAINT IF EXISTS session_streams_pkey;

ALTER TABLE session_streams
    ADD PRIMARY KEY (scope_id, source, stream_id);

CREATE INDEX IF NOT EXISTS session_streams_actor_idx
    ON session_streams (scope_id, agent_id, session_id);
