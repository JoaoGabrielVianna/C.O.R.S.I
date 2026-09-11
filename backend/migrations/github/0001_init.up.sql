-- The GitHub integration owns its own Postgres schema, and its own
-- migration timeline (`schema_migrations_github`).
--
-- ── Why not inside `chat` ──────────────────────────────────────────────
-- Because Agents must remain unable to name GitHub. Putting a
-- github_connections table in the chat schema would make the Agents module
-- the owner of an external system's credential and of the operator's
-- decision about which repositories are readable — which is precisely the
-- `Module → Integration` coupling the architecture forbids as a real
-- dependency rather than a declared one.
--
-- ── Why not inside `platform` ──────────────────────────────────────────
-- Platform is generic by admission rule: it holds capabilities that carry
-- no vendor and no business meaning. AES-256-GCM is Platform's (and this
-- schema uses it, through the same Sealer the LiteLLM credential uses).
-- "Which GitHub account is linked, and which of its repositories may be
-- read" is not generic, and a `platform.github_*` table would be Platform
-- learning a vendor's name.
--
-- So the integration owns the two rows only it can own, and nothing else
-- in the database references them.
CREATE SCHEMA IF NOT EXISTS github;

-- ── connections ────────────────────────────────────────────────────────
--
-- One linked GitHub account per workspace.
--
-- ── Why the token lives here and not in a generic credential store ─────
-- There is no generic credential store, and inventing one for the second
-- credential in the system would be designing an abstraction from a sample
-- of two. What IS shared is the mechanism: `token_cipher` is sealed by
-- internal/platform/secrets, the same AES-256-GCM the LiteLLM key uses,
-- with the same key and the same rotation consequence. Only the storage
-- location is local, and it is local because the *decision* is local.
CREATE TABLE github.connections (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- How the token in this row was obtained.
    --
    -- 'pat' is the only value this build writes. The column exists from the
    -- first migration because the alternative — adding it on the day OAuth
    -- ships — would require deciding retroactively what every existing row
    -- was, and every existing row would be a guess. A column that records
    -- the answer while the answer is still known costs one TEXT.
    --
    -- Everything downstream of the token is auth-agnostic: the GitHub REST
    -- API takes `Authorization: Bearer <token>` whether the token is a
    -- fine-grained PAT, an OAuth user token, or a GitHub App installation
    -- token. So a future auth kind adds a row shape and an acquisition
    -- flow, and changes nothing about repositories, tools, or the API.
    auth_kind    TEXT NOT NULL CHECK (auth_kind IN ('pat')),

    -- Sealed by platform/secrets. Never selected into a response: the
    -- repository scans it, the client spends it, and the wire sees only the
    -- hint. See domain.Connection, where the field is json:"-".
    token_cipher BYTEA NOT NULL,
    -- The display-safe remnant ("...a3f9"). Enough to tell two tokens
    -- apart, useless on its own. Same pattern as chat.providers.
    token_hint   TEXT NOT NULL CHECK (length(token_hint) <= 40),

    -- Who the token turned out to be, read from GET /user at connect time.
    -- Stored rather than fetched on every page load: it is what the card
    -- shows, it changes rarely, and a page that cannot render without a
    -- round trip to GitHub is a page that breaks when GitHub is slow.
    account_login      TEXT NOT NULL CHECK (length(account_login) BETWEEN 1 AND 100),
    account_id         BIGINT NOT NULL,
    account_type       TEXT NOT NULL CHECK (account_type IN ('User', 'Organization')),
    account_name       TEXT NOT NULL DEFAULT '' CHECK (length(account_name) <= 200),
    account_avatar_url TEXT NOT NULL DEFAULT '' CHECK (length(account_avatar_url) <= 500),

    -- The API root this connection talks to. Stored so a GitHub Enterprise
    -- host is a row rather than a redeploy, and so a test can point one
    -- connection at a fake server without a global switch.
    api_base_url TEXT NOT NULL CHECK (length(api_base_url) BETWEEN 1 AND 500),

    -- The last time the token was proven to still work. Nullable because
    -- "never verified since it was stored" is a real state and must not be
    -- spelled as an old timestamp.
    last_verified_at TIMESTAMPTZ,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Soft delete, so disconnecting keeps the history of having connected
    -- without keeping the ability to use it. Every read filters it out.
    deleted_at   TIMESTAMPTZ
);

