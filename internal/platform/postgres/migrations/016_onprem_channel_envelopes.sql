CREATE TABLE IF NOT EXISTS onprem_agent_identities (
    agent_id TEXT PRIMARY KEY CHECK (btrim(agent_id) <> ''),
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> ''),
    created_at TIMESTAMPTZ NOT NULL
);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM agent_credentials
        GROUP BY agent_id
        HAVING count(DISTINCT user_id) > 1
    ) THEN
        RAISE EXCEPTION 'cannot enable on-prem channel: agent_id is bound to multiple users'
            USING ERRCODE = '23505';
    END IF;
END
$$;

INSERT INTO onprem_agent_identities (agent_id, user_id, created_at)
SELECT agent_id, min(user_id), min(created_at)
FROM agent_credentials
GROUP BY agent_id
ON CONFLICT (agent_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS onprem_channel_envelopes (
    envelope_id TEXT PRIMARY KEY,
    from_user_id TEXT NOT NULL CHECK (btrim(from_user_id) <> ''),
    from_agent_id TEXT NOT NULL CHECK (btrim(from_agent_id) <> ''),
    to_user_id TEXT NOT NULL CHECK (btrim(to_user_id) <> ''),
    to_agent_id TEXT NOT NULL CHECK (btrim(to_agent_id) <> ''),
    payload_type TEXT NOT NULL CHECK (payload_type = 'knowledge_capsule'),
    payload_json JSONB NOT NULL,
    message TEXT NOT NULL DEFAULT '' CHECK (char_length(message) <= 1000),
    idempotency_key TEXT NOT NULL CHECK (
        btrim(idempotency_key) <> '' AND char_length(idempotency_key) <= 256
    ),
    status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'archived')),
    created_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS onprem_channel_envelopes_idempotency_idx
    ON onprem_channel_envelopes (from_user_id, from_agent_id, idempotency_key);

CREATE INDEX IF NOT EXISTS onprem_channel_envelopes_inbox_idx
    ON onprem_channel_envelopes (to_user_id, to_agent_id, status, created_at DESC, envelope_id DESC);

CREATE INDEX IF NOT EXISTS onprem_channel_envelopes_outbox_idx
    ON onprem_channel_envelopes (from_user_id, from_agent_id, status, created_at DESC, envelope_id DESC);
