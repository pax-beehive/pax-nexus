ALTER TABLE note_candidates
    ADD COLUMN IF NOT EXISTS valid_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invalid_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS source_occurred_at TIMESTAMPTZ;

ALTER TABLE team_notes
    ADD COLUMN IF NOT EXISTS valid_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invalid_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS source_occurred_at TIMESTAMPTZ;

ALTER TABLE note_revisions
    ADD COLUMN IF NOT EXISTS valid_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invalid_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS expired_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS team_notes_temporal_recall_idx
    ON team_notes (scope_id, state, invalid_at, source_occurred_at DESC);

CREATE INDEX IF NOT EXISTS team_notes_search_idx
    ON team_notes USING GIN (to_tsvector('simple', subject || ' ' || body));
