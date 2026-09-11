DROP INDEX IF EXISTS finance.transactions_workspace_plan_idx;

ALTER TABLE finance.transactions
    DROP CONSTRAINT IF EXISTS transactions_plan_installment_chk,
    DROP COLUMN IF EXISTS installment_number,
    DROP COLUMN IF EXISTS plan_id;

DROP INDEX IF EXISTS finance.purchase_plans_workspace_person_idx;
DROP INDEX IF EXISTS finance.purchase_plans_workspace_active_idx;

DROP TABLE IF EXISTS finance.purchase_plans;
