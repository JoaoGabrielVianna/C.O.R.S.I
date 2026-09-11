ALTER TABLE IF EXISTS chat.agent_memories
    DROP COLUMN IF EXISTS model_proposed;

-- `ALTER TABLE IF EXISTS`, not a bare ALTER: TestMemoryReadFailureDegradesTheTurn
-- drops this table under the live stack on purpose, and the suite then rolls
-- the schema back before the next test. A down migration that assumed its
-- table was still there would turn that fault injection into a dirty
-- database for every test after it.
