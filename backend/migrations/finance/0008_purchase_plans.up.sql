-- A PurchasePlan represents a single purchase paid in N installments
-- (e.g., a 12× credit-card charge). The plan is the parent record; the
-- installments are real finance.transactions rows linked back via
-- plan_id + installment_number. See docs/totals-contract.md §3.
--
-- remaining_installments is materialized for cheap reads. It is set to
-- `installments` at create and recomputed by the cancel service from the
-- live installment rows. Marking an installment paid via PATCH
-- /transactions/{id} does NOT decrement it in v0.1 — see the MVP gap
-- noted in the cancel docs.

CREATE TABLE finance.purchase_plans (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id           UUID NOT NULL,
    person_id              UUID REFERENCES finance.persons (id),
    name                   TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    total_amount_cents     BIGINT NOT NULL CHECK (total_amount_cents > 0),
    installments           INTEGER NOT NULL CHECK (installments BETWEEN 2 AND 360),
    remaining_installments INTEGER NOT NULL CHECK (remaining_installments >= 0),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at             TIMESTAMPTZ
);

CREATE INDEX purchase_plans_workspace_active_idx
    ON finance.purchase_plans (workspace_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX purchase_plans_workspace_person_idx
    ON finance.purchase_plans (workspace_id, person_id)
    WHERE deleted_at IS NULL AND person_id IS NOT NULL;

-- Installments live as ordinary finance.transactions rows. plan_id binds
-- them to the parent; installment_number gives a stable 1..N ordinal so
-- the UI can render "3/12". Both fields are NULL on every non-plan row,
-- so the change is additive. The composite CHECK keeps the pair coherent.
ALTER TABLE finance.transactions
    ADD COLUMN plan_id            UUID NULL REFERENCES finance.purchase_plans (id),
    ADD COLUMN installment_number INTEGER NULL,
    ADD CONSTRAINT transactions_plan_installment_chk
        CHECK ((plan_id IS NULL     AND installment_number IS NULL)
            OR (plan_id IS NOT NULL AND installment_number IS NOT NULL
                AND installment_number >= 1));

-- Partial index supports the "list / cancel installments of a plan"
-- access pattern. Soft-deleted rows never participate.
CREATE INDEX transactions_workspace_plan_idx
    ON finance.transactions (workspace_id, plan_id, installment_number)
    WHERE plan_id IS NOT NULL AND deleted_at IS NULL;
