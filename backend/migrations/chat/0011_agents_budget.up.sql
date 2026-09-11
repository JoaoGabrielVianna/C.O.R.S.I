-- Daily budget, per agent.
--
-- ── Why columns and not a table ────────────────────────────────────────
-- A budget is 1:1 with its agent, is read on every turn by a gate that has
-- already loaded the agent, and has no history of its own — changing a
-- limit does not rewrite anything, because consumption is derived from
-- chat.messages rather than accumulated in a counter (see below). A
-- separate table would buy a join on the hot path and a second row to keep
-- in step, in exchange for nothing.
--
-- ── Why NULL and not 0 ─────────────────────────────────────────────────
-- NULL is "no limit". Zero is a limit of zero, which is a legitimate way to
-- freeze an agent. The module already spends this distinction on cost
-- (0011's neighbour, chat.messages.cost) and must not collapse it here:
-- an agent whose limit reads 0 because nobody set one would be an agent
-- that refuses every turn.
--
-- ── Why there is no `current_spend` column ─────────────────────────────
-- There is exactly one truth about what an agent consumed today, and it is
-- the turns it took. Tokens and cost are stamped onto every assistant
-- message; the gate SUMs those rows over the day. A mutable counter beside
-- them would be a second truth that drifts on every crash, retry, deleted
-- conversation and manual correction — and the first time the two disagree,
-- neither can be trusted.
ALTER TABLE chat.agents
    -- Tokens, prompt plus completion, per UTC day. Enforced against the
    -- provider's own counts where they exist and against our estimate where
    -- they do not, exactly as chat.messages records them.
    ADD COLUMN daily_token_limit INTEGER
        CHECK (daily_token_limit IS NULL OR daily_token_limit >= 0),

    -- US dollars per UTC day. USD because that is the currency the gateway
    -- prices and bills in; any other currency here would be a conversion
    -- stored as if it were a fact, and the rate would be stale the next day.
    ADD COLUMN daily_cost_limit_usd DOUBLE PRECISION
        CHECK (daily_cost_limit_usd IS NULL OR daily_cost_limit_usd >= 0);

-- No new index, and that is a measured decision rather than an omission.
--
-- The gate runs this on every turn of a budgeted agent:
--
--   SELECT ... FROM chat.messages m
--     JOIN chat.conversations c ON c.id = m.conversation_id
--    WHERE c.agent_id = $1 AND m.workspace_id = $2
--      AND m.role = 'assistant'
--      AND m.created_at >= $3 AND m.created_at < $4
--
-- An index on chat.conversations (agent_id, workspace_id) was written first,
-- on the theory that the planner would otherwise scan the whole workspace's
-- day. It was then measured against 60.000 messages across 50 agents and
-- three days, and it earned nothing:
--
--   with the index      3.96 / 3.90 / 3.75 ms
--   without the index   3.96 / 3.82 ms
--
-- The planner drives from messages_workspace_created_idx (0007) and hashes
-- the agent's forty conversations from conversations_agent_idx (0004),
-- which already exists. The extra index never became the deciding factor,
-- so it was removed rather than kept as a plausible-sounding write cost on
-- every conversation insert.
--
-- If an agent ever accumulates enough history for this to matter, the plan
-- to reach for is a nested loop from conversations into
-- messages_conversation_seq_idx — and the evidence for needing it should be
-- a slow query, not a hunch.
