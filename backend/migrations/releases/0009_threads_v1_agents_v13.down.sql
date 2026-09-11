-- Removing the two rows, and the module identity that had to exist first.
--
-- Guarded on module and version so nothing else in either timeline goes
-- with them. Written because every migration in this timeline has a down,
-- not because reverting a published release is a normal operation: both
-- rows are frozen by trigger against UPDATE, and deleting one erases a
-- historical fact rather than correcting it. If a snapshot is wrong, the
-- honest fix is another release.
--
-- Order matters: releases.module_key is ON DELETE RESTRICT, so the
-- release goes before the module that owns it.
DELETE FROM releases.releases
 WHERE module_key = 'threads' AND version = '1.0.0';

DELETE FROM releases.releases
 WHERE module_key = 'agents' AND version = '1.3.0';

-- Only if nothing else was ever recorded against it. A module row with a
-- release still attached is one this migration did not fully create, and
-- dropping it would take somebody else's history with it.
DELETE FROM releases.modules
 WHERE key = 'threads'
   AND NOT EXISTS (SELECT 1 FROM releases.releases WHERE module_key = 'threads');
