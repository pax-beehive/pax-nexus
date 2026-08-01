CREATE TABLE IF NOT EXISTS pagewiki_page_lifecycle (
    scope_id   TEXT NOT NULL,
    ordinal    BIGSERIAL,
    event_id   TEXT NOT NULL,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, event_id)
);

CREATE TABLE IF NOT EXISTS pagewiki_curation_runs (
    scope_id   TEXT NOT NULL,
    run_id     TEXT NOT NULL,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, run_id)
);

CREATE TABLE IF NOT EXISTS pagewiki_page_embeddings (
    scope_id   TEXT NOT NULL,
    page_id    TEXT NOT NULL,
    payload    JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, page_id)
);
