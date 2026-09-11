ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_person_id_fkey     TO fixed_expenses_person_id_fkey;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_category_id_fkey   TO fixed_expenses_category_id_fkey;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_period_chk         TO fixed_expenses_period_chk;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_notes_check        TO fixed_expenses_notes_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_due_day_check      TO fixed_expenses_due_day_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_description_check  TO fixed_expenses_description_check;
ALTER TABLE finance.recurring_entries RENAME CONSTRAINT recurring_entries_amount_cents_check TO fixed_expenses_amount_cents_check;

ALTER INDEX finance.recurring_entries_workspace_category_idx RENAME TO fixed_expenses_workspace_category_idx;
ALTER INDEX finance.recurring_entries_workspace_active_idx   RENAME TO fixed_expenses_workspace_active_idx;
ALTER INDEX finance.recurring_entries_pkey                   RENAME TO fixed_expenses_pkey;

ALTER TABLE finance.recurring_entries RENAME TO fixed_expenses;

ALTER TYPE finance.recurring_status    RENAME TO fixed_status;
ALTER TYPE finance.recurring_frequency RENAME TO fixed_recurrence;
