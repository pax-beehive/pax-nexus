CREATE TABLE IF NOT EXISTS pagewiki_type_registry (
    scope_id   TEXT NOT NULL,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, kind, name)
);
