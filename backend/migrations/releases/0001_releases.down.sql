-- Reverting drops the recorded history along with the tables. That is the
-- honest behavior for a down migration, and it is also the reason this one
-- must never run against a populated database: a release history has no
-- other copy. Rollback in production is a restore from backup.
DROP TRIGGER IF EXISTS releases_freeze_published ON releases.releases;
DROP FUNCTION IF EXISTS releases.freeze_published();
DROP TABLE IF EXISTS releases.releases;
DROP TABLE IF EXISTS releases.modules;
DROP SCHEMA IF EXISTS releases;
