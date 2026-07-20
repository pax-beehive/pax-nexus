CREATE TABLE IF NOT EXISTS recall_hint_deliveries (
    scope_id TEXT NOT NULL,
    lead_fingerprint TEXT NOT NULL,
    lead_note_id TEXT NOT NULL,
    recipient_user_id TEXT NOT NULL,
    recipient_agent_id TEXT NOT NULL,
    recipient_session_id TEXT NOT NULL,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    context_tokens INTEGER NOT NULL,
    PRIMARY KEY (scope_id, lead_fingerprint)
);
