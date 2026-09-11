-- Removing the row.
--
-- Guarded on module and version so nothing else in the timeline goes with
-- it. Written because every migration here has a down, not because
-- reverting a published release is a normal operation: the row is frozen
-- by trigger against UPDATE, and deleting one erases a historical fact
-- rather than correcting it. If a snapshot is wrong, the honest fix is
-- another release.
DELETE FROM releases.releases
 WHERE module_key = 'agents' AND version = '1.6.0';
