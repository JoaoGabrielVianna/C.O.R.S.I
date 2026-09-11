ALTER TABLE chat.messages
    DROP COLUMN IF EXISTS reasoning,
    DROP COLUMN IF EXISTS reasoning_ms;
