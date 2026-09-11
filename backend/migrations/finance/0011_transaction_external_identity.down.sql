DROP INDEX IF EXISTS finance.transactions_workspace_external_identity_unique;
ALTER TABLE finance.transactions
    DROP CONSTRAINT IF EXISTS transactions_external_identity_chk,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS external_source;
