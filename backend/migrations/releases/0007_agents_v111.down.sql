-- Removing the v1.1.1 row.
--
-- Guarded on the version so it cannot take v1.1.0 or v1.0.0 with it. Written
-- because every migration in this timeline has a down, not because reverting
-- a published release is a normal operation: the row is frozen by trigger
-- against UPDATE, and deleting it erases a historical fact rather than
-- correcting one. If a snapshot is wrong, the honest fix is another release.
DELETE FROM releases.releases
 WHERE module_key = 'agents' AND version = '1.1.1';
