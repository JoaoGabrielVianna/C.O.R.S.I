DROP INDEX IF EXISTS chat.messages_workspace_created_idx;

ALTER TABLE chat.messages
    DROP COLUMN IF EXISTS estimated_prompt_tokens,
    DROP COLUMN IF EXISTS cost,
    DROP COLUMN IF EXISTS output_cost_per_token,
    DROP COLUMN IF EXISTS input_cost_per_token,
    DROP COLUMN IF EXISTS usage_source;
