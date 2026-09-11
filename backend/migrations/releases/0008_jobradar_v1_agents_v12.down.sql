-- Removing the two rows and restoring the module's former lifecycle.
--
-- Guarded on module and version so nothing else in either timeline goes
-- with them. Written because every migration in this timeline has a down,
-- not because reverting a published release is a normal operation: both
-- rows are frozen by trigger against UPDATE, and deleting one erases a
-- historical fact rather than correcting it. If a snapshot is wrong, the
-- honest fix is another release.
DELETE FROM releases.releases
 WHERE module_key = 'job-radar' AND version = '1.0.0';

DELETE FROM releases.releases
 WHERE module_key = 'agents' AND version = '1.2.0';

UPDATE releases.modules
   SET status = 'frozen', updated_at = now()
 WHERE key = 'job-radar' AND status = 'active';
