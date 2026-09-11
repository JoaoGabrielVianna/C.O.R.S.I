-- Reverting a publication is not the same as reverting a schema change.
--
-- The freeze trigger refuses to modify or delete a published release, and
-- that is the point of it: it survives a migration that means well. This
-- down migration therefore cannot remove the row, and does not try —
-- running it against a published Finance 1.0.0 will raise
-- restrict_violation from releases.freeze_published, which is the correct
-- and intended outcome.
--
-- The module status is a different fact: it describes the module now, not
-- what shipped, and nothing freezes it. It is returned to `partial`.
--
-- Forward-only remains the rule for a populated database. Real rollback is
-- a restore.

UPDATE releases.modules
   SET status      = 'partial',
       description = 'Personal financial records: transactions, categories, cards, people and purchase plans.',
       updated_at  = now()
 WHERE key = 'finance';

DELETE FROM releases.releases
 WHERE module_key = 'finance'
   AND version    = '1.0.0';
