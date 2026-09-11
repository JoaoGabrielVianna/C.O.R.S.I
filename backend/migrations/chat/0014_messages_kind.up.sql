-- What a row in chat.messages is: a turn, or a receipt.
--
-- ── The problem this solves ────────────────────────────────────────────
-- Consumption is derived from this table. Every usage aggregation reads
-- `role = 'assistant'` here, and the budget gate sums the same rows over a
-- UTC day (see 0011). A billable call that wrote no row here would be spend
-- no gate can see and no statement can report.
--
-- The next version adds exactly such a call: memory consolidation asks the
-- model to read a conversation and propose what is worth remembering. It is
-- not a turn — it produces no answer, it belongs to no exchange, and it must
-- never be replayed to the model — but it costs tokens like one.
--
-- ── The decision, and the debt it carries ──────────────────────────────
-- The alternative was a separate ledger table with a UNION in every usage
-- query. It was rejected because 0011 already fixed the rule this module
-- lives by: there is exactly ONE truth about what an agent consumed, and it
-- is the rows in this table. A second table of spend is the thing that
-- comment exists to forbid.
--
-- The cost is stated rather than hidden: chat.messages now holds records
-- that are not messages. If billable operations and conversation turns ever
-- diverge structurally — a receipt that needs fields a message has no use
-- for, or an operation with no conversation to belong to — this is the
-- decision to revisit, and a ledger of its own becomes the right shape.
--
-- ── Why a receipt carries no words ─────────────────────────────────────
-- The CHECK below is the whole safety argument. A reader that forgets to
-- filter `kind = 'turn'` shows an empty bubble; it cannot leak auxiliary
-- text into a transcript, and the context builder already drops an empty
-- message as an empty turn. The invariant is enforced by the database and
-- not merely by the writer, because the writer is one code path today and
-- an unknown number of them later.
ALTER TABLE chat.messages
    -- 'turn'      part of the conversation: replayed, shown, truncatable
    -- 'auxiliary' an accounting receipt for a billable operation that is
    --             not part of the conversation
    --
    -- Every row that already exists is a turn. The default says so, and no
    -- backfill is needed or possible: nothing but turns has ever been
    -- written here.
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'turn'
        CHECK (kind IN ('turn', 'auxiliary')),

    -- A receipt is an assistant-side record with no content. Both halves
    -- matter: assistant, because usage aggregation counts assistant rows
    -- and a receipt that was not one would be invisible to the budget; and
    -- empty, because that is what bounds the damage of a missing filter to
    -- something cosmetic.
    ADD CONSTRAINT messages_auxiliary_carries_no_words
        CHECK (kind = 'turn' OR (role = 'assistant' AND content = ''));

-- No new index, and the reasoning follows 0011's.
--
-- The conversation reads gain `AND kind = 'turn'` on top of
-- messages_conversation_seq_idx (conversation_id, seq), which they already
-- drive from. The filter is applied to a set already narrowed to one
-- thread's trailing rows, and auxiliary rows are rare by construction: one
-- per consolidation, against one per message. An index carrying `kind`
-- would add a write cost to every turn ever inserted to speed up a
-- predicate that discards almost nothing.
--
-- The usage aggregations are unchanged and deliberately do NOT filter kind:
-- they count what was consumed, and a receipt is consumed spend.
