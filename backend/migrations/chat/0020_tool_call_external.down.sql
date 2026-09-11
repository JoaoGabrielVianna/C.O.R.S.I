-- Forward-only in a populated database; this exists for the integration
-- harness, which drops to a clean slate before each run.
DROP INDEX IF EXISTS chat.tool_calls_external_idx;
ALTER TABLE chat.tool_calls DROP COLUMN IF EXISTS external;
