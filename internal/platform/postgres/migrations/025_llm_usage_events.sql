CREATE TABLE IF NOT EXISTS llm_usage_events (
    ordinal BIGSERIAL PRIMARY KEY,
    scope_id TEXT NOT NULL,
    component TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    cache_hit_tokens BIGINT NOT NULL DEFAULT 0,
    cache_miss_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS llm_usage_events_scope_time_idx
    ON llm_usage_events (scope_id, occurred_at);
