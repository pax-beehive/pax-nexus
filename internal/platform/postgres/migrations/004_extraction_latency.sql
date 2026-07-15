ALTER TABLE session_events
    ADD COLUMN IF NOT EXISTS extracted_at TIMESTAMPTZ;
