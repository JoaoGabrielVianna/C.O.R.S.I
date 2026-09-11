-- The freeze trigger refuses to modify or delete a published release, and
-- that is the point of it. Running this against a published finance 1.1.0
-- raises restrict_violation, which is the correct outcome.
--
-- The module row is not touched: finance was already `active` before this
-- release and stays that way.
DELETE FROM releases.releases WHERE module_key = 'finance' AND version = '1.1.0';
