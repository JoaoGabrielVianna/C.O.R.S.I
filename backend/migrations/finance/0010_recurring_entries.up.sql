-- FixedExpense becomes RecurringEntry.
--
-- ── Why this is a rename and not a new table ───────────────────────────
-- The concept was right and the NAME was half of it. `fixed_expenses`
-- could only ever hold money going out, which made a salary — the most
-- ordinary recurring entry a person has — unrepresentable, and pointed at
-- a future `fixed_incomes` table beside it: two tables, two sets of
-- operations, two capabilities, one concept.
--
-- ── Why "entry" ────────────────────────────────────────────────────────
-- Because this schema already calls a directional money item an ENTRY:
-- `finance.entry_type` has been the income/expense enum since migration
-- 0002, and both categories and transactions carry it. A recurring entry
-- is a thing with an entry type that repeats. Nothing is invented here.
--
-- "Commitment" was rejected: it means an obligation, and a salary is not
-- one. "Fixed" was rejected: what is fixed about a recurrence is that it
-- repeats, not that the amount never moves — the amount moves all the
-- time, which is why `update` exists.
--
-- ── Why the direction is NOT a column ──────────────────────────────────
-- It is derived from the category, exactly as `transactions.type` is
-- derived at write time from the category it references. A stored
-- direction would be a second copy free to drift the day a category is
-- recategorised. Transactions keep the copy because a composite foreign
-- key `(category_id, type)` makes the database enforce their agreement;
-- a recurring entry has no such pair, so the honest choice is to keep no
-- copy at all and read the direction through the category.
--
-- Renames, not drops: every object keeps its data, and an environment
-- that already holds rows migrates without losing one.

ALTER TYPE finance.fixed_recurrence RENAME TO recurring_frequency;
ALTER TYPE finance.fixed_status     RENAME TO recurring_status;

ALTER TABLE finance.fixed_expenses RENAME TO recurring_entries;

-- Indexes and constraints keep their old names through a table rename, so
-- they are renamed explicitly. A schema whose index is called
-- `fixed_expenses_*` on a table called `recurring_entries` is a schema that
-- tells two stories.
ALTER INDEX finance.fixed_expenses_pkey                    RENAME TO recurring_entries_pkey;
ALTER INDEX finance.fixed_expenses_workspace_active_idx    RENAME TO recurring_entries_workspace_active_idx;
ALTER INDEX finance.fixed_expenses_workspace_category_idx  RENAME TO recurring_entries_workspace_category_idx;

ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_amount_cents_check TO recurring_entries_amount_cents_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_description_check  TO recurring_entries_description_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_due_day_check      TO recurring_entries_due_day_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_notes_check        TO recurring_entries_notes_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_period_chk         TO recurring_entries_period_chk;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_category_id_fkey   TO recurring_entries_category_id_fkey;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT fixed_expenses_person_id_fkey     TO recurring_entries_person_id_fkey;
