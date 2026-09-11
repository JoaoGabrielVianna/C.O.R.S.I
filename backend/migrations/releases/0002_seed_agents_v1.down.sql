-- Only the draft can be withdrawn. If the owner has since published this
-- release, the trigger in 0001 refuses the delete and this migration
-- fails — which is the correct outcome: a published release is history,
-- and a down migration is not a licence to rewrite it.
DELETE FROM releases.releases WHERE module_key = 'agents' AND version = '1.0.0';
DELETE FROM releases.modules WHERE key IN ('agents', 'finance', 'job-radar');
