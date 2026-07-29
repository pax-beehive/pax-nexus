-- Generalize session storage into evidence streams keyed by (source, stream_id).
-- Legacy agent-session rows derive stream_id = agent_id || ':' || session_id.
--
-- This file is replayed on every Migrate() call (no migration-tracking table),
-- so every step below is written to be cheap on repeat application: column
-- adds and index/constraint drops use IF NOT EXISTS / IF EXISTS (pure
-- catalog checks, no table access once already applied), and the one-time
-- backfill + primary-key swap below are gated behind a single check of
-- session_streams' current primary-key columns: once that PK already covers
-- (scope_id, source, stream_id), the whole block is skipped and a steady-
-- state boot costs only the pg_index/pg_attribute catalog lookup that makes
-- the check — no scan or write against session_events or session_streams.

ALTER TABLE session_events
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'text',
    ADD COLUMN IF NOT EXISTS author_kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS author_native_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS author_user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS media JSONB;

ALTER TABLE session_streams
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'team';

-- One-time backfill + primary-key swap, gated on session_streams' current PK
-- column set (read from pg_index/pg_attribute) rather than on row data, so a
-- steady-state boot short-circuits on a single catalog lookup instead of
-- scanning session_events/session_streams to prove there is nothing left to
-- backfill.
--
-- This must run — and therefore backfill session_events.stream_id — before
-- the CREATE UNIQUE INDEX on session_events (scope_id, source, stream_id,
-- sequence) further down. On a pre-021 database every legacy row still has
-- stream_id = '', and the old uniqueness was per (scope_id, agent_id,
-- session_id, sequence), not per scope, so two different sessions in the
-- same scope can legitimately share a sequence number (both have a sequence
-- 1 event, say). Building the new unique index against unbackfilled rows
-- would collide on (scope_id, 'agent-session', '', sequence) for exactly
-- that reason. Running the backfill inside this block, ahead of the index
-- section below, avoids that.
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
        UPDATE session_events
        SET stream_id = agent_id || ':' || session_id,
            author_native_id = agent_id,
            author_user_id = user_id
        WHERE stream_id = '';

        UPDATE session_streams
        SET stream_id = agent_id || ':' || session_id
        WHERE stream_id = '';

        ALTER TABLE session_streams DROP CONSTRAINT IF EXISTS session_streams_pkey;
        ALTER TABLE session_streams ADD PRIMARY KEY (scope_id, source, stream_id);
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

CREATE INDEX IF NOT EXISTS session_streams_actor_idx
    ON session_streams (scope_id, agent_id, session_id);
