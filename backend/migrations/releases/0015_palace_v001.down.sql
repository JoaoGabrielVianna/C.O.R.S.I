-- Removing a published release is removing a historical record.
--
-- It is written so the migration is reversible in a throwaway database,
-- where the whole timeline is applied and rolled back on every test. In a
-- populated database this is not a repair: the row is immutable by trigger
-- precisely because a published release is a statement about a moment, and
-- deleting one makes the history lie by omission. Forward-only applies
-- here as it does everywhere else.
--
-- The module row is removed only if nothing references it, which keeps a
-- rollback of this migration from taking a later Palace release with it.

DELETE FROM releases.releases WHERE module_key = 'palace' AND version = '0.0.1';
DELETE FROM releases.modules  WHERE key = 'palace'
  AND NOT EXISTS (SELECT 1 FROM releases.releases WHERE module_key = 'palace');
