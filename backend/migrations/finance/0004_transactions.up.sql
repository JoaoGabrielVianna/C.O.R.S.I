CREATE TABLE finance.transactions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    account_id   UUID,                                  -- FK deferred until accounts entity lands
    category_id  UUID NOT NULL,
    type         finance.entry_type NOT NULL,
    person_id    UUID,                                  -- FK deferred until persons entity lands
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    description  TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 280),
    occurred_at  TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    -- composite FK enforces transaction.type == category.type at the DB
    CONSTRAINT transactions_category_fk
        FOREIGN KEY (category_id, type)
        REFERENCES finance.categories (id, type)
);

CREATE INDEX transactions_workspace_occurred_idx
    ON finance.transactions (workspace_id, occurred_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE INDEX transactions_workspace_category_idx
    ON finance.transactions (workspace_id, category_id)
    WHERE deleted_at IS NULL;

CREATE INDEX transactions_workspace_type_idx
    ON finance.transactions (workspace_id, type)
    WHERE deleted_at IS NULL;
