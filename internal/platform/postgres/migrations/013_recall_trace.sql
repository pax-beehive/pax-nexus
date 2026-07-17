ALTER TABLE team_note_recall_observations
    ADD COLUMN IF NOT EXISTS trace JSONB NOT NULL DEFAULT '{}'::jsonb;
