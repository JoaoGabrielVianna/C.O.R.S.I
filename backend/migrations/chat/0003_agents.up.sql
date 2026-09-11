-- An agent is a named, reusable configuration of one model: system prompt,
-- sampling parameters, and how much history it is fed. Conversations point
-- at an agent rather than duplicating its settings, so editing the agent
-- changes every future turn without rewriting past ones.
--
-- provider_id is ON DELETE RESTRICT: providers are soft-deleted, and an
-- agent left pointing at a dead credential would fail at send time with a
-- much worse error than a refused delete.
CREATE TABLE chat.agents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL,
    provider_id   UUID NOT NULL REFERENCES chat.providers (id) ON DELETE RESTRICT,
    name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    description   TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 280),
    system_prompt TEXT NOT NULL DEFAULT '' CHECK (length(system_prompt) <= 20000),
    model         TEXT NOT NULL CHECK (length(model) BETWEEN 1 AND 120),
    temperature   REAL NOT NULL DEFAULT 0.7 CHECK (temperature BETWEEN 0 AND 2),
    max_tokens    INTEGER NOT NULL DEFAULT 4096 CHECK (max_tokens BETWEEN 1 AND 200000),
    -- How many trailing messages of a conversation are replayed to the model.
    -- Bounded so a long thread can't silently grow into a huge bill.
    history_limit SMALLINT NOT NULL DEFAULT 40 CHECK (history_limit BETWEEN 1 AND 200),
    accent        TEXT NOT NULL DEFAULT 'cyan' CHECK (length(accent) <= 20),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX agents_workspace_active_idx
    ON chat.agents (workspace_id)
    WHERE deleted_at IS NULL;

CREATE INDEX agents_provider_idx
    ON chat.agents (provider_id)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX agents_workspace_name_unique
    ON chat.agents (workspace_id, lower(name))
    WHERE deleted_at IS NULL;
