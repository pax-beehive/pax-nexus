ALTER TABLE note_candidates
    ADD COLUMN IF NOT EXISTS identity_ref TEXT NOT NULL DEFAULT '';

UPDATE team_notes
SET note_key = octet_length(task_ref)::text || ':' || task_ref
    || octet_length(thread_ref)::text || ':' || thread_ref
    || octet_length(kind)::text || ':' || kind
    || octet_length(regexp_replace(lower(btrim(subject)), '\s+', ' ', 'g'))::text || ':'
    || regexp_replace(lower(btrim(subject)), '\s+', ' ', 'g'),
    identity_version = 3
WHERE identity_version < 3
  AND kind IN ('blocker', 'artifact_reference');

UPDATE team_notes
SET identity_version = 3
WHERE identity_version < 3;

ALTER TABLE team_notes
    ALTER COLUMN identity_version SET DEFAULT 3;
