-- Each bounded context owns its own Postgres schema. `chat` holds the LLM
-- provider credentials, the agent registry, and the conversation log.
-- Keeping it isolated here makes the eventual extraction into a standalone
-- service a `pg_dump --schema=chat` rather than a data-archaeology project.
CREATE SCHEMA IF NOT EXISTS chat;
