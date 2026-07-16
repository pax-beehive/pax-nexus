CREATE TABLE IF NOT EXISTS team_note_recall_observations (
    observation_id BIGSERIAL PRIMARY KEY,
    scope_id TEXT NOT NULL,
    recipient_user_id TEXT NOT NULL,
    recipient_agent_id TEXT NOT NULL,
    recipient_session_id TEXT NOT NULL,
    task_ref TEXT NOT NULL DEFAULT '',
    thread_ref TEXT NOT NULL DEFAULT '',
    query_digest BYTEA NOT NULL,
    token_budget INTEGER NOT NULL,
    max_items INTEGER NOT NULL DEFAULT 0,
    extraction_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    extraction_provenance JSONB NOT NULL DEFAULT '{}'::jsonb,
    envelope JSONB NOT NULL,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '7 days'
);

ALTER TABLE team_note_recall_observations
    ADD COLUMN IF NOT EXISTS query_digest BYTEA,
    ADD COLUMN IF NOT EXISTS extraction_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS extraction_provenance JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '7 days';

-- Recall observations are transient diagnostics. Discard rows from any
-- pre-release schema that stored raw queries instead of a digest.
DELETE FROM team_note_recall_observations WHERE query_digest IS NULL;

ALTER TABLE team_note_recall_observations
    ALTER COLUMN query_digest SET NOT NULL,
    DROP COLUMN IF EXISTS query;

DROP INDEX IF EXISTS team_note_recall_observations_lookup_idx;

CREATE INDEX IF NOT EXISTS team_note_recall_observations_lookup_idx
    ON team_note_recall_observations (
        scope_id, recipient_user_id, recipient_agent_id,
        recipient_session_id, query_digest, token_budget, observation_id DESC
    );

CREATE INDEX IF NOT EXISTS team_note_recall_observations_expiry_idx
    ON team_note_recall_observations (expires_at);
