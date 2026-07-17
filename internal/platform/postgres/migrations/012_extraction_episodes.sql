CREATE TABLE IF NOT EXISTS extraction_episodes (
    scope_id TEXT NOT NULL,
    task_ref TEXT NOT NULL DEFAULT '',
    thread_ref TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL CHECK (version > 0),
    checkpoint JSONB NOT NULL DEFAULT '{}'::jsonb,
    messages JSONB NOT NULL DEFAULT '[]'::jsonb,
    runs JSONB NOT NULL DEFAULT '{}'::jsonb,
    estimated_tokens BIGINT NOT NULL DEFAULT 0,
    event_count BIGINT NOT NULL DEFAULT 0,
    compaction_count BIGINT NOT NULL DEFAULT 0,
    protocol_version TEXT NOT NULL DEFAULT 'v1',
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, task_ref, thread_ref)
);

ALTER TABLE extraction_episodes
    ADD COLUMN IF NOT EXISTS runs JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE extraction_episodes
    ADD COLUMN IF NOT EXISTS protocol_version TEXT NOT NULL DEFAULT 'v1';
