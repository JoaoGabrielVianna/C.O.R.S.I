-- Agent Memory: what an agent is told to remember between conversations.
--
-- This is NOT conversation history and NOT knowledge. History belongs to one
-- conversation and dies with it; memory belongs to the agent and survives
-- every thread it was learnt in. That single sentence is what dictates every
-- column below.
--
-- agent_id is ON DELETE RESTRICT, matching the rest of the chain. Agents are
-- soft-deleted, so the constraint never actually fires — the application
-- layer counts memories before removing an agent, the same way it counts
-- conversations (app/agents.go).
--
-- source_conversation_id is the one deliberately WEAK reference in the whole
-- module: a memory outlives the conversation it came from. ON DELETE SET NULL
-- covers the hard-delete case (someone running SQL); the ordinary case is a
-- soft delete, where the row stays and the read simply stops resolving a
-- title for it. Either way the memory survives and the interface says the
-- origin is gone instead of erroring.
--
-- source_message_seq is a plain integer, on purpose. Messages are hard-deleted
-- by truncate/regenerate, and a foreign key here would either block that or
-- silently erase provenance on every edit. It is a hint for a future deep
-- link, never a guarantee.
CREATE TABLE chat.agent_memories (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id           UUID NOT NULL,
    agent_id               UUID NOT NULL REFERENCES chat.agents (id) ON DELETE RESTRICT,
    -- Bounded at 2000: a memory is a distilled fact, not a document. Anything
    -- longer belongs to Sources, which is a different capability.
    content                TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 2000),
    -- How it was captured. Only the two mechanisms that exist are allowed;
    -- adding 'model' here is what a future function-calling capture would do.
    origin                 TEXT NOT NULL DEFAULT 'manual'
                           CHECK (origin IN ('manual', 'conversation')),
    source_conversation_id UUID REFERENCES chat.conversations (id) ON DELETE SET NULL,
    source_message_seq     BIGINT,
    -- Off is not deleted. Turning a memory off is a reversible experiment
    -- ("is this what is confusing the agent?"); deleting it is a decision.
    enabled                BOOLEAN NOT NULL DEFAULT TRUE,
    -- Only matters once the context budget starts cutting: pinned memories
    -- are selected first. It is the control the user understands, in place of
    -- a relevance algorithm they cannot see.
    pinned                 BOOLEAN NOT NULL DEFAULT FALSE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at             TIMESTAMPTZ
);

-- The selection every turn runs: one agent's live, enabled memories in
-- priority order. Ordered exactly as the context builder consumes them so the
-- read is an index scan rather than a sort.
CREATE INDEX agent_memories_context_idx
    ON chat.agent_memories (agent_id, pinned DESC, updated_at DESC)
    WHERE deleted_at IS NULL AND enabled;

-- The Memory page: everything the agent holds, enabled or not.
CREATE INDEX agent_memories_agent_idx
    ON chat.agent_memories (workspace_id, agent_id)
    WHERE deleted_at IS NULL;

-- Backs the FK's ON DELETE SET NULL and the provenance grouping the Brain
-- view draws its edges from.
CREATE INDEX agent_memories_source_conversation_idx
    ON chat.agent_memories (source_conversation_id)
    WHERE source_conversation_id IS NOT NULL;
