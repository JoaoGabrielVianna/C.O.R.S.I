-- Dropping the columns loses which entity each past write touched.
--
-- That is lossy and unavoidable: the fact has nowhere to live in the
-- reverted schema. What it costs is resume safety for turns already
-- recorded — a resume after this would be back to knowing that a create
-- ran and not which one, which is the incident this migration answers.
-- Forward-only in a populated database, for this reason.

DROP INDEX IF EXISTS chat.tool_calls_effect_idx;
ALTER TABLE chat.tool_calls DROP CONSTRAINT IF EXISTS tool_calls_effect_type_shape;
ALTER TABLE chat.tool_calls DROP CONSTRAINT IF EXISTS tool_calls_effect_ref_complete;
ALTER TABLE chat.tool_calls DROP COLUMN IF EXISTS effect_id;
ALTER TABLE chat.tool_calls DROP COLUMN IF EXISTS effect_type;
