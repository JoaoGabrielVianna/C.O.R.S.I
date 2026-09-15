-- Palace owns its own Postgres schema and its own migration timeline
-- (`schema_migrations_palace`).
--
-- ══════════════════════════════════════════════════════════════════════
--  READ THIS BEFORE TOUCHING ANYTHING NAMED "MEMORY" OR "SOURCE"
-- ══════════════════════════════════════════════════════════════════════
--
-- There are TWO unrelated things in this system called Memory, and two
-- called Source. Confusing them is the single most likely mistake a
-- future session can make here, and it is the same shape of mistake that
-- `threads` versus `meta_threads` already has a warning for.
--
--   chat.memories        The AGENT's memory. One short fact an agent was
--                        told to keep between conversations, scoped to
--                        THAT agent, injected into the prompt under a
--                        token budget. It is about how the agent should
--                        behave. See internal/chat/domain/memory.go.
--
--   palace.memories      THIS. The OPERATOR's knowledge. A durable fact,
--                        decision, preference or reflection about their
--                        own life and work, scoped to the WORKSPACE, read
--                        on demand through palace.memory.* and never
--                        injected by budget.
--
--   chat.agent_sources   Reference material an agent may consult: long,
--                        titled, written deliberately, spent against the
--                        context budget.
--
--   palace.sources       THIS. EVIDENCE. The raw text, transcript or
--                        event a palace memory was derived FROM. It
--                        exists to answer "why do I believe this", not to
--                        be consulted as material.
--
-- Nothing converts one into the other. There is no mechanism that
-- promotes a chat memory into a palace memory or the reverse, and adding
-- one would collapse a distinction the whole design rests on: one is
-- configuration of an assistant, the other is the operator's own record.
--
-- ── Why not inside `chat` ──────────────────────────────────────────────
-- Because Agents must remain unable to name Palace, exactly as it is
-- unable to name Finance, Job Radar, Threads or GitHub. A knowledge table
-- in the chat schema would make the Agents module the owner of the
-- operator's memory, which is the `Module → Module` coupling the
-- architecture forbids. Chat is a way to REACH Palace. It is not where
-- Palace lives.
--
-- ── What is deliberately absent ────────────────────────────────────────
-- No vector column, no embedding, no tsvector, no full-text index, no
-- reminder table, no scheduler state, no person table, and no JSONB. Each
-- of those is a decision this cut did not make. In particular the absence
-- of JSONB is a choice with a scar behind it: the one place in this
-- system that stores a document per row (`releases.snapshots`) carries
-- 125 items across 6 immutable rows that render blank because the shape
-- drifted. Structured artifact state lives in a table with columns.
CREATE SCHEMA IF NOT EXISTS palace;

-- ══════════════════════════════════════════════════════════════════════
--  Vocabularies
-- ══════════════════════════════════════════════════════════════════════
--
-- Every CHECK below spells a vocabulary that internal/palace/domain also
-- spells. That is not two sources of truth: the Go package is the only
-- copy that VALIDATES, and these constraints are backstops that refuse a
-- row the domain would never build. Every write path parses in the domain
-- and nowhere else.
--
-- ── sensitivity, on every entity that carries free text ────────────────
--
--   normal            ordinary content of this workspace
--   private           the operator's own, not to be volunteered
--   highly_sensitive  withheld from broad listings; reachable only by id,
--                     or by a listing that opted in explicitly
--
-- Rooms carry it too, and that is deliberate rather than uniform-for-the-
-- sake-of-it: a room's NAME can reveal the thing it contains before a
-- single artifact inside it is read.

