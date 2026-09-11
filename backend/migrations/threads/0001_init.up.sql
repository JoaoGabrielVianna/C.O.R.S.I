-- Threads owns its own Postgres schema and its own migration timeline
-- (`schema_migrations_threads`).
--
-- ── What a Thread is, and what it is not ───────────────────────────────
-- A Thread is a unit of CONTENT WORK: an idea that became a draft that
-- became something publishable. It is durable and it is worked on over
-- days, across many conversations.
--
-- It is NOT a `chat.conversation`. The frontend nicknames a conversation a
-- "thread" in three places — a component file, an i18n key and a brain
-- origin kind — but no user-facing string in this product has ever said
-- the word, and the canonical name of that entity is Conversation
-- everywhere it is stored, routed or typed. So the noun was free, and it
-- is spent here, on the concept the operator actually calls a thread.
--
-- The relationship between the two is one-way and loose:
--
--     a Conversation may WORK ON a Thread     (via tools, via a reference)
--     a Thread OUTLIVES the Conversation      (it is the durable side)
--
-- ── Why not inside `chat` ──────────────────────────────────────────────
-- Because the first interface being Chat is a fact about this sprint, not
-- about the entity. A content table in the chat schema would make Agents
-- the owner of a draft's lifecycle, which is the `Module → Module`
-- coupling the architecture forbids — the same reason Job Radar refused
-- it. Chat is a way to reach Threads. It is not where Threads lives.
CREATE SCHEMA IF NOT EXISTS threads;

-- ── threads ────────────────────────────────────────────────────────────
CREATE TABLE threads.threads (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- A short handle for the piece: "Microservices cedo demais". It is how
    -- a person, and a model resolving "aquele de microservices", tells two
    -- threads apart. Required, because a content item nobody can name is a
    -- content item nobody can ask for.
    title        TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),

    -- The work itself, in whatever state it is in: one sentence of an idea,
    -- or the finished post.
    --
    -- ── Why one column and not a version table ─────────────────────────
    -- Because the product need is "what is it NOW", and every screen, tool
    -- and hydration reads exactly that. Version history is a real feature
    -- with real questions attached (what is a version, who cut it, how is
    -- one restored) and none of them have been asked yet. A `versions`
    -- table added on the chance it is wanted would be answering them by
    -- accident.
    --
    -- 20000 is the ceiling every other long-text column in this system
    -- carries (chat.agent_sources.content, jobradar.opportunities.
    -- description). One number, so a limit is a property of the platform
    -- rather than a guess made per table.
    content      TEXT NOT NULL DEFAULT '' CHECK (length(content) <= 20000),

    -- ── the lifecycle ──────────────────────────────────────────────────
    -- Five states, spelled exactly as internal/threads/domain.Status
    -- spells them. That package is the only copy that VALIDATES; this
    -- CHECK is a backstop that refuses a row the domain would never build.
    --
    -- No `ready` between review and published, and that is a decision
    -- rather than an omission: with a single operator and no approval
    -- system, "marca para revisão" and "está pronto" are the same person
    -- making the same judgement minutes apart, and a state that is only
    -- ever distinguished by which sentence was said is a state that will
    -- be used inconsistently. Adding it later is one CHECK and one
    -- constant — additive, and cheaper than un-teaching a habit.
    status       TEXT NOT NULL DEFAULT 'idea'
                 CHECK (status IN ('idea', 'draft', 'review', 'published', 'archived')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Soft delete, matching every other module here — and with an extra
    -- reason of its own. Content acquires links: a published post is
    -- referenced from outside this system, and a conversation that worked
    -- on a thread for a week keeps a context reference to it. A hard
    -- delete would turn both into dangling ids.
    deleted_at   TIMESTAMPTZ
);

-- The listing read: one workspace's live threads, most recently touched
-- first. `updated_at` rather than `created_at` because content work is
-- returned to — the thread edited an hour ago is the one being asked
-- about, not the one captured first.
CREATE INDEX threads_workspace_idx
    ON threads.threads (workspace_id, updated_at DESC)
    WHERE deleted_at IS NULL;

-- Backs "what do I have in review", which is the question a status exists
-- to answer.
CREATE INDEX threads_status_idx
    ON threads.threads (workspace_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;
