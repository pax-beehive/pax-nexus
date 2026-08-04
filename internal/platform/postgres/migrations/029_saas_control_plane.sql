-- 029_saas_control_plane.sql
-- Multi-team SaaS control plane (M3): teams, team memberships and
-- invitations, team human sessions, and per-team agent registries and
-- credentials. team_id IS the scope_id. Human users stay global
-- (onprem_users); the on-prem single-installation tables are untouched
-- except the shared audit table gaining a scope column.
-- Replay-safe: runs on every boot, including on populated databases.

CREATE TABLE IF NOT EXISTS teams (
    team_id TEXT PRIMARY KEY CHECK (btrim(team_id) <> ''),
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    slug TEXT NOT NULL CHECK (btrim(slug) <> ''),
    created_by_user_id TEXT NOT NULL REFERENCES onprem_users(user_id),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    resource_version BIGINT NOT NULL DEFAULT 1 CHECK (resource_version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS teams_slug_idx
    ON teams (slug);

CREATE TABLE IF NOT EXISTS team_memberships (
    membership_id TEXT PRIMARY KEY CHECK (btrim(membership_id) <> ''),
    team_id TEXT NOT NULL REFERENCES teams(team_id),
    user_id TEXT NOT NULL REFERENCES onprem_users(user_id),
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'removed')),
    invited_by_membership_id TEXT REFERENCES team_memberships(membership_id),
    create_idempotency_key TEXT NOT NULL DEFAULT '',
    joined_at TIMESTAMPTZ NOT NULL,
    suspended_at TIMESTAMPTZ,
    removed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    resource_version BIGINT NOT NULL DEFAULT 1 CHECK (resource_version > 0)
);

-- One live membership per (user, team); unlike on-prem, a user may hold
-- live memberships in many teams at once.
CREATE UNIQUE INDEX IF NOT EXISTS team_memberships_live_user_team_idx
    ON team_memberships (user_id, team_id)
    WHERE status IN ('active', 'suspended');

CREATE UNIQUE INDEX IF NOT EXISTS team_memberships_create_idempotency_idx
    ON team_memberships (user_id, create_idempotency_key)
    WHERE create_idempotency_key <> '';

CREATE INDEX IF NOT EXISTS team_memberships_team_idx
    ON team_memberships (team_id, status, membership_id);