-- Exactly one live connection per workspace.
--
-- Not a product limitation dressed as a constraint: it is what makes "the
-- GitHub connection of this workspace" a phrase with one referent. A tool
-- that had to choose between two connections would need the model to name
-- one, and the model naming which credential to spend is the shape of
-- problem this whole design exists to avoid.
CREATE UNIQUE INDEX connections_one_per_workspace_idx
    ON github.connections (workspace_id)
    WHERE deleted_at IS NULL;

-- ── repositories ───────────────────────────────────────────────────────
--
-- The repositories the operator authorized C.O.R.S.I. to read.
--
-- ── Presence is the authorization ──────────────────────────────────────
-- Same rule as chat.agent_tools, and for the same reason: there is no
-- `enabled` column, because a disabled authorization and an absent one are
-- the same fact, and a schema that can express both is a schema where they
-- will eventually disagree. Revoking deletes the row.
--
-- ── Why this table exists at all ───────────────────────────────────────
-- Because connecting an account must not mean "every agent may read
-- everything this account can see". The token's reach and the operator's
-- decision are two different sets, and only the second one may reach a
-- tool. This table is the second one.
CREATE TABLE github.repositories (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL,
    connection_id UUID NOT NULL REFERENCES github.connections (id) ON DELETE CASCADE,

    -- GitHub's own immutable id for the repository. It survives a rename,
    -- which `full_name` does not, so it is what makes "the same repository"
    -- a decidable question after somebody renames an org.
    github_id     BIGINT NOT NULL,

    -- The parts, stored separately, because these are what get pasted into
    -- a URL path. The tool resolves a model-supplied string to a ROW and
    -- then builds every request from these columns — never from the string.
    -- That is the whole traversal defence: a value that is not exactly a
    -- stored owner and a stored name never reaches the network.
    owner         TEXT NOT NULL CHECK (owner ~ '^[A-Za-z0-9][A-Za-z0-9-]{0,38}$'),
    name          TEXT NOT NULL CHECK (name ~ '^[A-Za-z0-9_.-]{1,100}$'),
    -- The denormalised "owner/name", lowercased into an index below. It is
    -- the lookup key a model can plausibly produce, and it is only ever a
    -- key: nothing is built from it.
    full_name     TEXT NOT NULL CHECK (length(full_name) BETWEEN 3 AND 140),

    private        BOOLEAN NOT NULL DEFAULT FALSE,
    default_branch TEXT NOT NULL DEFAULT '' CHECK (length(default_branch) <= 255),
    html_url       TEXT NOT NULL DEFAULT '' CHECK (length(html_url) <= 500),
    description    TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    owner_type     TEXT NOT NULL DEFAULT 'User' CHECK (owner_type IN ('User', 'Organization')),

    authorized_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Authorizing the same repository twice is authorizing it once.
    UNIQUE (connection_id, github_id)
);

-- The lookup every tool call performs: workspace first, then the name the
-- caller supplied, case-folded because GitHub treats owner and repo names
-- case-insensitively and a model will not reproduce capitalisation exactly.
--
-- UNIQUE rather than a plain index: two rows differing only in case would
-- be two answers to one question, and the tool would silently take
-- whichever the planner returned first.
CREATE UNIQUE INDEX repositories_full_name_idx
    ON github.repositories (workspace_id, lower(full_name));

-- Backs the connection's own listing and the ON DELETE CASCADE above.
CREATE INDEX repositories_connection_idx
    ON github.repositories (workspace_id, connection_id);
