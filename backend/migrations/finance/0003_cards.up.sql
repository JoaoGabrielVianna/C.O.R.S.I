CREATE TYPE finance.card_network AS ENUM ('visa', 'mastercard', 'elo', 'amex');

CREATE TABLE finance.cards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    institution  TEXT NOT NULL CHECK (length(institution) BETWEEN 1 AND 80),
    network      finance.card_network NOT NULL,
    variant      TEXT NOT NULL DEFAULT '' CHECK (length(variant) <= 40),
    last4        TEXT NOT NULL CHECK (last4 ~ '^[0-9]{4}$'),
    limit_cents  BIGINT NOT NULL CHECK (limit_cents >= 0),
    closing_day  SMALLINT NOT NULL CHECK (closing_day BETWEEN 1 AND 28),
    due_day      SMALLINT NOT NULL CHECK (due_day BETWEEN 1 AND 28),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX cards_workspace_active_idx
    ON finance.cards (workspace_id)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX cards_workspace_name_unique
    ON finance.cards (workspace_id, lower(name))
    WHERE deleted_at IS NULL;
