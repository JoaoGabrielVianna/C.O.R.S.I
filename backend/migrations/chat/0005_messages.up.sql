CREATE TYPE chat.message_role AS ENUM ('user', 'assistant');

CREATE TABLE chat.messages (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID NOT NULL,
    conversation_id   UUID NOT NULL REFERENCES chat.conversations (id) ON DELETE CASCADE,
    role              chat.message_role NOT NULL,
    content           TEXT NOT NULL,
    -- Stamped on assistant turns only: which model actually answered, and
    -- what it cost. Recorded per-message because an agent's model can change
    -- between turns and the thread should still read back truthfully.
    model             TEXT NOT NULL DEFAULT '',
    prompt_tokens     INTEGER NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    -- 'stop' | 'length' | 'aborted' | 'error'. A stream that dies mid-flight
    -- still persists whatever text arrived, so the thread never loses a
    -- half-written answer — it is stored with the reason it stopped.
    finish_reason     TEXT NOT NULL DEFAULT '' CHECK (length(finish_reason) <= 40),
    error             TEXT NOT NULL DEFAULT '' CHECK (length(error) <= 2000),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Ordering key. created_at alone is not enough: a user turn and its
    -- assistant reply can land inside the same clock tick.
    seq               BIGSERIAL NOT NULL
);

CREATE INDEX messages_conversation_seq_idx
    ON chat.messages (conversation_id, seq);