CREATE TABLE IF NOT EXISTS team_membership_invitations (
    invitation_id TEXT PRIMARY KEY CHECK (btrim(invitation_id) <> ''),
    team_id TEXT NOT NULL REFERENCES teams(team_id),
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    digest_key_version SMALLINT NOT NULL DEFAULT 0,
    target_issuer TEXT,
    target_subject TEXT,
    target_email TEXT,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_by_membership_id TEXT NOT NULL REFERENCES team_memberships(membership_id),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by_user_id TEXT REFERENCES onprem_users(user_id),
    created_membership_id TEXT REFERENCES team_memberships(membership_id),
    revoked_at TIMESTAMPTZ,
    accept_idempotency_key TEXT NOT NULL DEFAULT '',
    CHECK (target_subject IS NOT NULL OR target_email IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS team_membership_invitations_active_digest_idx
    ON team_membership_invitations (token_digest, expires_at)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS team_invitations_accept_idempotency_idx
    ON team_membership_invitations (accepted_by_user_id, accept_idempotency_key)
    WHERE accepted_by_user_id IS NOT NULL AND accept_idempotency_key <> '';

CREATE INDEX IF NOT EXISTS team_membership_invitations_team_idx
    ON team_membership_invitations (team_id, invitation_id);

CREATE TABLE IF NOT EXISTS team_human_sessions (
    session_id TEXT PRIMARY KEY CHECK (btrim(session_id) <> ''),
    user_id TEXT NOT NULL REFERENCES onprem_users(user_id),
    secret_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(secret_digest) = 32),
    digest_key_version SMALLINT NOT NULL DEFAULT 0,
    current_team_id TEXT REFERENCES teams(team_id),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS team_human_sessions_active_digest_idx
    ON team_human_sessions (secret_digest, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS team_agents (
    agent_id TEXT PRIMARY KEY CHECK (btrim(agent_id) <> ''),
    team_id TEXT NOT NULL REFERENCES teams(team_id),
    owner_membership_id TEXT NOT NULL REFERENCES team_memberships(membership_id),
    display_name TEXT NOT NULL CHECK (btrim(display_name) <> ''),
    description TEXT NOT NULL DEFAULT '',
    agent_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'retired')),
    directory_visible BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    resource_version BIGINT NOT NULL DEFAULT 1 CHECK (resource_version > 0),
    creation_idempotency_key TEXT NOT NULL DEFAULT '',
    retire_idempotency_key TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS team_agents_owner_idx
    ON team_agents (owner_membership_id, status, agent_id);

CREATE UNIQUE INDEX IF NOT EXISTS team_agents_owner_idempotency_idx
    ON team_agents (owner_membership_id, creation_idempotency_key)
    WHERE creation_idempotency_key <> '';

CREATE UNIQUE INDEX IF NOT EXISTS team_agents_owner_retire_idempotency_idx
    ON team_agents (owner_membership_id, retire_idempotency_key)
    WHERE retire_idempotency_key <> '';

-- Credentials precede enrollments so enrollments.consumed_credential_id can
-- reference them; the reference is deferred because the exchange marks the
-- enrollment consumed before inserting the credential row.
CREATE TABLE IF NOT EXISTS team_agent_credentials (
    credential_id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL REFERENCES teams(team_id),
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest) = 32),
    digest_key_version SMALLINT NOT NULL DEFAULT 0,
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> ''),
    owner_membership_id TEXT NOT NULL REFERENCES team_memberships(membership_id),
    agent_id TEXT NOT NULL REFERENCES team_agents(agent_id),
    label TEXT NOT NULL DEFAULT '',
    permissions TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    rotated_from_credential_id TEXT REFERENCES team_agent_credentials(credential_id),
    revoke_idempotency_key TEXT NOT NULL DEFAULT '',
    revoke_idempotency_actor_membership_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS team_agent_credentials_active_digest_idx
    ON team_agent_credentials (key_digest)
    WHERE revoked_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS team_agent_credentials_owner_revoke_idempotency_idx
    ON team_agent_credentials (revoke_idempotency_actor_membership_id, revoke_idempotency_key)
    WHERE revoke_idempotency_actor_membership_id <> '' AND revoke_idempotency_key <> '';

CREATE TABLE IF NOT EXISTS team_agent_enrollments (
    enrollment_id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL REFERENCES teams(team_id),
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    digest_key_version SMALLINT NOT NULL DEFAULT 0,
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> ''),
    membership_id TEXT NOT NULL REFERENCES team_memberships(membership_id),
    agent_id TEXT NOT NULL REFERENCES team_agents(agent_id),
    credential_label TEXT NOT NULL DEFAULT '',
    permissions TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    credential_expires_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    consumed_credential_id TEXT REFERENCES team_agent_credentials(credential_id) DEFERRABLE INITIALLY DEFERRED,
    revoked_at TIMESTAMPTZ,
    revoke_idempotency_key TEXT NOT NULL DEFAULT '',
    revoke_idempotency_actor_membership_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS team_agent_enrollments_active_digest_idx
    ON team_agent_enrollments (token_digest, expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS team_agent_enrollments_owner_revoke_idempotency_idx
    ON team_agent_enrollments (revoke_idempotency_actor_membership_id, revoke_idempotency_key)
    WHERE revoke_idempotency_actor_membership_id <> '' AND revoke_idempotency_key <> '';

-- Shared audit trail: on-prem rows keep the 'local-team' default, so
-- existing writers are unchanged; SaaS writers pass the team_id explicitly.
ALTER TABLE onprem_audit_events
    ADD COLUMN IF NOT EXISTS scope_id TEXT NOT NULL DEFAULT 'local-team';

CREATE INDEX IF NOT EXISTS onprem_audit_events_scope_idx
    ON onprem_audit_events (scope_id, audit_event_id);
