-- Each bounded context owns its own Postgres schema. Keeping finance
-- isolated here makes the eventual extraction into a standalone service
-- a `pg_dump --schema=finance` rather than a data-archaeology project.
CREATE SCHEMA IF NOT EXISTS finance;
