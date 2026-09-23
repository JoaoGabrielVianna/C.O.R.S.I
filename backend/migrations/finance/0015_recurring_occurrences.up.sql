-- One month of a recurring entry, as a row.
--
-- ══════════════════════════════════════════════════════════════════════
--
--   THE DEFINITION SAYS WHAT REPEATS
--   THE OCCURRENCE SAYS WHAT HAPPENED IN ONE MONTH
--   THE TRANSACTION SAYS MONEY MOVED
--
-- ══════════════════════════════════════════════════════════════════════
--
-- ── Why the paid state could not be a column on recurring_entries ──────
-- Because a boolean there has room for exactly one answer and the question
-- has one answer per month. Ticking "internet paid" in September and
-- unticking it in October would leave "was September's internet paid?"
-- permanently unanswerable, and the answer would already have been
-- overwritten by the time anybody asked. A monthly fact needs a monthly
-- row, and that is the whole of what this table is.
--
-- ── Why this is still NOT a transaction, and writes none ───────────────
-- Migration 0009 argued that materialising a recurrence into the ledger
-- invents money: twelve rows a year nobody confirmed, indistinguishable
-- from real ones in every total. Nothing here changes that. Marking an
-- occurrence paid is the OPERATOR asserting that an obligation was
-- settled; it touches `finance.transactions` in no way, and no total in
-- the frozen totals contract counts a row from this table.
--
-- So the monthly view and `finance.summary.get` are two separate readings
-- of the same month, exactly as the recurring summary already is, and they
-- are never added. A recurrence that was both ticked here and recorded as
-- a transaction appears in both, and summing them would count it twice.
--
-- ── Why transaction_id exists now, empty ───────────────────────────────
-- Nothing populates it and nothing should until linking a paid month to
-- its ledger row has a design of its own. It is added today because
-- adding a column to a table that already holds a person's financial
-- history is a materially worse operation than adding it to one that is
-- empty by definition — the same argument migration 0011 made for
-- `external_source` / `external_id`, which were also deliberately unused
-- on arrival.
--
-- ── NO BACKFILL ────────────────────────────────────────────────────────
-- This table starts empty and stays empty until the application reads a
-- month. An `INSERT ... SELECT` here would be eager materialisation by
-- another name: it would write rows for months nobody has looked at, at
-- today's amounts, and every one of them would be a reconstruction
-- presented as a record. The application marks reconstructions as
-- estimated; a migration has nowhere to say that and no month to say it
-- about.

/* ── the definition gains two facts ──────────────────────────────────── */

-- Whether the amount is the same every time.
--
-- It does NOT make amount_cents optional. The amount stays required and
-- becomes an ESTIMATE for a varying entry, which is what lets a monthly
-- view show a total at all before the electricity bill arrives. What the
-- flag buys is honesty about that total.
--
-- NOT NULL DEFAULT false is a metadata-only change on PostgreSQL 11+, so
-- it does not rewrite the table.
ALTER TABLE finance.recurring_entries
    ADD COLUMN amount_varies BOOLEAN NOT NULL DEFAULT false;

-- WHICH MONTH an annual obligation falls in, 1..12.
--
-- ── Why it is not derived from starts_at ───────────────────────────────
-- Because they answer different questions and the answers differ almost
-- always. `starts_at` says when the DEFINITION became applicable: the day
-- somebody wrote the obligation down, which is the day they happened to be
-- tidying their finances. `due_month` says when the MONEY is due: January,
-- for an IPVA, however September the data entry was.
--
-- Deriving one from the other would file every annual obligation in the
-- month of its own data entry, and the only way to correct it afterwards
-- would be to falsify the date the record began.
--
-- ── Why NULLABLE, when an annual entry must have one ───────────────────
-- Because this database already contains recurring entries and this
-- migration has no truthful value to give an annual one. Guessing January,
-- or the month of starts_at, would be inventing a due date for somebody's
-- real bill — the precise thing the column exists to stop.
--
-- So the constraint is split, and the split is deliberate:
--
--   monthly ⇒ due_month IS NULL      HERE, as a CHECK. It is true of
--                                    every row that exists, so the
--                                    database can hold it.
--
--   annual  ⇒ due_month IS NOT NULL  IN THE DOMAIN, in
--                                    RecurringEntry.Validate. A legacy
--                                    annual row would violate it, and a
--                                    CHECK that cannot be added is worse
--                                    than one that is honestly elsewhere.
--
-- The consequence is stated rather than hidden: an annual entry written
-- before this column is READABLE and INERT — it materialises no month,
-- because `RecurringEntry.OccursIn` refuses to guess — and it cannot be
-- saved again until somebody supplies a month. That is a visible,
-- recoverable refusal. The alternative is a bill that silently appears in
-- a total in a month the operator never agreed to.
ALTER TABLE finance.recurring_entries
    ADD COLUMN due_month SMALLINT;

ALTER TABLE finance.recurring_entries
    ADD CONSTRAINT recurring_entries_due_month_range
    CHECK (due_month IS NULL OR due_month BETWEEN 1 AND 12);

