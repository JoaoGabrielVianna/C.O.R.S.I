DROP INDEX IF EXISTS finance.transactions_workspace_status_idx;

ALTER TABLE finance.transactions
    DROP COLUMN IF EXISTS notes,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS payment_method,
    DROP COLUMN IF EXISTS status;

DROP TYPE IF EXISTS finance.transaction_source;
DROP TYPE IF EXISTS finance.payment_method;
DROP TYPE IF EXISTS finance.transaction_status;
