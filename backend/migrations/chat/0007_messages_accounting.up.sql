-- Turn accounting: what a turn consumed, at what price, for what cost.
--
-- Before this migration the only stored numbers were prompt_tokens and
-- completion_tokens, and cost was recomputed on every read against the
-- gateway's *current* rate card. That made a historical statement change
-- whenever a model's price changed, and it made "the rate card was down"
-- indistinguishable from "this turn was free".
--
-- The rule these columns exist to enforce:
--
--     the cost of a past turn must not depend on today's price list.
--
-- so the price applied is stamped alongside the tokens it was applied to.

ALTER TABLE chat.messages
    -- Where prompt_tokens/completion_tokens came from. This column, not the
    -- counts, is the authority on whether those numbers mean anything:
    --
    --   provider   the gateway sent a usage frame. Exact.
    --   estimated  it did not, but the request did reach the model, so the
    --              counts are our characters/4 heuristic. Approximate, and
    --              marked as such on every surface that shows them.
    --   unknown    we do not know what this turn consumed. The counts are
    --              zero because the column is NOT NULL, and zero here means
    --              *nothing is known*, never "nothing was spent".
    --
    -- Existing rows default to 'unknown' and the backfill below promotes
    -- only the ones that provably came from a usage frame.
    ADD COLUMN usage_source TEXT NOT NULL DEFAULT 'unknown'
        CHECK (usage_source IN ('unknown', 'provider', 'estimated')),

    -- The rate card in force when this turn happened, in dollars per single
    -- token, as the gateway reports it. NULL means the price was not known
    -- at the time — never 0, which would read as "free".
    ADD COLUMN input_cost_per_token  DOUBLE PRECISION
        CHECK (input_cost_per_token IS NULL OR input_cost_per_token >= 0),
    ADD COLUMN output_cost_per_token DOUBLE PRECISION
        CHECK (output_cost_per_token IS NULL OR output_cost_per_token >= 0),

    -- What the turn cost, frozen at the moment it happened. NULL when
    -- unknown, for the same reason as above.
    --
    -- Redundant with tokens x rates today, and stored anyway for two
    -- reasons: it is the number a statement shows, so it should have one
    -- authoritative value rather than being re-derived by each reader; and
    -- a future pricing shape that does not fit two per-token rates (cached
    -- input tiers, per-request fees) must not silently reprice these rows.
    ADD COLUMN cost DOUBLE PRECISION
        CHECK (cost IS NULL OR cost >= 0),

    -- What the Context Builder estimated the input would be, before the
    -- call. Kept beside the provider's real prompt_tokens so the heuristic
    -- can be calibrated against reality instead of trusted forever, and so
    -- a turn with no usage frame has something honest to fall back on.
    ADD COLUMN estimated_prompt_tokens INTEGER
        CHECK (estimated_prompt_tokens IS NULL OR estimated_prompt_tokens >= 0);

-- Backfill, and the limit of what it can honestly claim.
--
-- Nothing but a provider usage frame ever wrote a non-zero token count on
-- this table, so a row carrying one is provably 'provider'. A row at 0/0 is
-- either a turn the gateway never reported usage for or a turn that failed;
-- the two are indistinguishable after the fact, and both stay 'unknown'.
--
-- No price, no cost and no estimate is invented for any historical row. We
-- do not know what the rate card said last month, and a fabricated number
-- would be worse than an absent one.
UPDATE chat.messages
   SET usage_source = 'provider'
 WHERE role = 'assistant'
   AND (prompt_tokens > 0 OR completion_tokens > 0);

-- Usage aggregation has a time dimension now ("how much did today cost?"),
-- and the only index on this table was (conversation_id, seq). A daily
-- workspace query without this is a sequential scan.
--
-- Plain CREATE INDEX, not CONCURRENTLY: golang-migrate runs each migration
-- in a transaction, and this table is small enough in this deployment that
-- the brief write lock is not worth splitting the timeline over.
CREATE INDEX messages_workspace_created_idx
    ON chat.messages (workspace_id, created_at);
