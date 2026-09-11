CREATE TABLE chat.conversations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL,
    agent_id        UUID NOT NULL REFERENCES chat.agents (id) ON DELETE RESTRICT,
    title           TEXT NOT NULL DEFAULT '' CHECK (length(title) <= 200),
    last_message_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

-- The sidebar lists threads most-recently-active first, and a brand new
-- conversation has no messages yet — hence the coalesce onto created_at.
CREATE INDEX conversations_workspace_recent_idx
    ON chat.conversations (workspace_id, coalesce(last_message_at, created_at) DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX conversations_agent_idx
    ON chat.conversations (agent_id)
    WHERE deleted_at IS NULL;
