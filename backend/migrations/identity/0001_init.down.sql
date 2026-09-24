-- Reverses 0001. Dropping this schema logs the operator out and loses
-- nothing else: the credential is environment, not data, and no other
-- schema references these rows.
--
-- Forward-only in a populated database, as everywhere else in this project.
-- This exists so the throwaway databases the integration suite provisions
-- can be rebuilt, and rollback in production is a restore.
DROP SCHEMA IF EXISTS identity CASCADE;
