-- What a tool call DID, recorded as a fact about the run.
--
-- ── The defect this exists to close ────────────────────────────────────
-- A live financial agent answered "8 transações importadas" in a turn that
-- made ZERO tool calls, against a ledger holding zero transactions. The
-- runtime behaved correctly: nothing was written. The person was told the
-- opposite, because the product renders the model's prose and the ABSENCE
-- of a tool call is silent.
--
-- Silence is the problem. A turn that executed a write and a turn that
-- merely claimed one are indistinguishable at the presentation layer, and
-- for money that is the worst failure mode this system has.
--
-- ── Why `effect` has to be stored and not looked up ────────────────────
-- The question a receipt answers is "did a WRITE run in this turn?", and
-- today that can only be answered by asking the registry — which describes
-- the build running NOW, not the build that ran THEN. A capability whose
-- effect changed, or which was removed from the build, would silently
-- change or erase the answer for a turn that already happened.
--
-- The column records what the definition said at the moment of execution.
-- That is the same reason the release history is not derived.
--
-- ── Why the vocabulary grows a third value ─────────────────────────────
-- 'ok' and 'error' describe calls that RAN. A call the model asked for and
-- the runtime refused — an unauthorized capability, arguments that did not
-- validate — is neither: it did not fail at its job, it never got one.
-- Collapsing it into 'error' makes "was this attempted or merely asked
-- for" unanswerable, and that is precisely the distinction a receipt is.
--
-- Existing rows are backfilled to 'read'. Not a guess dressed as data: the
-- effect of past calls is genuinely unknown, and 'read' is the value that
-- claims the least. It means no historical turn will be presented as a
-- confirmed write on the strength of a default.

ALTER TABLE chat.tool_calls
    ADD COLUMN effect TEXT NOT NULL DEFAULT 'read';

ALTER TABLE chat.tool_calls
    ADD CONSTRAINT tool_calls_effect_check CHECK (effect IN ('read', 'write'));

ALTER TABLE chat.tool_calls
    DROP CONSTRAINT tool_calls_status_check;

ALTER TABLE chat.tool_calls
    ADD CONSTRAINT tool_calls_status_check
        CHECK (status IN ('ok', 'error', 'not_executed'));

-- The receipt query: every write this turn actually ran.
CREATE INDEX tool_calls_write_receipt_idx
    ON chat.tool_calls (workspace_id, conversation_id, message_id)
    WHERE effect = 'write';
