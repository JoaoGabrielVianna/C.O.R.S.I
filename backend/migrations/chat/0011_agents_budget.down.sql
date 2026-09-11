ALTER TABLE chat.agents
    DROP COLUMN IF EXISTS daily_token_limit,
    DROP COLUMN IF EXISTS daily_cost_limit_usd;
