ALTER TABLE chat.messages
    DROP CONSTRAINT IF EXISTS messages_cache_read_tokens_check,
    DROP CONSTRAINT IF EXISTS messages_cache_creation_tokens_check,
    DROP CONSTRAINT IF EXISTS messages_reasoning_tokens_check;

ALTER TABLE chat.messages
    DROP COLUMN IF EXISTS cache_read_tokens,
    DROP COLUMN IF EXISTS cache_creation_tokens,
    DROP COLUMN IF EXISTS reasoning_tokens;
