-- Whether a tool call read a system this product does not own.
--
-- ── The defect this exists to close ────────────────────────────────────
-- A live content agent answered "1.535 seguidores, 40.055 views" in a turn
-- that made ZERO tool calls. The real figures, read moments later through
-- the capability, were 163 and 224. The runtime behaved correctly: nothing
-- was read. The person was told numbers that came from nowhere, because
-- the product renders the model's prose and the ABSENCE of a read is
-- silent.
--
-- This is the same shape as the write defect that produced `effect` in
-- 0018 — "8 transações importadas" with nothing written — and it has the
-- same answer: make the absence explicit instead of invisible.
--
-- ── Why `external` and not just `effect = read` ────────────────────────
-- Because most reads in this system are of OUR OWN state, and a claim
-- about our own Postgres is verifiable from our own Postgres at any time.
-- "You have three drafts" being stale is a refresh problem. "You have 163
-- followers" being invented is a different kind of wrong: the product
-- cannot check it, the user cannot check it without leaving, and the
-- number looks exactly as authoritative either way.
--
-- So the distinction that matters is not read-versus-write, it is
-- ours-versus-theirs.
--
-- ── Why the column is stored and not looked up ─────────────────────────
-- The same reason 0018 gives for `effect`. The registry describes the
-- build running NOW. A capability that stops being external, or that is
-- removed, would silently change or erase the answer for a turn that
-- already happened. The column records what the definition said at the
-- moment of execution.
--
-- ── Why the backfill is false ──────────────────────────────────────────
-- Not a guess dressed as data. Whether a historical call was external is
-- genuinely unknown, and `false` is the value that claims the least: it
-- means no past turn will be presented as a VERIFIED EXTERNAL READ on the
-- strength of a default. The direction of the lie matters — a receipt that
-- under-claims makes a true answer look unverified, which is recoverable;
-- one that over-claims is the bug this migration exists to prevent.
ALTER TABLE chat.tool_calls
    ADD COLUMN external BOOLEAN NOT NULL DEFAULT false;

-- The receipt read: one message's external calls, in the order they ran.
-- Partial, because the overwhelming majority of rows are not external and
-- an index over all of them would be mostly dead weight.
CREATE INDEX tool_calls_external_idx
    ON chat.tool_calls (workspace_id, message_id)
    WHERE external;
