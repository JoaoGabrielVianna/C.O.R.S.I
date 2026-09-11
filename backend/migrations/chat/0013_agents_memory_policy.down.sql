ALTER TABLE IF EXISTS chat.agents
    DROP COLUMN IF EXISTS memory_policy_mode,
    DROP COLUMN IF EXISTS memory_policy_notes;
