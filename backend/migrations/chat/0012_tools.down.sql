-- Reverses 0012. Both tables are dropped whole: they were created by this
-- migration and nothing older depends on them.
--
-- As everywhere in this timeline, `down` exists for the test harness and for
-- a local reset. On a populated database the direction is forward only, and
-- a real rollback is a restore from backup.
DROP TABLE IF EXISTS chat.tool_calls;
DROP TABLE IF EXISTS chat.agent_tools;
