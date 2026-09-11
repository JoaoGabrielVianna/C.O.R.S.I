-- Forward-only in a populated database; this exists for the integration
-- harness, which drops to a clean slate before each run.
--
-- Order matters: stage_events references opportunities, opportunities
-- references companies.
DROP TABLE IF EXISTS jobradar.stage_events;
DROP TABLE IF EXISTS jobradar.opportunities;
DROP TABLE IF EXISTS jobradar.companies;
DROP SCHEMA IF EXISTS jobradar;
