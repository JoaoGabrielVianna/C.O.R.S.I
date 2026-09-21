-- The Telegram integration owns its own Postgres schema and its own
-- migration timeline (`schema_migrations_telegram`).
--
-- ══════════════════════════════════════════════════════════════════════
--
--   TELEGRAM IS AN INTERFACE TO C.O.R.S.I., NOT A SECOND RUNTIME
--
-- ══════════════════════════════════════════════════════════════════════
--
-- Everything in this schema is ROUTING STATE: which Telegram identity may
-- speak to which workspace, which agent it is speaking to right now, and
-- which C.O.R.S.I. conversation carries that pairing. Not one row here is
-- conversation content. The transcript lives in `chat.messages`, written
-- by the same application service the web client drives, and this schema
-- would be droppable tomorrow without losing a single thing the operator
-- said.
--
-- ── Why not inside `chat` ──────────────────────────────────────────────
-- Because Agents must remain unable to name Telegram. A
-- `chat.telegram_bindings` table would make the Agents module the owner of
-- an external vendor's numeric identity space and of the decision about
-- who may reach a workspace through it — the `Module → Integration`
-- coupling the architecture forbids as a real dependency.
--
-- ── Why there is no foreign key to chat.agents or chat.conversations ───
-- Same reason `github.repositories` has none. A cross-schema FK is a real
-- dependency written in DDL: it would make the Telegram timeline
-- un-appliable without the chat timeline, and would let a `DELETE` in one
-- bounded context cascade into another's. The ids here are workspace-
-- scoped references resolved through the runtime port at use time, and an
-- agent that has since been deleted is a refusal, not a dangling row.
CREATE SCHEMA IF NOT EXISTS telegram;

-- ── bindings ───────────────────────────────────────────────────────────
--
-- One Telegram chat, bound to one C.O.R.S.I. workspace.
--
-- ── Why the identity is numeric and nothing else ───────────────────────
-- `telegram_user_id` and `telegram_chat_id` are Telegram's own immutable
-- integers. A @username is NOT stored and must never become one: it is
-- user-editable, it is reassignable to a different person after release,
-- and it is exactly the field a spoofing attempt would target. Nothing in
-- this integration authorizes on a name.
CREATE TABLE telegram.bindings (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The workspace this Telegram chat speaks to. It is written ONCE, by
    -- an operator confirming a pairing code, and never by anything
    -- arriving from Telegram. That is the whole isolation argument: no
    -- request path can propose a workspace.
    workspace_id UUID NOT NULL,

    -- Telegram's numeric identities. Both are checked on every update:
    -- the chat says which binding, the user says who is typing, and a
    -- mismatch is refused rather than resolved.
    telegram_user_id BIGINT NOT NULL,
    telegram_chat_id BIGINT NOT NULL,

    -- The agent this chat is currently talking to.
    --
    -- NULL is a real, reachable state and is not a defect: a freshly
    -- paired chat has selected nothing, and the bot asks before it will
    -- carry a message. Defaulting to "whatever agent sorts first" would
    -- be the product guessing which of the operator's agents should
    -- receive a message they have not yet been told is going anywhere.
    active_agent_id UUID,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Unbinding is a revocation, not a delete: "this Telegram account was
    -- once allowed in" is a fact worth keeping, and the conversation
    -- mappings below hang off this row.
    revoked_at TIMESTAMPTZ
);

-- Exactly one live binding per Telegram chat.
--
-- Partial on `revoked_at IS NULL` so a revoked binding does not block a
-- later re-pairing, and UNIQUE so "the workspace this chat speaks to" is a
-- phrase with one referent. Two live bindings would mean an incoming
-- message could be answered from either of two workspaces, chosen by the
-- planner.
CREATE UNIQUE INDEX bindings_one_per_chat_idx
    ON telegram.bindings (telegram_chat_id)
    WHERE revoked_at IS NULL;

-- The same guarantee from the other side. A Telegram USER with two live
-- bindings would be two workspaces reachable by one person's phone, and
-- the MVP has no way for them to say which they meant.
CREATE UNIQUE INDEX bindings_one_per_user_idx
    ON telegram.bindings (telegram_user_id)
    WHERE revoked_at IS NULL;

