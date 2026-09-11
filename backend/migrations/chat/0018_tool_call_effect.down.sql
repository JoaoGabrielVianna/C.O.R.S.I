-- Reverting a vocabulary is lossy, and pretending otherwise fails loudly.
--
-- The old CHECK allows only 'ok' and 'error'. Any row recorded since this
-- migration went up may say 'not_executed', and re-adding the narrower
-- constraint over those rows raises 23514 — which is what a straight
-- reversal did, leaving the timeline dirty.
--
-- So the collapse is made explicit: a call the runtime REFUSED becomes an
-- 'error', because that is the only thing the old vocabulary can say about
-- it. The distinction between "was stopped" and "broke" is destroyed, and
-- it is destroyed on the way DOWN, where losing it is the point of the
-- operation rather than a surprise inside it.
UPDATE chat.tool_calls SET status = 'error' WHERE status = 'not_executed';

DROP INDEX IF EXISTS chat.tool_calls_write_receipt_idx;
ALTER TABLE chat.tool_calls DROP CONSTRAINT IF EXISTS tool_calls_status_check;
ALTER TABLE chat.tool_calls
    ADD CONSTRAINT tool_calls_status_check CHECK (status IN ('ok', 'error'));
ALTER TABLE chat.tool_calls DROP CONSTRAINT IF EXISTS tool_calls_effect_check;
ALTER TABLE chat.tool_calls DROP COLUMN IF EXISTS effect;
