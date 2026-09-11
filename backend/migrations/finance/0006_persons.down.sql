-- UNSAFE ON POPULATED DATABASES.
-- Dropping finance.persons while transactions.person_id still references
-- it leaves every linked transaction with a dangling id. Migrations are
-- forward-only on real data — see backend/README.md ("Migrations are
-- forward-only"). This down is exercised by TestMigrationRoundTrip on an
-- empty DB only; do not run it via `make migrate-down` against a
-- workspace with data.

DROP INDEX IF EXISTS finance.transactions_workspace_person_idx;
ALTER TABLE finance.transactions DROP CONSTRAINT IF EXISTS transactions_person_fk;
DROP TABLE IF EXISTS finance.persons;
