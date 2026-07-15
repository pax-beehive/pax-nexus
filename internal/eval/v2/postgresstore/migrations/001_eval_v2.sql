CREATE TABLE IF NOT EXISTS eval_v2_runs (
    run_id TEXT PRIMARY KEY,
    dataset TEXT NOT NULL,
    dataset_revision TEXT NOT NULL,
    config_hash TEXT NOT NULL,
    config JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS eval_v2_trials (
    run_id TEXT NOT NULL REFERENCES eval_v2_runs(run_id),
    case_id TEXT NOT NULL,
    arm TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    result JSONB,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_id, case_id, arm)
);

CREATE INDEX IF NOT EXISTS eval_v2_trials_status_idx
    ON eval_v2_trials (run_id, status, case_id, arm);
