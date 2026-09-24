-- The Identity platform capability owns its own Postgres schema and its
-- own migration timeline (`schema_migrations_identity`).
--
-- ══════════════════════════════════════════════════════════════════════
--
--   THIS SCHEMA HOLDS SESSIONS. IT DOES NOT HOLD A CREDENTIAL.
--
-- ══════════════════════════════════════════════════════════════════════
--
-- There is no users table and no password column, because there is no
-- password to store. The one authorized subject is named by AUTH_EMAIL and
-- verified against AUTH_PASSWORD_HASH, an argon2id verifier supplied to the
-- process as environment. A dump of this database therefore contains
-- nothing that authenticates anybody: sessions expire, and the rows below
-- carry a DIGEST rather than the bearer token itself.
--
-- ── Why token_sha256 and not the token ────────────────────────────────
-- Because the token is a bearer credential, and a bearer credential stored
-- verbatim is one `pg_dump`, one slow-query log or one screenshot away from
-- being usable. The cookie carries 256 bits of uniform entropy, so a plain
-- SHA-256 is the right function here and a password hash would be the wrong
-- one: there is no dictionary to precompute against a random 32-byte value,
-- and a work factor on every request would only slow the operator down.
--
-- ── Why no ip or user_agent column ────────────────────────────────────
-- Because nothing reads them. They would be personal data collected for a
-- feature that does not exist, in a product whose whole premise is that the
-- operator's record stays on the operator's machine.
CREATE SCHEMA IF NOT EXISTS identity;

CREATE TABLE identity.sessions (
    token_sha256 bytea       PRIMARY KEY,
    -- The subject this session was issued for. Denormalised on purpose:
    -- there is no users table to join to, and recording who a session was
    -- for means a change of AUTH_EMAIL does not silently convert every
    -- live session into the new operator's.
    subject      text        NOT NULL,
    issued_at    timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,

    CONSTRAINT sessions_expire_after_issue CHECK (expires_at > issued_at)
);

-- Supports the expiry sweep. Lookup hits the primary key, so it needs
-- nothing.
CREATE INDEX sessions_expires_at_idx ON identity.sessions (expires_at);
