CREATE TABLE IF NOT EXISTS pagewiki_ingestion_settings (
    scope_id TEXT PRIMARY KEY,
    auto_inject BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS session_processor_cursors (
    processor_name TEXT NOT NULL,
    processor_version TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    committed_sequence BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (processor_name, processor_version, scope_id, agent_id, session_id)
);

CREATE TABLE IF NOT EXISTS pagewiki_source_revisions (
    scope_id TEXT NOT NULL,
    source_revision_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, source_revision_id)
);

CREATE TABLE IF NOT EXISTS pagewiki_publications (
    ordinal BIGSERIAL PRIMARY KEY,
    scope_id TEXT NOT NULL,
    page_revision_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scope_id, page_revision_id)
);

CREATE TABLE IF NOT EXISTS pagewiki_maintenance_runs (
    scope_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, run_id)
);

