-- Generalize session storage into evidence streams keyed by (source, stream_id).
-- Legacy agent-session rows derive stream_id = agent_id || ':' || session_id.
--
-- This file is replayed on every Migrate() call (no migration-tracking table),
-- so every step below is written to be cheap on repeat application: column
-- adds use IF NOT EXISTS, backfills are gated behind an EXISTS check so a
-- fully-backfilled table costs one short-circuiting probe instead of a
-- full-table UPDATE, and the primary key swap on session_streams is skipped
-- once it already covers (scope_id, source, stream_id).

ALTER TABLE session_events
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'text',
    ADD COLUMN IF NOT EXISTS author_kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS author_native_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS author_user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS media JSONB;

-- Backfill legacy rows' stream identity. Gated: once every row has a
-- non-empty stream_id (the common case on every boot after the first),
-- this short-circuits on the EXISTS probe instead of paying for a
-- full-table UPDATE (and the WAL/row-version churn that comes with it).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM session_events WHERE stream_id = '' LIMIT 1) THEN
        UPDATE session_events
        SET stream_id = agent_id || ':' || session_id,
            author_native_id = agent_id,
            author_user_id = user_id
        WHERE stream_id = '';
    END IF;
END $$;

ALTER TABLE session_events
    DROP CONSTRAINT IF EXISTS session_events_scope_id_agent_id_session_id_sequence_key;

CREATE UNIQUE INDEX IF NOT EXISTS session_events_stream_sequence_key
    ON session_events (scope_id, source, stream_id, sequence);

CREATE INDEX IF NOT EXISTS session_events_source_kind_type_idx
    ON session_events (scope_id, source, kind, event_type);

CREATE INDEX IF NOT EXISTS session_events_author_user_idx
    ON session_events (scope_id, author_user_id);

CREATE INDEX IF NOT EXISTS session_events_author_native_idx
    ON session_events (scope_id, author_native_id);

CREATE INDEX IF NOT EXISTS session_events_occurred_at_idx
    ON session_events (scope_id, occurred_at);

CREATE INDEX IF NOT EXISTS session_events_thread_ref_idx
    ON session_events (scope_id, thread_ref);

ALTER TABLE session_streams
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'team';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM session_streams WHERE stream_id = '' LIMIT 1) THEN
        UPDATE session_streams
        SET stream_id = agent_id || ':' || session_id
        WHERE stream_id = '';
    END IF;
END $$;

-- Swap session_streams' primary key to (scope_id, source, stream_id), but
-- only if it doesn't already cover exactly those columns: DROP + ADD PRIMARY
-- KEY takes an ACCESS EXCLUSIVE lock and rebuilds the backing index, which
-- would otherwise happen on every app boot even though it's a no-op after
-- the first successful run.
--
-- Note: stream_id is derived by concatenating agent_id || ':' || session_id.
-- If an agent_id or session_id value itself contains a ':' such that two
-- distinct (agent_id, session_id) pairs collide onto the same stream_id
-- string, the ADD PRIMARY KEY below will fail with a duplicate-key error.
-- That is treated as a hard failure by design (no silent data loss); it has
-- not been observed in practice because agent/session identifiers are
-- generated, not user-supplied free text.
DO $$
DECLARE
    current_pk_columns TEXT;
BEGIN
    SELECT string_agg(a.attname, ',' ORDER BY array_position(i.indkey, a.attnum))
      INTO current_pk_columns
      FROM pg_index i
      JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
     WHERE i.indrelid = 'session_streams'::regclass
       AND i.indisprimary;

    IF current_pk_columns IS DISTINCT FROM 'scope_id,source,stream_id' THEN
        ALTER TABLE session_streams DROP CONSTRAINT IF EXISTS session_streams_pkey;
        ALTER TABLE session_streams ADD PRIMARY KEY (scope_id, source, stream_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS session_streams_actor_idx
    ON session_streams (scope_id, agent_id, session_id);
