-- Transfers are NOT a new entity. A transfer is two finance.transactions
-- rows sharing the same transfer_pair_id (UUID minted in the app layer
-- when the pair is created atomically inside a single tx). One leg is
-- expense (outflow), the other income (inflow). transfer_pair_id is
-- NULLABLE so every existing row stays valid unchanged.
--
-- Totals queries filter `transfer_pair_id IS NULL` so an inter-account
-- move never inflates dashboard income/expense — the overview total
-- before and after a transfer is identical.

ALTER TABLE finance.transactions
    ADD COLUMN transfer_pair_id UUID NULL;

-- Partial index — paired rows are a small slice of the table and we look
-- them up by (workspace_id, transfer_pair_id) when fetching a pair, and
-- by `transfer_pair_id IS NOT NULL` when excluding them from aggregates.
-- Mirrors the deleted_at IS NULL convention used by sibling indexes so
-- soft-deleted rows never occupy index space.
CREATE INDEX transactions_workspace_transfer_pair_idx
    ON finance.transactions (workspace_id, transfer_pair_id)
    WHERE transfer_pair_id IS NOT NULL AND deleted_at IS NULL;
