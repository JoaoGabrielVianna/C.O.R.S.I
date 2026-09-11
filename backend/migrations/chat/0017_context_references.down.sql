ALTER TABLE chat.messages
    DROP COLUMN IF EXISTS context_references;

ALTER TABLE chat.conversations
    DROP COLUMN IF EXISTS context_references;
