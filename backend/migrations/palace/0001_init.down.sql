-- Forward-only in a populated database; this exists for the integration
-- harness, which drops to a clean slate before each run.
--
-- Order matters, and it is the reverse of the dependency graph:
-- memory_sources references memories and sources; memories and sessions
-- reference artifacts and rooms; artifact_items references artifacts;
-- artifacts reference rooms. `relations` references nothing.
DROP TABLE IF EXISTS palace.memory_sources;
DROP TABLE IF EXISTS palace.relations;
DROP TABLE IF EXISTS palace.sessions;
DROP TABLE IF EXISTS palace.memories;
DROP TABLE IF EXISTS palace.sources;
DROP TABLE IF EXISTS palace.artifact_items;
DROP TABLE IF EXISTS palace.artifacts;
DROP TABLE IF EXISTS palace.rooms;
DROP SCHEMA IF EXISTS palace;