-- ── rooms ──────────────────────────────────────────────────────────────
--
-- A thematic area. Rooms are containers and nothing else: they hold no
-- state that evolves, which is what Artifacts are for.
--
-- ── Why there is no parent_room_id ─────────────────────────────────────
-- Because nothing asks a question that needs one. A tree is a real
-- feature with real questions attached (what does archiving a parent do
-- to a child, does a listing recurse, is a cycle refused) and none of
-- them have been asked. Adding it later is one nullable column.
CREATE TABLE palace.rooms (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    -- Optional. Empty string rather than NULL, which is what every other
    -- optional text column in this system does (threads.content,
    -- jobradar.salary): two ways to spell "nothing" is one more than any
    -- reader needs.
    description  TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000),

    -- Lifecycle. Archiving is the ORGANIZING act; deletion is not.
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'archived')),
    sensitivity  TEXT NOT NULL DEFAULT 'normal'
                 CHECK (sensitivity IN ('normal', 'private', 'highly_sensitive')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Present, and nothing in v1 writes it. The read predicate of every
    -- repository in this codebase is `deleted_at IS NULL`, so the column
    -- keeps Palace readable by the same rule as everything else; and
    -- retrofitting soft delete onto rows that already exist is harder
    -- than including the column now. Same reasoning as
    -- ToolCallRecord.Redacted, which was designed in before anything
    -- redacted anything.
    deleted_at   TIMESTAMPTZ,

    -- The target of every composite foreign key below. A plain
    -- `REFERENCES palace.rooms (id)` would let an artifact in workspace A
    -- point at a room in workspace B: the FK checks that the row exists,
    -- never that it is YOURS. Making the workspace part of the key moves
    -- isolation from a rule the application must remember into one the
    -- database cannot be talked out of.
    CONSTRAINT rooms_workspace_id_key UNIQUE (workspace_id, id)
);

CREATE INDEX rooms_workspace_idx
    ON palace.rooms (workspace_id, updated_at DESC)
    WHERE deleted_at IS NULL;

-- ── artifacts ──────────────────────────────────────────────────────────
--
-- A living object that evolves: a project, a list, a plan, a note.
--
-- ── Why `kind` is immutable and the domain says so ─────────────────────
-- Because the kind decides what the object IS, and the structured state
-- hanging off it (artifact_items) was built under that answer. A list
-- turned into a note would keep items that nothing renders; a note turned
-- into a list would be a list nobody wrote items for. Re-filing means
-- creating the right thing and archiving the wrong one, which leaves both
-- facts on the record.
CREATE TABLE palace.artifacts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- Optional home. NULL is an artifact that has not been filed, which
    -- is a real and common state: the thing exists before anybody decides
    -- where it belongs.
    --
    -- MATCH SIMPLE (the default) is what makes the nullable side work: a
    -- NULL room_id skips the constraint entirely, and a non-NULL one must
    -- match BOTH columns.
    room_id      UUID,

    kind         TEXT NOT NULL
                 CHECK (kind IN ('project', 'list', 'plan', 'note')),
    title        TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    -- The prose side of the artifact. Structured state lives in
    -- artifact_items; this is what the thing SAYS.
    body         TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 20000),

    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'archived')),
    sensitivity  TEXT NOT NULL DEFAULT 'normal'
                 CHECK (sensitivity IN ('normal', 'private', 'highly_sensitive')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,

    CONSTRAINT artifacts_workspace_id_key UNIQUE (workspace_id, id),
    -- RESTRICT rather than CASCADE: a room is never hard-deleted in this
    -- design, and if one ever is, taking the operator's projects with it
    -- silently is the worst available outcome.
    CONSTRAINT artifacts_room_fk FOREIGN KEY (workspace_id, room_id)
        REFERENCES palace.rooms (workspace_id, id) ON DELETE RESTRICT
);

CREATE INDEX artifacts_workspace_idx
    ON palace.artifacts (workspace_id, updated_at DESC)
    WHERE deleted_at IS NULL;

-- Backs "everything in this room", which is the question a room exists to
-- answer.
CREATE INDEX artifacts_room_idx
    ON palace.artifacts (workspace_id, room_id, updated_at DESC)
    WHERE deleted_at IS NULL AND room_id IS NOT NULL;

