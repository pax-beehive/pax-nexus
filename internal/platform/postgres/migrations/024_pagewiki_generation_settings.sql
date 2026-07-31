CREATE TABLE IF NOT EXISTS pagewiki_generation_settings (
    scope_id            TEXT PRIMARY KEY,
    language            TEXT NOT NULL DEFAULT '',
    custom_instructions TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
