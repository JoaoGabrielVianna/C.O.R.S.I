-- Tools: which capabilities an agent may use, and what actually ran.
--
-- Two tables, and they answer two different questions. `agent_tools` is
-- configuration — a decision the user made, which the code cannot know.
-- `tool_calls` is history — a record of something that happened, which no
-- configuration can change afterwards.
--
-- ── What is deliberately NOT here ──────────────────────────────────────
-- There is no table of tool definitions. A tool's name, description and
-- input schema are properties of the program that implements it: they ship
-- with the binary, they change with a deploy, and a row describing them
-- would be a second copy that drifts the first time a schema is edited —
-- and, worse, could describe an executor that no longer exists. The
-- registry is code (internal/chat/adapters/tools), and this schema stores
-- only what code cannot: the grant, and the log.
--
-- ── The default is DENY, and it is the absence of a row ────────────────
-- No backfill, and none is possible: no agent has ever had a tool, so
-- inventing grants for existing agents would be fabricating history. An
-- agent created before this migration and an agent created after it are in
-- the same state — no rows, no tools, a request body byte-identical to the
-- one it sent yesterday.

-- ── agent_tools ────────────────────────────────────────────────────────
--
-- One row is one grant. There is no `enabled` column: a disabled grant and
-- an absent grant are the same fact, and a schema that can express both is
-- a schema where they will eventually disagree. Revoking deletes.
CREATE TABLE chat.agent_tools (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id     UUID NOT NULL REFERENCES chat.agents (id) ON DELETE RESTRICT,

    -- The canonical tool name, e.g. 'system.echo'.
    --
    -- No foreign key, and its absence is the design rather than an
    -- oversight. The thing this column references lives in Go. A FK would
    -- require a table of tools maintained by hand alongside the binary,
    -- which is a second source of truth wearing the costume of integrity —
    -- and the first deploy that removed a tool would either fail to start
    -- or silently keep a grant pointing at nothing.
    --
    -- What the database enforces is what it can actually verify: the shape
    -- of the name, and that a grant is not duplicated. Whether the name
    -- still resolves is checked against the registry on every read, which
    -- is the only place that can answer it truthfully.
    tool_name    TEXT NOT NULL
                 CHECK (tool_name ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,3}$'
                        AND tool_name !~ '__'
                        AND length(tool_name) <= 64),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Granting twice is granting once. The application relies on this to
    -- make Authorize idempotent instead of read-then-write, which would be
    -- a race with itself.
    UNIQUE (agent_id, tool_name)
);

-- The read every turn of an authorized agent runs, and the only index it
-- needs: one agent's grants, scoped to the workspace. workspace_id leads
-- because every query in this module is workspace-first, and an id arriving
-- from a URL must never select rows it does not own.
CREATE INDEX agent_tools_agent_idx
    ON chat.agent_tools (workspace_id, agent_id);

-- ── tool_calls ─────────────────────────────────────────────────────────
--
-- The audit trail. Write-once: there is no UPDATE path in the repository,
-- because a record of what happened is not a document to be edited.
--
-- ── Why it hangs off the message and not off the conversation ──────────
-- Because a tool call belongs to a *turn*, and the turn is the message.
-- That gives it the lifecycle it should have for free: truncate and
-- regenerate hard-delete the assistant message, and the calls go with it
-- rather than outliving the answer they produced. It is the same choice
-- migration 0010 made for the context report, for the same reason.
--
-- conversation_id is stored beside it anyway, denormalised on purpose: the
-- transcript reads a whole conversation's calls at once, and reaching them
-- through messages would be a join on every open of a thread.
CREATE TABLE chat.tool_calls (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations (id) ON DELETE CASCADE,
    message_id      UUID NOT NULL REFERENCES chat.messages (id) ON DELETE CASCADE,

    -- Which provider round asked for this call, 1-based. Two calls in the
    -- same round are two rows sharing a round number; the model is allowed
    -- to ask for several at once.
    round           INTEGER NOT NULL CHECK (round >= 1),

    -- The gateway's own tool_call id. Recorded because it is the only value
    -- that correlates this row with the provider's logs, and because
    -- answering a call with a different id is the specific bug that makes a
    -- turn respond to the wrong question.
    provider_call_id TEXT NOT NULL CHECK (length(provider_call_id) <= 128),

    tool_name       TEXT NOT NULL CHECK (length(tool_name) <= 64),

    -- The raw arguments as the model produced them, and the serialised
    -- result. NULL is not "empty": it is "not retained", which is what a
    -- future redaction will write here. `redacted` says which of the two a
    -- NULL means, so a reader never has to guess whether a payload was
    -- absent or removed.
    --
    -- Nothing redacts anything today. The affordance is designed in now
    -- precisely because retrofitting it onto rows that already exist is the
    -- version of this that goes wrong: the rows would need a default, and
    -- the default would be a claim about data nobody checked.
    arguments       TEXT CHECK (arguments IS NULL OR length(arguments) <= 16384),
    result          TEXT CHECK (result IS NULL OR length(result) <= 32768),
    redacted        BOOLEAN NOT NULL DEFAULT FALSE,

    status          TEXT NOT NULL CHECK (status IN ('ok', 'error')),
    -- Empty on success. One of the codes in domain.ToolErrorCode otherwise.
    error_code      TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 40),
    error_message   TEXT NOT NULL DEFAULT '' CHECK (length(error_message) <= 500),
    duration_ms     INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One answer per call, per round.
    --
    -- The round is part of the key, and it has to be. What the protocol
    -- guarantees is that a tool_call id is unique inside ONE response; it
    -- promises nothing across the several responses a turn now makes, and a
    -- gateway that reuses `call_1` in round two is unusual rather than
    -- impossible. Keying without the round would make that collision reject
    -- the whole insert, and the turn would lose its entire audit trail
    -- because of a duplicate id it did not choose.
    UNIQUE (message_id, round, provider_call_id)
);

-- The transcript's read: one conversation's calls in turn order. `id` breaks
-- the tie for two calls made in the same round of the same turn, so the
-- order shown is stable between two identical reads.
CREATE INDEX tool_calls_conversation_idx
    ON chat.tool_calls (workspace_id, conversation_id, created_at, id);

-- Backs the ON DELETE CASCADE from messages, which truncate and regenerate
-- exercise on every edit of a thread.
CREATE INDEX tool_calls_message_idx
    ON chat.tool_calls (message_id);
