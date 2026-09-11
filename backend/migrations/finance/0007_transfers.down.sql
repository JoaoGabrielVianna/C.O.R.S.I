-- UNSAFE ON POPULATED DATABASES.
-- Dropping transfer_pair_id erases the only link between the two legs of
-- every existing transfer. Both legs survive as ordinary expense/income
-- rows and the totals contract's transfer-exclusion rule no longer
-- fires — dashboards start double-counting account-to-account moves.
-- Migrations are forward-only on real data; see backend/README.md.

DROP INDEX IF EXISTS finance.transactions_workspace_transfer_pair_idx;

ALTER TABLE finance.transactions
    DROP COLUMN IF EXISTS transfer_pair_id;
