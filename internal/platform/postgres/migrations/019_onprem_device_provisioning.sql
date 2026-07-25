-- 019_onprem_device_provisioning.sql
-- Device-scoped agent provisioning (ADR docs/decisions/2026-07-24-device-scoped-agent-provisioning.md).
-- Replay-safe: runs on every boot, including on populated databases.

ALTER TABLE agent_credentials
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS provisioned_by TEXT,
    ADD COLUMN IF NOT EXISTS grantable_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE agent_enrollments
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS grantable_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE onprem_agents
    ADD COLUMN IF NOT EXISTS provisioned_by TEXT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_credentials_kind_check') THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_kind_check CHECK (kind IN ('agent', 'device'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_enrollments_kind_check') THEN
        ALTER TABLE agent_enrollments
            ADD CONSTRAINT agent_enrollments_kind_check CHECK (kind IN ('agent', 'device'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_credentials_provisioned_by_fkey') THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_provisioned_by_fkey
            FOREIGN KEY (provisioned_by) REFERENCES agent_credentials (credential_id) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'onprem_agents_provisioned_by_fkey') THEN
        ALTER TABLE onprem_agents
            ADD CONSTRAINT onprem_agents_provisioned_by_fkey
            FOREIGN KEY (provisioned_by) REFERENCES agent_credentials (credential_id) NOT VALID;
    END IF;
END $$;

-- Device rows carry no agent_id: replace the strict btrim checks with kind-aware ones.
-- The original checks were inline column constraints with auto-generated names, so
-- find them by definition instead of hardcoding the name.
DO $$
DECLARE
    found_name TEXT;
BEGIN
    SELECT conname INTO found_name
    FROM pg_constraint
    WHERE conrelid = 'agent_credentials'::regclass AND contype = 'c'
      AND conname <> 'agent_credentials_agent_id_kind_check'
      AND pg_get_constraintdef(oid) LIKE '%btrim(agent_id)%';
    IF found_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_credentials DROP CONSTRAINT %I', found_name);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_credentials_agent_id_kind_check') THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_agent_id_kind_check
            CHECK (kind = 'device' OR btrim(agent_id) <> '');
    END IF;
END $$;

DO $$
DECLARE
    found_name TEXT;
BEGIN
    SELECT conname INTO found_name
    FROM pg_constraint
    WHERE conrelid = 'agent_enrollments'::regclass AND contype = 'c'
      AND conname <> 'agent_enrollments_agent_id_kind_check'
      AND pg_get_constraintdef(oid) LIKE '%btrim(agent_id)%';
    IF found_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_enrollments DROP CONSTRAINT %I', found_name);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_enrollments_agent_id_kind_check') THEN
        ALTER TABLE agent_enrollments
            ADD CONSTRAINT agent_enrollments_agent_id_kind_check
            CHECK (kind = 'device' OR btrim(agent_id) <> '');
    END IF;
END $$;

-- Audit events may now be performed by a device credential.
DO $$
DECLARE
    found_name TEXT;
BEGIN
    SELECT conname INTO found_name
    FROM pg_constraint
    WHERE conrelid = 'onprem_audit_events'::regclass AND contype = 'c'
      AND conname <> 'onprem_audit_events_actor_kind_device_check'
      AND pg_get_constraintdef(oid) LIKE '%actor_kind%';
    IF found_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE onprem_audit_events DROP CONSTRAINT %I', found_name);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'onprem_audit_events_actor_kind_device_check') THEN
        ALTER TABLE onprem_audit_events
            ADD CONSTRAINT onprem_audit_events_actor_kind_device_check
            CHECK (actor_kind IN ('bootstrap', 'human', 'agent', 'device', 'system'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS agent_credentials_provisioned_by_active_idx
    ON agent_credentials (provisioned_by)
    WHERE provisioned_by IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS agent_credentials_device_kind_idx
    ON agent_credentials (created_at DESC, credential_id)
    WHERE kind = 'device';

CREATE INDEX IF NOT EXISTS onprem_agents_provisioned_by_idx
    ON onprem_agents (provisioned_by)
    WHERE provisioned_by IS NOT NULL;
