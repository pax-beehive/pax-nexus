CREATE TABLE IF NOT EXISTS agent_enrollments (
    enrollment_id TEXT PRIMARY KEY,
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> ''),
    agent_id TEXT NOT NULL CHECK (btrim(agent_id) <> ''),
    permissions TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS agent_enrollments_active_digest_idx
    ON agent_enrollments (token_digest, expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS agent_credentials (
    credential_id TEXT PRIMARY KEY,
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest) = 32),
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> ''),
    agent_id TEXT NOT NULL CHECK (btrim(agent_id) <> ''),
    permissions TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS agent_credentials_active_digest_idx
    ON agent_credentials (key_digest)
    WHERE revoked_at IS NULL;
