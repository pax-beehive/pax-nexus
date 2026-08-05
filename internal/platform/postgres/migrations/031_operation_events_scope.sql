-- Operation events were written without an owning scope, so in a multi-team
-- deployment every team's Operations view read every other team's rows. The
-- backfill attributes all historical rows to the single-tenant scope: for an
-- on-prem install that is exactly right, and for a multi-team install the
-- attribution is unrecoverable, so parking them where no team sees them is the
-- conservative choice.
ALTER TABLE onprem_operation_events
    ADD COLUMN IF NOT EXISTS scope_id TEXT;

UPDATE onprem_operation_events SET scope_id = 'local-team' WHERE scope_id IS NULL;

ALTER TABLE onprem_operation_events
    ALTER COLUMN scope_id SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'onprem_operation_events_scope_not_blank'
          AND conrelid = 'onprem_operation_events'::regclass) THEN
        ALTER TABLE onprem_operation_events
            ADD CONSTRAINT onprem_operation_events_scope_not_blank
            CHECK (btrim(scope_id) <> '');
    END IF;
END $$;

-- The two pre-existing indexes lead with started_at / operation_kind. Every
-- read now filters on scope_id first, so without these the queries degrade to
-- a full scan once more than one scope exists.
CREATE INDEX IF NOT EXISTS onprem_operation_events_scope_time_idx
    ON onprem_operation_events (scope_id, started_at DESC, operation_event_id DESC);

CREATE INDEX IF NOT EXISTS onprem_operation_events_scope_kind_outcome_idx
    ON onprem_operation_events (
        scope_id, operation_kind, outcome, started_at DESC, operation_event_id DESC
    );
