-- The invariant the code already assumed, now actually held.
--
-- ── Why this is a real invariant and not just a tidy idea ──────────────
-- `CreatePurchasePlan` writes installments numbered 1..N inside one
-- transaction. The screens render "3/12". `CancelFutureInstallments`
-- reasons about which ones are still scheduled. Two live rows both
-- claiming installment 3 of the same plan would make that label ambiguous
-- and would count the plan's third payment twice in every total.
--
-- `domain/transaction.go` stated this uniqueness in a comment while the
-- schema had only a NON-unique index and a CHECK. The code was relying on
-- a guarantee nothing enforced. Correcting the database rather than the
-- comment, because the semantics are real: an installment number is a
-- position within a plan, and a position holds one thing.
--
-- ── Why the index excludes soft-deleted rows ───────────────────────────
-- Cancelling a plan soft-deletes its future installments, and re-creating
-- a plan over the same ground must not be blocked by rows that no longer
-- exist for any reader. Same convention every other partial index in this
-- schema follows.
--
-- plan_id alone is enough to scope it: a plan belongs to exactly one
-- workspace, so (plan_id, installment_number) cannot span two.
CREATE UNIQUE INDEX transactions_plan_installment_unique
    ON finance.transactions (plan_id, installment_number)
    WHERE plan_id IS NOT NULL AND deleted_at IS NULL;
