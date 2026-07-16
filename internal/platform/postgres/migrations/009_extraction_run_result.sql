ALTER TABLE extraction_runs
    ADD COLUMN IF NOT EXISTS result JSONB NOT NULL DEFAULT '[]'::jsonb;
