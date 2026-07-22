CREATE TABLE IF NOT EXISTS onprem_installation_state (
    singleton_id SMALLINT PRIMARY KEY CHECK (singleton_id = 1),
    bootstrap_claimed_at TIMESTAMPTZ,
    bootstrap_claimed_by_membership_id TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

INSERT INTO onprem_installation_state (singleton_id, created_at, updated_at)
VALUES (1, now(), now())
ON CONFLICT (singleton_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS onprem_users (
    user_id TEXT PRIMARY KEY CHECK (btrim(user_id) <> ''),
    identity_issuer TEXT,
    identity_subject TEXT,
    email TEXT,
    email_verified BOOLEAN NOT NULL DEFAULT false,
    display_name TEXT NOT NULL DEFAULT '',
    identity_status TEXT NOT NULL CHECK (identity_status IN ('unclaimed', 'active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ,
    CHECK ((identity_issuer IS NULL) = (identity_subject IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS onprem_users_external_identity_idx
    ON onprem_users (identity_issuer, identity_subject)
    WHERE identity_issuer IS NOT NULL AND identity_subject IS NOT NULL;

CREATE TABLE IF NOT EXISTS onprem_memberships (
    membership_id TEXT PRIMARY KEY CHECK (btrim(membership_id) <> ''),
    user_id TEXT NOT NULL REFERENCES onprem_users(user_id),
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'removed')),
    invited_by_membership_id TEXT REFERENCES onprem_memberships(membership_id),
    joined_at TIMESTAMPTZ NOT NULL,
    suspended_at TIMESTAMPTZ,
    removed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    resource_version BIGINT NOT NULL DEFAULT 1 CHECK (resource_version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS onprem_memberships_live_user_idx
    ON onprem_memberships (user_id)
    WHERE status IN ('active', 'suspended');

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'onprem_installation_state_bootstrap_membership_fk'
    ) THEN
        ALTER TABLE onprem_installation_state
            ADD CONSTRAINT onprem_installation_state_bootstrap_membership_fk
            FOREIGN KEY (bootstrap_claimed_by_membership_id) REFERENCES onprem_memberships(membership_id)
            NOT VALID;
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS onprem_human_sessions (
    session_id TEXT PRIMARY KEY CHECK (btrim(session_id) <> ''),
    user_id TEXT NOT NULL REFERENCES onprem_users(user_id),
    secret_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(secret_digest) = 32),
    digest_key_version SMALLINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS onprem_human_sessions_active_digest_idx
    ON onprem_human_sessions (secret_digest, expires_at)
    WHERE revoked_at IS NULL;

ALTER TABLE onprem_human_sessions
    ADD COLUMN IF NOT EXISTS digest_key_version SMALLINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS onprem_membership_invitations (
    invitation_id TEXT PRIMARY KEY CHECK (btrim(invitation_id) <> ''),
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    digest_key_version SMALLINT NOT NULL DEFAULT 0,
    target_issuer TEXT,
    target_subject TEXT,
    target_email TEXT,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_by_membership_id TEXT NOT NULL REFERENCES onprem_memberships(membership_id),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by_user_id TEXT REFERENCES onprem_users(user_id),
    created_membership_id TEXT REFERENCES onprem_memberships(membership_id),
    revoked_at TIMESTAMPTZ,
    accept_idempotency_key TEXT NOT NULL DEFAULT '',
    CHECK (target_subject IS NOT NULL OR target_email IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS onprem_membership_invitations_active_digest_idx
    ON onprem_membership_invitations (token_digest, expires_at)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

ALTER TABLE onprem_membership_invitations
    ADD COLUMN IF NOT EXISTS digest_key_version SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS accept_idempotency_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS onprem_invitations_accept_idempotency_idx
    ON onprem_membership_invitations (accepted_by_user_id, accept_idempotency_key)
    WHERE accepted_by_user_id IS NOT NULL AND accept_idempotency_key <> '';

CREATE TABLE IF NOT EXISTS onprem_agents (
    agent_id TEXT PRIMARY KEY CHECK (btrim(agent_id) <> ''),
    owner_membership_id TEXT NOT NULL REFERENCES onprem_memberships(membership_id),
    display_name TEXT NOT NULL CHECK (btrim(display_name) <> ''),
    description TEXT NOT NULL DEFAULT '',
    agent_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'retired')),
    directory_visible BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    resource_version BIGINT NOT NULL DEFAULT 1 CHECK (resource_version > 0)
);

ALTER TABLE onprem_agents
    ADD COLUMN IF NOT EXISTS creation_idempotency_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS retire_idempotency_key TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS onprem_agents_owner_idx
    ON onprem_agents (owner_membership_id, status, agent_id);

CREATE UNIQUE INDEX IF NOT EXISTS onprem_agents_owner_idempotency_idx
    ON onprem_agents (owner_membership_id, creation_idempotency_key)
    WHERE creation_idempotency_key <> '';

CREATE UNIQUE INDEX IF NOT EXISTS onprem_agents_owner_retire_idempotency_idx
    ON onprem_agents (owner_membership_id, retire_idempotency_key)
    WHERE retire_idempotency_key <> '';

CREATE TABLE IF NOT EXISTS onprem_audit_events (
    audit_event_id BIGSERIAL PRIMARY KEY,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('bootstrap', 'human', 'agent', 'system')),
    actor_user_id TEXT,
    actor_membership_id TEXT,
    actor_agent_id TEXT,
    actor_credential_id TEXT,
    action TEXT NOT NULL CHECK (btrim(action) <> ''),
    target_kind TEXT NOT NULL CHECK (btrim(target_kind) <> ''),
    target_id TEXT NOT NULL CHECK (btrim(target_id) <> ''),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE agent_enrollments
    ADD COLUMN IF NOT EXISTS membership_id TEXT REFERENCES onprem_memberships(membership_id),
    ADD COLUMN IF NOT EXISTS credential_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS credential_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS digest_key_version SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS consumed_credential_id TEXT,
    ADD COLUMN IF NOT EXISTS revoke_idempotency_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS revoke_idempotency_actor_membership_id TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_credentials
    ADD COLUMN IF NOT EXISTS owner_membership_id TEXT REFERENCES onprem_memberships(membership_id),
    ADD COLUMN IF NOT EXISTS digest_key_version SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rotated_from_credential_id TEXT,
    ADD COLUMN IF NOT EXISTS revoke_idempotency_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS revoke_idempotency_actor_membership_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS agent_enrollments_owner_revoke_idempotency_idx;
CREATE UNIQUE INDEX IF NOT EXISTS agent_enrollments_owner_revoke_idempotency_idx
    ON agent_enrollments (revoke_idempotency_actor_membership_id, revoke_idempotency_key)
    WHERE revoke_idempotency_actor_membership_id <> '' AND revoke_idempotency_key <> '';

DROP INDEX IF EXISTS agent_credentials_owner_revoke_idempotency_idx;
CREATE UNIQUE INDEX IF NOT EXISTS agent_credentials_owner_revoke_idempotency_idx
    ON agent_credentials (revoke_idempotency_actor_membership_id, revoke_idempotency_key)
    WHERE revoke_idempotency_actor_membership_id <> '' AND revoke_idempotency_key <> '';

DO $$
BEGIN
    IF EXISTS (
        SELECT agent_id
        FROM (
            SELECT agent_id, user_id FROM agent_credentials
            UNION ALL
            SELECT agent_id, user_id FROM agent_enrollments
        ) identities
        GROUP BY agent_id
        HAVING count(DISTINCT user_id) > 1
    ) THEN
        RAISE EXCEPTION 'cannot migrate on-prem identity: agent_id is bound to multiple users'
            USING ERRCODE = '23505';
    END IF;
END
$$;

INSERT INTO onprem_users (
    user_id, display_name, identity_status, created_at, updated_at
)
SELECT user_id, user_id, 'unclaimed', min(created_at), min(created_at)
FROM (
    SELECT user_id, created_at FROM agent_credentials
    UNION ALL
    SELECT user_id, created_at FROM agent_enrollments
) legacy_users
GROUP BY user_id
ON CONFLICT (user_id) DO NOTHING;

INSERT INTO onprem_memberships (
    membership_id, user_id, role, status, joined_at, updated_at
)
SELECT 'legacy-membership-' || md5(user_id), user_id, 'member', 'active', created_at, created_at
FROM onprem_users
WHERE identity_status = 'unclaimed'
ON CONFLICT (membership_id) DO NOTHING;

INSERT INTO onprem_agents (
    agent_id, owner_membership_id, display_name, status, directory_visible, created_at, updated_at
)
SELECT identities.agent_id, memberships.membership_id, identities.agent_id, 'active', true,
       identities.created_at, identities.created_at
FROM (
    SELECT agent_id, min(user_id) AS user_id, min(created_at) AS created_at
    FROM (
        SELECT agent_id, user_id, created_at FROM agent_credentials
        UNION ALL
        SELECT agent_id, user_id, created_at FROM agent_enrollments
    ) all_identities
    GROUP BY agent_id
) identities
JOIN onprem_memberships memberships ON memberships.user_id = identities.user_id
ON CONFLICT (agent_id) DO NOTHING;

UPDATE agent_enrollments enrollments
SET membership_id = memberships.membership_id
FROM onprem_memberships memberships
WHERE enrollments.membership_id IS NULL
  AND memberships.user_id = enrollments.user_id
  AND memberships.status IN ('active', 'suspended');

UPDATE agent_credentials credentials
SET owner_membership_id = memberships.membership_id
FROM onprem_memberships memberships
WHERE credentials.owner_membership_id IS NULL
  AND memberships.user_id = credentials.user_id
  AND memberships.status IN ('active', 'suspended');

ALTER TABLE agent_enrollments
    ALTER COLUMN membership_id SET NOT NULL;

ALTER TABLE agent_credentials
    ALTER COLUMN owner_membership_id SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'agent_enrollments_consumed_credential_fk'
    ) THEN
        ALTER TABLE agent_enrollments
            ADD CONSTRAINT agent_enrollments_consumed_credential_fk
            FOREIGN KEY (consumed_credential_id) REFERENCES agent_credentials(credential_id)
            DEFERRABLE INITIALLY DEFERRED NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'agent_credentials_rotated_from_fk'
    ) THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_rotated_from_fk
            FOREIGN KEY (rotated_from_credential_id) REFERENCES agent_credentials(credential_id)
            NOT VALID;
    END IF;
END
$$;

ALTER TABLE agent_enrollments
    ALTER CONSTRAINT agent_enrollments_consumed_credential_fk
    DEFERRABLE INITIALLY DEFERRED;