-- ── chat_agents ────────────────────────────────────────────────────────
--
-- The conversation mapping:
--
--      (telegram binding, C.O.R.S.I. agent)  →  C.O.R.S.I. conversation
--
-- ── Why the agent is part of the key ───────────────────────────────────
-- Because a conversation belongs to an agent for its whole life: its
-- history is replayed into that agent's context window, and its memories
-- and grants are that agent's. Reusing one conversation and swapping the
-- agent on it would feed Scout's transcript to Palace — context crossover
-- with no way to undo it after the fact, since the rows are already
-- written.
--
-- So switching agents does not move a conversation. It selects a
-- different one, and switching back returns to the first, still holding
-- everything that was said in it.
CREATE TABLE telegram.chat_agents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL,
    binding_id      UUID NOT NULL REFERENCES telegram.bindings (id) ON DELETE CASCADE,

    -- A `chat.agents` id, resolved through the runtime port rather than by
    -- a foreign key. See the header.
    agent_id        UUID NOT NULL,
    -- A `chat.conversations` id, created through the same port. This
    -- column is the ONLY thing that makes a Telegram chat continuous:
    -- without it every message would open a new thread and the agent would
    -- meet the operator for the first time on every turn.
    conversation_id UUID NOT NULL,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One conversation per (binding, agent). Selecting an agent twice
    -- selects the same thread.
    UNIQUE (binding_id, agent_id)
);

-- Backs the lookup every inbound message performs.
CREATE INDEX chat_agents_binding_idx
    ON telegram.chat_agents (workspace_id, binding_id);

-- ── pairing_codes ──────────────────────────────────────────────────────
--
-- The short-lived, single-use secret that turns a Telegram identity into a
-- binding.
--
-- ── Why a HASH and not the code ────────────────────────────────────────
-- Because a database dump, a backup file or a `SELECT *` over somebody's
-- shoulder must not hand over a live pairing code. The code exists in
-- exactly two places for exactly ten minutes: the operator's Telegram
-- screen, and the operator's terminal. Storage holds only enough to
-- recognise it.
--
-- SHA-256 rather than a password hash is the right primitive here and the
-- reason is the input, not the convenience: this is a high-entropy random
-- value from crypto/rand, not a memorable secret, so there is nothing for
-- a slow KDF to protect against. Brute force is bounded by the entropy and
-- by the expiry, not by the hash's cost.
CREATE TABLE telegram.pairing_codes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- SHA-256 of the code, always 32 bytes. The code itself is never
    -- written anywhere.
    code_hash   BYTEA NOT NULL CHECK (octet_length(code_hash) = 32),

    -- WHO this code will bind, fixed when the code is issued. A code is
    -- not a general-purpose admission ticket: redeeming it binds the
    -- Telegram identity that asked for it and no other, so a code
    -- intercepted in transit cannot be used to pair a different account.
    telegram_user_id BIGINT NOT NULL,
    telegram_chat_id BIGINT NOT NULL,

    -- Expiry is a column rather than a convention so it is enforced in the
    -- query that redeems, where it cannot be forgotten.
    expires_at  TIMESTAMPTZ NOT NULL,
    -- Single use. Set at redemption in the same statement that reads the
    -- row, so two concurrent redemptions cannot both win.
    consumed_at TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The redemption lookup, and the guarantee that one code means one row.
CREATE UNIQUE INDEX pairing_codes_hash_idx
    ON telegram.pairing_codes (code_hash);

-- Backs "expire every outstanding code for this user", which `/start`
-- performs before issuing a new one: an operator who typed /start three
-- times should have one live code, not three.
CREATE INDEX pairing_codes_user_idx
    ON telegram.pairing_codes (telegram_user_id)
    WHERE consumed_at IS NULL;

-- ── update_cursor ──────────────────────────────────────────────────────
--
-- The last Telegram update this deployment has taken responsibility for.
--
-- ── Why it is a single row and NOT workspace-scoped ────────────────────
-- Because it is a property of the BOT, which is deployment configuration,
-- not of a workspace. One token, one update stream, one cursor. A
-- workspace column here would invite a second poller.
--
-- ── At-most-once, stated plainly ───────────────────────────────────────
-- The cursor advances BEFORE a batch is processed, not after. So a crash
-- mid-turn loses that message rather than replaying it on restart.
--
-- That is the deliberate direction. A replayed message is a second paid
-- provider call and, worse, a second execution of whatever writes the
-- first turn had already performed — the exact "second Room, second
-- Artifact" failure Safe Resume exists to prevent, arriving by a different
-- door. A lost message is one the operator can see was never answered and
-- can retype.
CREATE TABLE telegram.update_cursor (
    -- A one-row table, enforced by the type rather than by discipline.
    id             BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    last_update_id BIGINT  NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
