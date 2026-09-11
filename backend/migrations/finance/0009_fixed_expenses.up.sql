-- A FixedExpense is a RECURRING COMMITMENT, not money that moved.
--
-- ── Why it is not a transaction, and must never become one silently ────
-- `finance.transactions` records what happened: an amount, on a date, that
-- either settled or is scheduled to. A fixed expense says "this repeats" —
-- rent every month, a gym every month, insurance every year. It has no
-- date of its own, only a due DAY, and it has no end unless someone ends
-- it.
--
-- Materialising one into transactions would invent money: twelve rows a
-- year that nobody confirmed, indistinguishable from real ones in every
-- total. So this table stores the commitment, and a payment against it is
-- an ordinary transaction someone records when it happens. Nothing here
-- writes to `finance.transactions`, and nothing should.
--
-- Until now this lived in the browser's localStorage, which meant the
-- screens could project a monthly commitment the agent had no way to see.
-- That was the last divergence between what the UI knew and what Ledger
-- knew.

CREATE TYPE finance.fixed_recurrence AS ENUM ('monthly', 'annual');
CREATE TYPE finance.fixed_status     AS ENUM ('active', 'paused');

CREATE TABLE finance.fixed_expenses (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    description  TEXT NOT NULL CHECK (length(description) BETWEEN 1 AND 280),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    -- The category gives the commitment the same vocabulary a transaction
    -- has, so "quanto comprometo com saúde" is answerable. Expense-side
    -- only is NOT enforced by a composite FK here: `categories` is unique
    -- on (id, type), but a commitment has no `type` column of its own to
    -- pair with — the application refuses an income category instead.
    category_id  UUID NOT NULL REFERENCES finance.categories (id),
    -- Optional, exactly like transactions: who this belongs to.
    person_id    UUID REFERENCES finance.persons (id),
    due_day      INTEGER NOT NULL CHECK (due_day BETWEEN 1 AND 31),
    recurrence   finance.fixed_recurrence NOT NULL DEFAULT 'monthly',
    status       finance.fixed_status     NOT NULL DEFAULT 'active',
    -- When the commitment began, and when it stopped recurring.
    --
    -- `ends_at` is how a cancellation is recorded: the row stays, so the
    -- history of having paid it stays true, and projection stops at the
    -- date. Deleting instead would rewrite the past — see the note on
    -- CancelPurchasePlan, which makes the same choice for installments.
    starts_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    ends_at      TIMESTAMPTZ,
    notes        TEXT NOT NULL DEFAULT '' CHECK (length(notes) <= 1000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT fixed_expenses_period_chk CHECK (ends_at IS NULL OR ends_at >= starts_at)
);

CREATE INDEX fixed_expenses_workspace_active_idx
    ON finance.fixed_expenses (workspace_id, due_day)
    WHERE deleted_at IS NULL;

CREATE INDEX fixed_expenses_workspace_category_idx
    ON finance.fixed_expenses (workspace_id, category_id)
    WHERE deleted_at IS NULL;
