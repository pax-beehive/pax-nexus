-- 030_saas_team_devices.sql
-- Team-scoped device enrollment and provisioning (the saas mirror of
-- 019_onprem_device_provisioning.sql): device-kind credentials, grantable
-- permission sets, and provisioned-by lineage on the team agent tables.
-- Replay-safe: runs on every boot, including on populated databases.

ALTER TABLE team_agent_credentials
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS provisioned_by TEXT,
    ADD COLUMN IF NOT EXISTS grantable_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE team_agent_enrollments
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS grantable_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE team_agents
    ADD COLUMN IF NOT EXISTS provisioned_by TEXT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_agent_credentials_kind_check'
          AND conrelid = 'team_agent_credentials'::regclass) THEN
        ALTER TABLE team_agent_credentials
            ADD CONSTRAINT team_agent_credentials_kind_check CHECK (kind IN ('agent', 'device'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_agent_enrollments_kind_check'
          AND conrelid = 'team_agent_enrollments'::regclass) THEN
        ALTER TABLE team_agent_enrollments
            ADD CONSTRAINT team_agent_enrollments_kind_check CHECK (kind IN ('agent', 'device'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_agent_credentials_provisioned_by_fkey'
          AND conrelid = 'team_agent_credentials'::regclass) THEN
        ALTER TABLE team_agent_credentials
            ADD CONSTRAINT team_agent_credentials_provisioned_by_fkey
            FOREIGN KEY (provisioned_by) REFERENCES team_agent_credentials (credential_id) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_agents_provisioned_by_fkey'
          AND conrelid = 'team_agents'::regclass) THEN
        ALTER TABLE team_agents
            ADD CONSTRAINT team_agents_provisioned_by_fkey
            FOREIGN KEY (provisioned_by) REFERENCES team_agent_credentials (credential_id) NOT VALID;
    END IF;
END $$;

-- Device rows carry an empty agent_id, which the 029 foreign keys forbid.
-- Replace them with the kind-aware rule 019 uses (agent rows keep a
-- non-empty agent_id; referential integrity for them is enforced the same
-- way the on-prem tables do it: at the store layer, not by FK).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'team_agent_credentials_agent_id_fkey'
          AND conrelid = 'team_agent_credentials'::regclass
    ) THEN
        ALTER TABLE team_agent_credentials DROP CONSTRAINT team_agent_credentials_agent_id_fkey;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_agent_credentials_agent_id_kind_check'
          AND conrelid = 'team_agent_credentials'::regclass) THEN
        ALTER TABLE team_agent_credentials
            ADD CONSTRAINT team_agent_credentials_agent_id_kind_check
            CHECK (kind = 'device' OR btrim(agent_id) <> '');
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'team_agent_enrollments_agent_id_fkey'
          AND conrelid = 'team_agent_enrollments'::regclass
    ) THEN
        ALTER TABLE team_agent_enrollments DROP CONSTRAINT team_agent_enrollments_agent_id_fkey;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_agent_enrollments_agent_id_kind_check'
          AND conrelid = 'team_agent_enrollments'::regclass) THEN
        ALTER TABLE team_agent_enrollments
            ADD CONSTRAINT team_agent_enrollments_agent_id_kind_check
            CHECK (kind = 'device' OR btrim(agent_id) <> '');
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS team_agent_credentials_provisioned_by_active_idx
    ON team_agent_credentials (provisioned_by)
    WHERE provisioned_by IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS team_agent_credentials_device_kind_idx
    ON team_agent_credentials (created_at DESC, credential_id)
    WHERE kind = 'device';

CREATE INDEX IF NOT EXISTS team_agents_provisioned_by_idx
    ON team_agents (provisioned_by)
    WHERE provisioned_by IS NOT NULL;
