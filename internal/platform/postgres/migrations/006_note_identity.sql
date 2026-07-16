ALTER TABLE team_notes
    ADD COLUMN IF NOT EXISTS identity_version INTEGER NOT NULL DEFAULT 1;

UPDATE team_notes
SET note_key = note_key || octet_length(origin_agent_id)::text || ':' || origin_agent_id,
    identity_version = 2
WHERE identity_version < 2
  AND kind IN ('status', 'blocker', 'handoff');

UPDATE team_notes
SET identity_version = 2
WHERE identity_version < 2;

ALTER TABLE team_notes
    ALTER COLUMN identity_version SET DEFAULT 2;
