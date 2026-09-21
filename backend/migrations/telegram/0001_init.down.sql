-- Reverses 0001. Everything this integration owns is routing state, so
-- dropping the schema loses no conversation: the transcripts are in
-- `chat.messages` and are not touched here.
--
-- Forward-only in a populated database, as everywhere else in this
-- project. This exists so the throwaway databases the integration suite
-- provisions can be rebuilt, and rollback in production is a restore.
DROP SCHEMA IF EXISTS telegram CASCADE;