-- ── artifact_items ─────────────────────────────────────────────────────
--
-- The structured state of an artifact: the entries of a list, the steps
-- of a plan, the tasks of a project.
--
-- ── Why a table and not a JSONB column ─────────────────────────────────
-- Three reasons, and the third is the expensive one. A table can be
-- indexed and counted without decoding a document. A table cannot be
-- silently REPLACED by a caller that round-trips the whole object, which
-- matters the moment a model is one of the writers. And a table cannot
-- drift in shape: `releases.snapshots` stores documents, its shape
-- changed, and 125 items across 6 rows now render blank in rows that are
-- immutable by trigger. That is the cost of a payload with no columns,
-- paid once already.
--
-- ── Why `position` is not unique ───────────────────────────────────────
-- Because reordering would then have to be a deferred, multi-statement
-- dance to avoid colliding with itself halfway through, and a checklist
-- is not worth that. Ties break on `id`, so two identical reads return an
-- identical order.
CREATE TABLE palace.artifact_items (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    artifact_id  UUID NOT NULL,

    position     INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    text         TEXT NOT NULL CHECK (length(text) BETWEEN 1 AND 2000),
    done         BOOLEAN NOT NULL DEFAULT FALSE,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Unlike the columns above, this one IS written in v1: removing an
    -- entry from a checklist is ordinary use, and `palace.artifact.item.
    -- remove` sets it. Soft rather than hard because an item may be the
    -- endpoint of nothing today and the subject of a memory tomorrow.
    deleted_at   TIMESTAMPTZ,

    -- CASCADE here and RESTRICT above, and the difference is the point:
    -- an item has no meaning without its artifact, whereas an artifact
    -- has plenty without its room.
    CONSTRAINT artifact_items_artifact_fk FOREIGN KEY (workspace_id, artifact_id)
        REFERENCES palace.artifacts (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX artifact_items_artifact_idx
    ON palace.artifact_items (workspace_id, artifact_id, position, id)
    WHERE deleted_at IS NULL;

-- ── sources ────────────────────────────────────────────────────────────
--
-- EVIDENCE. The text, transcript or event a memory was derived from.
--
-- ── Why a source is never edited ───────────────────────────────────────
-- There is no update capability and there must not be one. A source
-- answers "what was actually said or seen", and evidence that can be
-- rewritten after the fact answers nothing. A corrected transcript is a
-- NEW source; the memory can be linked to both, and the disagreement
-- between them is itself information.
CREATE TABLE palace.sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    kind         TEXT NOT NULL
                 CHECK (kind IN ('text', 'voice_transcript', 'system_event', 'external')),
    content      TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 20000),
    -- Where it came from, when that is outside this product: a URL, a
    -- message id, an event name. Required by the domain for kind
    -- 'external': evidence about a system we do not own that cannot say
    -- which system is not provenance.
    external_ref TEXT NOT NULL DEFAULT '' CHECK (length(external_ref) <= 1000),

    -- When the evidence was produced, which is not when the row was
    -- written. A transcript of yesterday's voice note is captured today.
    captured_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    sensitivity  TEXT NOT NULL DEFAULT 'normal'
                 CHECK (sensitivity IN ('normal', 'private', 'highly_sensitive')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,

    CONSTRAINT sources_workspace_id_key UNIQUE (workspace_id, id)
);

CREATE INDEX sources_workspace_idx
    ON palace.sources (workspace_id, captured_at DESC)
    WHERE deleted_at IS NULL;

-- ── memories ───────────────────────────────────────────────────────────
--
-- The operator's durable knowledge. NOT chat.memories. See the header.
CREATE TABLE palace.memories (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    kind         TEXT NOT NULL
                 CHECK (kind IN ('fact', 'preference', 'idea', 'decision', 'learning', 'reflection')),
    content      TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 20000),
    -- A one-line restatement, for listings. Optional, and empty means the
    -- listing shows an excerpt of the content instead. It is never
    -- generated here: whoever writes it decides what it says.
    summary      TEXT NOT NULL DEFAULT '' CHECK (length(summary) <= 500),

    -- 1..5, where 3 is the value a caller who said nothing gets. A scale
    -- rather than a flag because "how much does this matter" is the
    -- question a listing needs to rank on, and a boolean would force
    -- every fact into important-or-not.
    importance   INTEGER NOT NULL DEFAULT 3 CHECK (importance BETWEEN 1 AND 5),
    -- How sure the operator is. THREE WORDS AND NOT A NUMBER: a decimal
    -- produced by a model is false precision, nobody can defend the
    -- difference between 0.72 and 0.78, and every surface would round it
    -- into three bands anyway.
    confidence   TEXT NOT NULL DEFAULT 'medium'
                 CHECK (confidence IN ('low', 'medium', 'high')),

    -- When the thing this memory is ABOUT happened. Optional, because
    -- plenty of knowledge has no date: a preference did not occur.
    -- Distinct from created_at, which is when it was written down.
    occurred_at  TIMESTAMPTZ,

    -- Optional context. Both nullable, both composite-keyed to the
    -- workspace for the reason rooms_workspace_id_key states.
    room_id      UUID,
    artifact_id  UUID,

    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'archived')),
    sensitivity  TEXT NOT NULL DEFAULT 'normal'
                 CHECK (sensitivity IN ('normal', 'private', 'highly_sensitive')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,

    CONSTRAINT memories_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT memories_room_fk FOREIGN KEY (workspace_id, room_id)
        REFERENCES palace.rooms (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT memories_artifact_fk FOREIGN KEY (workspace_id, artifact_id)
        REFERENCES palace.artifacts (workspace_id, id) ON DELETE RESTRICT
);

CREATE INDEX memories_workspace_idx
    ON palace.memories (workspace_id, updated_at DESC)
    WHERE deleted_at IS NULL;

-- Backs "what decisions have I made", which is what a kind exists for.
CREATE INDEX memories_kind_idx
    ON palace.memories (workspace_id, kind, updated_at DESC)
    WHERE deleted_at IS NULL;

-- Backs "what do I know about this artifact / this room".
CREATE INDEX memories_room_idx
    ON palace.memories (workspace_id, room_id, updated_at DESC)
    WHERE deleted_at IS NULL AND room_id IS NOT NULL;

CREATE INDEX memories_artifact_idx
    ON palace.memories (workspace_id, artifact_id, updated_at DESC)
    WHERE deleted_at IS NULL AND artifact_id IS NOT NULL;

-- ── memory_sources ─────────────────────────────────────────────────────
--
-- Provenance: which evidence a memory came from.
--
-- ── Why this is a table and not a relation kind ────────────────────────
-- `relations` is polymorphic and therefore cannot carry a foreign key of
-- any sort: a fabricated id would insert cleanly and point at nothing.
-- Provenance is the one link in this context with a hard integrity
-- requirement, it is the answer to "why do I believe this", so it gets
-- a typed table where the database guarantees both ends exist and both
-- belong to the same workspace. That is also why DERIVED_FROM is absent
-- from the relation vocabulary: one mechanism, not two.
CREATE TABLE palace.memory_sources (
    workspace_id UUID NOT NULL,
    memory_id    UUID NOT NULL,
    source_id    UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (memory_id, source_id),

    -- CASCADE from the memory, RESTRICT from the source: the link has no
    -- meaning without the memory it explains, and evidence outlives the
    -- conclusions drawn from it.
    CONSTRAINT memory_sources_memory_fk FOREIGN KEY (workspace_id, memory_id)
        REFERENCES palace.memories (workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT memory_sources_source_fk FOREIGN KEY (workspace_id, source_id)
        REFERENCES palace.sources (workspace_id, id) ON DELETE RESTRICT
);

-- Backs "what evidence is behind this memory", the read that exists.
CREATE INDEX memory_sources_memory_idx
    ON palace.memory_sources (workspace_id, memory_id);

-- Backs the reverse, "what did this source produce".
CREATE INDEX memory_sources_source_idx
    ON palace.memory_sources (workspace_id, source_id);

-- ── relations ──────────────────────────────────────────────────────────
--
-- Controlled links between Palace entities.
--
-- ── What is NOT here, and why ──────────────────────────────────────────
-- BELONGS_TO is absent: belonging is a COLUMN (artifacts.room_id,
-- memories.room_id, memories.artifact_id). Two ways to say the same thing
-- is the arrangement where, on the day they disagree, an artifact is in
-- two rooms and no listing is right.
--
-- DERIVED_FROM is absent: see memory_sources above.
--
-- ── Why there is no foreign key ────────────────────────────────────────
-- Because the endpoints are polymorphic and Postgres cannot reference
-- "one of three tables". The integrity this table cannot enforce is
-- therefore enforced one layer up: the application service resolves BOTH
-- endpoints in the caller's workspace before inserting, and a row whose
-- endpoint it could not resolve is never written. That is stated here so
-- a future reader knows the absence is covered rather than overlooked.
CREATE TABLE palace.relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    from_type    TEXT NOT NULL CHECK (from_type IN ('room', 'artifact', 'memory')),
    from_id      UUID NOT NULL,
    kind         TEXT NOT NULL
                 CHECK (kind IN ('related_to', 'decision_for', 'mentions', 'supersedes')),
    to_type      TEXT NOT NULL CHECK (to_type IN ('room', 'artifact', 'memory')),
    to_id        UUID NOT NULL,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Nothing relates to itself. SUPERSEDES especially: a row that
    -- replaced itself would make "what is current" unanswerable.
    CONSTRAINT relations_not_reflexive CHECK (NOT (from_type = to_type AND from_id = to_id)),

    -- The closed matrix, spelled here as a backstop and validated in
    -- internal/palace/domain/relation.go, which is the only copy that
    -- decides.
    --
    -- The one rule SQL cannot express is DECISION_FOR requiring its
    -- origin memory to be of kind 'decision': that needs a join, so it is
    -- enforced in the domain against the loaded entity and nowhere else.
    CONSTRAINT relations_shape CHECK (
        (kind = 'related_to' AND (
            (from_type = 'room'     AND to_type = 'room')     OR
            (from_type = 'artifact' AND to_type = 'artifact') OR
            (from_type = 'memory'   AND to_type = 'memory')   OR
            (from_type = 'memory'   AND to_type = 'artifact')))
        OR
        (kind = 'decision_for' AND from_type = 'memory' AND to_type IN ('artifact', 'room'))
        OR
        (kind = 'mentions' AND (
            (from_type = 'memory'   AND to_type = 'artifact') OR
            (from_type = 'artifact' AND to_type = 'artifact')))
        OR
        (kind = 'supersedes' AND from_type = to_type AND from_type IN ('memory', 'artifact'))
    )
);

-- Stating the same link twice is not a second fact.
CREATE UNIQUE INDEX relations_unique_idx
    ON palace.relations (workspace_id, from_type, from_id, kind, to_type, to_id);

-- The two directions a caller reads from.
CREATE INDEX relations_from_idx
    ON palace.relations (workspace_id, from_type, from_id);

CREATE INDEX relations_to_idx
    ON palace.relations (workspace_id, to_type, to_id);

-- ── sessions ───────────────────────────────────────────────────────────
--
-- Short-term working context: what the operator is currently on.
--
-- ══════════════════════════════════════════════════════════════════════
--  ONE OPEN SESSION PER WORKSPACE IS A v1 RESTRICTION, NOT A LAW
-- ══════════════════════════════════════════════════════════════════════
--
-- The partial unique index below is what makes `palace.session.get`
-- deterministic: there is one current working context and no tie to
-- break. That is the right answer for v1, where the only channel is a
-- conversation with an agent through one interface.
--
-- It is explicitly NOT a permanent conceptual invariant. Concurrent
-- channels (Web and Telegram at the same time, two devices, a scheduled
-- process) are a real direction, and they need independent sessions.
-- Relaxing this means dropping this index and giving Session a channel
-- identity; both are additive, and neither is being designed now. Do not
-- build anything that depends on "there is exactly one session" as
-- though it were a truth about the domain.
CREATE TABLE palace.sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    status       TEXT NOT NULL DEFAULT 'open'
                 CHECK (status IN ('open', 'closed')),

    -- What the session is pointed at right now. Both optional: a session
    -- can be open with nothing focused, which is what "start" produces.
    active_room_id     UUID,
    active_artifact_id UUID,

    -- What this working stretch was about, written when it closes.
    summary      TEXT NOT NULL DEFAULT '' CHECK (length(summary) <= 2000),

    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at        TIMESTAMPTZ,

    -- The status and the timestamp move together or not at all. Without
    -- this, a row could be 'closed' with no closed_at and every "how long
    -- did I work" answer would silently be NULL.
    CONSTRAINT sessions_closed_consistent CHECK (
        (status = 'open'   AND closed_at IS NULL) OR
        (status = 'closed' AND closed_at IS NOT NULL)
    ),

    CONSTRAINT sessions_room_fk FOREIGN KEY (workspace_id, active_room_id)
        REFERENCES palace.rooms (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT sessions_artifact_fk FOREIGN KEY (workspace_id, active_artifact_id)
        REFERENCES palace.artifacts (workspace_id, id) ON DELETE RESTRICT
);

-- The v1 restriction, enforced. See the block above before removing it.
CREATE UNIQUE INDEX sessions_one_open_idx
    ON palace.sessions (workspace_id)
    WHERE status = 'open';

-- Backs "what have I been working on lately", closed sessions included.
CREATE INDEX sessions_workspace_idx
    ON palace.sessions (workspace_id, last_activity_at DESC);
