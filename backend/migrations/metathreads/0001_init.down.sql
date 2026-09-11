-- Forward-only in a populated database; this exists for the integration
-- harness, which drops to a clean slate before each run.
DROP TABLE IF EXISTS meta_threads.connections;
DROP SCHEMA IF EXISTS meta_threads;
