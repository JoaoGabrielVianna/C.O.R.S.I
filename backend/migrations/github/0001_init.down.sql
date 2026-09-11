-- Reverses 0001. As everywhere in this repository, `down` exists for the
-- test harness and for a local reset: on a populated database the direction
-- is forward only, and a real rollback is a restore from a backup.
--
-- Dropping the schema takes the two tables with it. Running this against a
-- database that has a live connection row destroys the sealed token, which
-- cannot be recovered — reconnecting means pasting a new one.
DROP TABLE IF EXISTS github.repositories;
DROP TABLE IF EXISTS github.connections;
DROP SCHEMA IF EXISTS github;
