-- Removing a published release is removing a historical record.
--
-- It is written so the migration is reversible in a throwaway database,
-- where the whole timeline is applied and rolled back on every test. In a
-- populated database this is not a repair: the row is immutable by trigger
-- precisely because a published release is a statement about a moment, and
-- deleting one makes the history lie by omission. Forward-only applies
-- here as it does everywhere else.
--
-- The module description is restored to the sentence 0015 wrote, so that
-- rolling back this migration leaves the module row exactly as the
-- previous one left it.

DELETE FROM releases.releases WHERE module_key = 'palace' AND version = '0.0.2';

UPDATE releases.modules
   SET description = 'Persistent personal memory: what the operator wants to keep, what it came from, and how the pieces relate. Operated through an authorized agent — it has no screen of its own.'
 WHERE key = 'palace';
