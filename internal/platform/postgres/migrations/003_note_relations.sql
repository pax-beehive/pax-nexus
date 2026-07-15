ALTER TABLE note_candidates
    ADD COLUMN IF NOT EXISTS related_subjects TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE team_notes
    ADD COLUMN IF NOT EXISTS related_subjects TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE note_revisions
    ADD COLUMN IF NOT EXISTS related_subjects TEXT[] NOT NULL DEFAULT '{}';
