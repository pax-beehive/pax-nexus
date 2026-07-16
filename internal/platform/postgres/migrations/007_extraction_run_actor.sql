ALTER TABLE extraction_runs
    ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';

UPDATE extraction_runs AS run
SET user_id = stream.user_id
FROM session_streams AS stream
WHERE run.user_id = ''
  AND run.scope_id = stream.scope_id
  AND run.agent_id = stream.agent_id
  AND run.session_id = stream.session_id;
