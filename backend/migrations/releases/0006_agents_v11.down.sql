-- Removing the v1.1.0 row.
--
-- Guarded on the version so this cannot take v1.0.0 with it, and written at
-- all only because every migration in this timeline has a down. Reverting a
-- published release is not a normal operation: the row is frozen by trigger
-- against UPDATE, and deleting it erases a historical fact rather than
-- correcting one. If a snapshot is wrong, the honest fix is another release.
DELETE FROM releases.releases
 WHERE module_key = 'agents' AND version = '1.1.0';
