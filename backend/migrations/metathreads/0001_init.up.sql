-- The Meta Threads integration owns its own Postgres schema, and its own
-- migration timeline (`schema_migrations_metathreads`).
--
-- ══════════════════════════════════════════════════════════════════════
--  READ THIS BEFORE TOUCHING ANYTHING NAMED "THREADS"
-- ══════════════════════════════════════════════════════════════════════
--
-- There are TWO unrelated things in this system with that word in them,
-- and confusing them is the single most likely mistake a future session
-- can make here:
--
--   schema `threads`       C.O.R.S.I. Threads. An INTERNAL bounded context.
--                          Units of content work — idea, draft, review,
--                          published, archived. Owned by us, stored by us,
--                          written by the Content agent through
--                          threads.thread.* . See migrations/threads.
--
--   schema `meta_threads`  THIS. The Meta Threads social network. An
--                          EXTERNAL system. We hold one OAuth credential
--                          per workspace and read the operator's own
--                          published posts and their metrics through
--                          meta_threads.* . We store no posts.
--
--   internal state  →  threads.threads          (ours, writable)
--   external evidence → the Meta Threads API    (theirs, read-only)
--
-- The two never join. A row here is a CREDENTIAL, never content. Nothing
-- in this schema references threads.threads and nothing ever should: a
-- post the operator published is evidence about the past, and a Thread is
-- work in progress. Copying one into the other would make the pipeline
-- claim authorship of things it never held.
--
-- ── Why not inside `chat` ──────────────────────────────────────────────
-- Because Agents must remain unable to name Meta Threads, exactly as it is
-- unable to name GitHub or Job Radar.
--
-- ── Why not a generic credential store ─────────────────────────────────
-- There is none, and this is the third credential in the system — still a
-- sample too small to abstract from. What IS shared is the mechanism:
-- `token_cipher` is sealed by internal/platform/secrets, the same
-- AES-256-GCM the LiteLLM key and the GitHub token use, with the same key
-- and the same rotation consequence. Only the location is local, because
-- the decision is local.
CREATE SCHEMA IF NOT EXISTS meta_threads;

-- ── connections ────────────────────────────────────────────────────────
--
-- One linked Meta Threads account per workspace.
CREATE TABLE meta_threads.connections (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- Sealed by platform/secrets. Never selected into a response: the
    -- repository scans it, the client spends it, and the wire sees only the
    -- hint. See domain.Connection, where the field is json:"-".
    token_cipher BYTEA NOT NULL,
    -- The display-safe remnant ("…a3f9"). Enough to tell two tokens apart,
    -- useless on its own. Same pattern as chat.providers and
    -- github.connections.
    token_hint   TEXT NOT NULL CHECK (length(token_hint) <= 40),

    -- ── Why the expiry is a column and not a guess ─────────────────────
    -- A Meta Threads long-lived token is valid for 60 days and can be
    -- refreshed once it is at least 24 hours old. Both halves of that rule
    -- need a real timestamp: without one, the product can only discover
    -- expiry by failing a call the operator was in the middle of, and can
    -- never say "this connection needs attention in three days".
    --
    -- It records what the TOKEN ENDPOINT returned (`expires_in`), stamped
    -- at the moment of exchange. It is not recomputed and not inferred.
    token_expires_at TIMESTAMPTZ NOT NULL,

    -- The scopes the grant actually came back with, as Meta reported them.
    --
    -- ── Why stored, and why it is not an authorization ─────────────────
    -- Stored because what a token may do is a property of the token, and a
    -- product that has to make a failing call to find out is a product that
    -- discovers its limits in front of the user. A tool that needs
    -- threads_manage_insights can say so before spending a round trip.
    --
    -- It is NOT the authorization model. An agent's permission to call a
    -- capability is a grant in chat.agent_tools, checked by Agents. This
    -- column answers a different question — whether the CREDENTIAL can do
    -- it at all — and both have to be true.
    scopes       TEXT[] NOT NULL DEFAULT '{}',

    -- Who the token turned out to be, read from GET /me at connect time.
    -- Stored rather than fetched on every page load, for the reason the
    -- GitHub connection gives: it is what a card shows, it changes rarely,
    -- and a surface that cannot render without a round trip to Meta is one
    -- that breaks when Meta is slow.
    --
    -- account_id is TEXT, not BIGINT: Meta returns it as a string and it is
    -- an opaque identifier, never arithmetic. Storing an opaque id as a
    -- number is how a leading zero disappears.
    account_id          TEXT NOT NULL CHECK (length(account_id) BETWEEN 1 AND 100),
    username            TEXT NOT NULL CHECK (length(username) BETWEEN 1 AND 100),
    display_name        TEXT NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),
    profile_picture_url TEXT NOT NULL DEFAULT '' CHECK (length(profile_picture_url) <= 1000),

    -- Overridable so a test can point the whole stack at a local server.
    -- Never rendered and never accepted from a tool argument.
    api_base_url TEXT NOT NULL DEFAULT 'https://graph.threads.net',

    last_verified_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Soft delete, matching every other module. Disconnecting is a decision
    -- worth keeping a record of; the token it carried is not, and the
    -- application layer overwrites the cipher on disconnect rather than
    -- leaving a sealed credential on a row nobody can reach.
    deleted_at TIMESTAMPTZ
);

-- One live connection per workspace. A second connect REPLACES the first,
-- which is the only reading this index permits and the only one a person
-- means when they link a different account.
CREATE UNIQUE INDEX connections_workspace_idx
    ON meta_threads.connections (workspace_id)
    WHERE deleted_at IS NULL;
