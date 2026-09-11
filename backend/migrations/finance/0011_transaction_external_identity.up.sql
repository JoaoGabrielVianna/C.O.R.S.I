-- Room for a transaction to say where it came from.
--
-- ── What this is, and what it deliberately is not ──────────────────────
-- It is SCHEMA READINESS. No import capability, no bulk endpoint, and no
-- tool asks for these fields; `transaction.create` is unchanged and a
-- transaction recorded from a conversation has neither of them set.
--
-- It exists now because adding a unique constraint to a table that already
-- holds a person's financial history is a different and worse operation
-- than adding it to a table that does not: the constraint would have to be
-- validated against real rows, and any pre-existing violation would have
-- to be resolved before the migration could complete. Doing it before the
-- first release costs one migration and no decisions.
--
-- ── Why a SOURCE and not just an id ────────────────────────────────────
-- Because "id 4471" is not an identity, it is an identity WITHIN a system.
-- A bank statement, a card issuer's export and a spreadsheet each number
-- their own rows from one, and they will collide. The pair is the
-- identity; either half alone is a guess.
--
-- ── Why both or neither ────────────────────────────────────────────────
-- A row with an id and no source cannot be matched against anything, and a
-- row with a source and no id says only that it came from somewhere. The
-- CHECK refuses both halves of that.
ALTER TABLE finance.transactions
    ADD COLUMN external_source TEXT,
    ADD COLUMN external_id     TEXT,
    ADD CONSTRAINT transactions_external_identity_chk
        CHECK ((external_source IS NULL     AND external_id IS NULL)
            OR (external_source IS NOT NULL AND external_id IS NOT NULL
                AND length(external_source) BETWEEN 1 AND 60
                AND length(external_id)     BETWEEN 1 AND 200));

-- The identity is unique per workspace, and only for rows that claim one.
--
-- ── Why soft-deleted rows are excluded, and what that means ────────────
-- Every partial index in this schema follows `WHERE deleted_at IS NULL`,
-- and the reason applies here too: a soft-deleted transaction is gone from
-- every read, so holding its external identity hostage would mean a row
-- the user removed can never be brought back by the source that produced
-- it.
--
-- The consequence is real and is a DECISION, not an oversight: deleting an
-- imported transaction RELEASES its identity, so a later import of the
-- same source row will create it again. That is the honest reading of a
-- delete — the record is gone, and the source still says it happened. An
-- import that must not resurrect deleted rows needs a suppression list,
-- which is a different concept and not this one.
CREATE UNIQUE INDEX transactions_workspace_external_identity_unique
    ON finance.transactions (workspace_id, external_source, external_id)
    WHERE external_id IS NOT NULL AND deleted_at IS NULL;
