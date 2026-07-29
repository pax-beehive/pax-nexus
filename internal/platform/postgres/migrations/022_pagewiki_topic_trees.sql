CREATE TABLE IF NOT EXISTS pagewiki_topic_trees (
    scope_id TEXT PRIMARY KEY,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
