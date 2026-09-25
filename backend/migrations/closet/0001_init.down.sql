-- Drops the whole bounded context.
--
-- Forward-only in a populated database, like every other timeline here:
-- this is the undo for a migration that has just been applied against an
-- empty schema, not a rollback path for production. Real rollback is a
-- restore from `make backup` — and for this context that matters more than
-- for the others, because the assets table holds the ONLY copy of the
-- cut-out PNGs. Nothing re-derives them.
DROP SCHEMA IF EXISTS closet CASCADE;
