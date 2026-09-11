CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE finance.entry_type AS ENUM ('income', 'expense');

CREATE TABLE finance.categories (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    type         finance.entry_type NOT NULL,
    color        TEXT NOT NULL CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
    icon         TEXT NOT NULL CHECK (length(icon) BETWEEN 1 AND 40),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    -- composite uniqueness so transactions can FK against (id, type)
    -- and the DB enforces "transaction.type == category.type"
    UNIQUE (id, type)
);

CREATE INDEX categories_workspace_active_idx
    ON finance.categories (workspace_id)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX categories_workspace_name_unique
    ON finance.categories (workspace_id, lower(name))
    WHERE deleted_at IS NULL;
