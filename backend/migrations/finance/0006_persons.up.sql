CREATE TABLE finance.persons (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    notes        TEXT NOT NULL DEFAULT '' CHECK (length(notes) <= 1000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX persons_workspace_active_idx
    ON finance.persons (workspace_id)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX persons_workspace_name_unique
    ON finance.persons (workspace_id, lower(name))
    WHERE deleted_at IS NULL;

-- Promote the previously-deferred person_id column on transactions to a real FK.
-- purchase_plans.person_id (separate migration, future) will reference the same table.
ALTER TABLE finance.transactions
    ADD CONSTRAINT transactions_person_fk
    FOREIGN KEY (person_id) REFERENCES finance.persons (id);

CREATE INDEX transactions_workspace_person_idx
    ON finance.transactions (workspace_id, person_id)
    WHERE person_id IS NOT NULL AND deleted_at IS NULL;