ALTER TABLE finance.recurring_entries
    ADD CONSTRAINT recurring_entries_monthly_has_no_due_month
    CHECK (recurrence <> 'monthly' OR due_month IS NULL);

/* ── the two small vocabularies ──────────────────────────────────────── */

-- Two values, and `overdue` is deliberately NOT one of them: being late is
-- a fact about the due date and today, derived when the row is read. A
-- stored `overdue` would be correct only until the clock moved past it,
-- and keeping it correct would need something to run every night.
CREATE TYPE finance.occurrence_status AS ENUM ('pending', 'paid');

-- Who put the row here. `materialized` is the system filling in what the
-- definitions imply for a month somebody opened; `manual` is the operator
-- recording a month the recurrence did not imply — a one-off extra charge,
-- a bill that came twice. Only the first is written today; the second
-- exists so that capability does not need a migration.
CREATE TYPE finance.occurrence_origin AS ENUM ('materialized', 'manual');

/* ── the occurrences ─────────────────────────────────────────────────── */

CREATE TABLE finance.recurring_occurrences (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL,
    recurring_entry_id UUID NOT NULL REFERENCES finance.recurring_entries (id),

    -- The month, always stored as its FIRST DAY.
    --
    -- A `date` rather than text so ordering, ranging and the half-open
    -- `>= ... < ...` idiom the rest of Finance already uses all work
    -- without parsing. The CHECK is what makes the column an identity
    -- rather than a date that happens to be in the right month: without
    -- it, a writer using "today" would produce a second row for a month
    -- that already has one, and the unique index below would not catch it.
    period  DATE NOT NULL,

    -- The day it fell due inside `period`, already clamped to a day the
    -- month has: 31 in February is the 28th, or the 29th in a leap year.
    --
    -- Stored rather than recomputed from the definition's `due_day`,
    -- because changing that day must not move a due date that has already
    -- passed. Frozen, like the amount.
    due_on  DATE NOT NULL,

    -- Integer cents, positive, like every other amount in this module. The
    -- direction comes from the definition's category and is never a column
    -- here, exactly as it is never a column on recurring_entries.
    --
    -- Copied from the definition when the row is created and NEVER
    -- recomputed from it. That is what makes history hold: the rent rising
    -- in October does not rewrite September.
    amount_cents     BIGINT NOT NULL CHECK (amount_cents > 0),

    -- Whether that number is a confirmed fact about this month.
    --
    -- Two situations set it, and they mean the same thing downstream:
    -- the definition's amount varies and the real figure has not arrived;
    -- or the month had already ended when the row was created, so the
    -- figure was RECONSTRUCTED from the definition as it stands now.
    --
    -- The second is the honest cost of being able to open a month nobody
    -- opened at the time. Presenting it as a recorded historical amount
    -- would be the product inventing a past.
    amount_estimated BOOLEAN NOT NULL DEFAULT false,

    status  finance.occurrence_status NOT NULL DEFAULT 'pending',
    -- When it was settled, from the database's clock. See the CHECK below.
    paid_at TIMESTAMPTZ,

    -- The future link to the ledger row. Nil today, always — see the
    -- header. No foreign key on purpose: unticking a checkbox must never
    -- be able to reach into `finance.transactions`, and a cascade is
    -- exactly a path by which it could.
    transaction_id UUID,

    origin     finance.occurrence_origin NOT NULL DEFAULT 'materialized',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- `period` names a month, so it is the first of one. Anything else is
    -- a caller that thought it was storing a date.
    CONSTRAINT recurring_occurrences_period_is_first_of_month
        CHECK (EXTRACT(DAY FROM period) = 1),

    -- The due date belongs to the month the row is filed under. This is
    -- what catches an unclamped `due_day`: Go's time.Date normalises
    -- "31 February" into 3 March, which would give a row a due date
    -- outside its own period and make "what is due in February" wrong by
    -- one bill.
    CONSTRAINT recurring_occurrences_due_on_in_period
        CHECK (due_on >= period AND due_on < (period + INTERVAL '1 month')),

    -- Paid and the moment of payment are one fact in two columns, so they
    -- agree or the row is refused. A paid row with no date is a record
    -- with nothing recorded in it.
    CONSTRAINT recurring_occurrences_paid_state
        CHECK ((status = 'paid') = (paid_at IS NOT NULL))
);

-- One occurrence per definition per month.
--
-- This index is not an optimisation, it is the concurrency design. Lazy
-- materialisation means two readers can decide at the same instant that
-- September is missing its rent, and both will insert. `ON CONFLICT DO
-- NOTHING` against this index is what makes the second one a no-op instead
-- of a duplicate, without a lock, a queue or a scheduler.
CREATE UNIQUE INDEX recurring_occurrences_entry_period_idx
    ON finance.recurring_occurrences (recurring_entry_id, period);

-- Backs the one query the monthly view makes: this workspace, this month,
-- in due order.
CREATE INDEX recurring_occurrences_workspace_period_idx
    ON finance.recurring_occurrences (workspace_id, period, due_on);
