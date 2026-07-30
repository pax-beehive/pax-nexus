CREATE TABLE IF NOT EXISTS todoapp_todos (
    scope_id TEXT NOT NULL,
    todo_id TEXT NOT NULL,
    status TEXT NOT NULL,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, todo_id)
);

CREATE INDEX IF NOT EXISTS todoapp_todos_status_idx
    ON todoapp_todos (scope_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS todoapp_suggestions (
    scope_id TEXT NOT NULL,
    suggestion_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    status TEXT NOT NULL,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, suggestion_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS todoapp_suggestions_fingerprint_idx
    ON todoapp_suggestions (scope_id, fingerprint);
