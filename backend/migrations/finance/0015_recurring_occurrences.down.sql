-- Reverses 0015.
--
-- ── What a `down` is for here, and what it is not ──────────────────────
-- It exists for the disposable databases the integration suite creates and
-- destroys, which migrate down and up before every test. It is NOT the
-- rollback plan for production: Finance is forward-only on a populated
-- database, and running this there would drop every month the operator has
-- ticked. Real rollback is a restore from backup.
--
-- Order matters: the table goes before the types it uses, and the
-- constraints go before the column they constrain.

DROP INDEX IF EXISTS finance.recurring_occurrences_workspace_period_idx;
DROP INDEX IF EXISTS finance.recurring_occurrences_entry_period_idx;
DROP TABLE IF EXISTS finance.recurring_occurrences;

DROP TYPE IF EXISTS finance.occurrence_origin;
DROP TYPE IF EXISTS finance.occurrence_status;

ALTER TABLE finance.recurring_entries
    DROP CONSTRAINT IF EXISTS recurring_entries_monthly_has_no_due_month;
ALTER TABLE finance.recurring_entries
    DROP CONSTRAINT IF EXISTS recurring_entries_due_month_range;

ALTER TABLE finance.recurring_entries DROP COLUMN IF EXISTS due_month;
ALTER TABLE finance.recurring_entries DROP COLUMN IF EXISTS amount_varies;
