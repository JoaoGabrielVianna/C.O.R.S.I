-- An LLM provider is any OpenAI-compatible endpoint — LiteLLM in practice,
-- but the wire contract is what matters, not the vendor.
--
-- api_key_cipher holds an AES-GCM sealed blob (see internal/platform/secrets).
-- The plaintext key never leaves the server: reads return only api_key_hint,
-- the last few characters, which exists purely so the UI can confirm *which*
-- key is stored without being able to display it.
CREATE TABLE chat.providers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL,
    name           TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    base_url       TEXT NOT NULL CHECK (length(base_url) BETWEEN 1 AND 500),
    api_key_cipher BYTEA NOT NULL,
    api_key_hint   TEXT NOT NULL DEFAULT '' CHECK (length(api_key_hint) <= 12),
    default_model  TEXT NOT NULL CHECK (length(default_model) BETWEEN 1 AND 120),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);

CREATE INDEX providers_workspace_active_idx
    ON chat.providers (workspace_id)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX providers_workspace_name_unique
    ON chat.providers (workspace_id, lower(name))
    WHERE deleted_at IS NULL;
