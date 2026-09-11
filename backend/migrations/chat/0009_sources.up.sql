-- Agent Sources: reference material an agent can consult.
--
-- The distinction from memory is the whole reason this is a second table
-- and not a column on the first one:
--
--   Memory  is what the agent should REMEMBER about you. Short, distilled,
--           usually born inside a conversation.
--   Source  is what you handed it to STUDY. Long, titled, written on
--           purpose, and never born of a conversation.
--
-- Which is why there is no provenance here. A source has no origin to
-- resolve: it was written, not learnt.
--
-- agent_id is ON DELETE RESTRICT, matching the rest of the chain. Agents
-- are soft-deleted, so the constraint never actually fires — the
-- application layer counts sources before removing an agent, exactly as it
-- counts conversations and memories.
CREATE TABLE chat.agent_sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id     UUID NOT NULL REFERENCES chat.agents (id) ON DELETE RESTRICT,
    -- How the user recognises it in the list, and the anchor the model is
    -- given to cite it by ("according to the content strategy…").
    title        TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 120),
    -- One line about what it is and when it helps. NEVER sent to the model:
    -- describing a text that is itself being sent is paying twice for the
    -- same information.
    description  TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 280),
    -- 20.000 is the ceiling the module already chose for "long text written
    -- by the user" (agents.system_prompt). Inventing a second number would
    -- mean two answers to one question.
    content      TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 20000),
    -- Off is not deleted. It is the control that makes turning a source on
    -- a conscious cost decision rather than a permanent one.
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

-- The selection every turn runs: one agent's live, enabled sources in
-- priority order. Ordered exactly as the context builder consumes them, so
-- the read is an index scan rather than a sort.
CREATE INDEX agent_sources_context_idx
    ON chat.agent_sources (agent_id, updated_at DESC)
    WHERE deleted_at IS NULL AND enabled;

-- The Sources page: everything the agent holds, enabled or not.
CREATE INDEX agent_sources_agent_idx
    ON chat.agent_sources (workspace_id, agent_id)
    WHERE deleted_at IS NULL;
