-- The freeze trigger refuses to modify or delete a published release, and
-- that is the point of it. Running this against a published agents 1.5.0
-- raises restrict_violation, which is the correct and intended outcome.
--
-- The module row is untouched: agents was already `active`.
DELETE FROM releases.releases WHERE module_key = 'agents' AND version = '1.5.0';
