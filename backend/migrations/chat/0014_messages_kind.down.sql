-- Dropping the column takes the receipts' only marking with it, so they
-- would read as ordinary empty assistant turns. Nothing here deletes them:
-- a down migration that removed rows would destroy the record of spend that
-- actually happened.
ALTER TABLE IF EXISTS chat.messages
    DROP CONSTRAINT IF EXISTS messages_auxiliary_carries_no_words,
    DROP COLUMN IF EXISTS kind;
