ALTER TABLE extraction_runs
    ADD COLUMN IF NOT EXISTS candidate_checksum TEXT NOT NULL DEFAULT '';
