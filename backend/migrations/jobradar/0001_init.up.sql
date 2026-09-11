-- Job Radar owns its own Postgres schema and its own migration timeline
-- (`schema_migrations_jobradar`).
--
-- ── Why this schema exists at all ──────────────────────────────────────
-- Until this migration, Job Radar was a browser. Its entities lived in
-- `localStorage` under `corsi.module.jobradar.v1`, which means the pipeline
-- had no server-side existence: nothing could read it, nothing could write
-- it, and no capability could be granted over it. A tool that claimed to
-- move an opportunity would have had nothing to move.
--
-- The module keeps the shape the frontend already proved out — the types in
-- frontend/src/pages/app/modules/job-radar/types.ts are the design this
-- schema makes durable, not a new one. What changes is the authority: the
-- stage vocabulary, the transitions and the timestamps become facts the
-- database enforces rather than conventions a component honoured.
--
-- ── Why not inside `chat` ──────────────────────────────────────────────
-- Because Agents must remain unable to name Job Radar, exactly as it is
-- unable to name GitHub. A pipeline table in the chat schema would make the
-- Agents module the owner of an opportunity's lifecycle, which is the
-- `Module → Module` coupling the architecture forbids.
CREATE SCHEMA IF NOT EXISTS jobradar;

-- ── companies ──────────────────────────────────────────────────────────
--
-- A company is a name an opportunity points at. It is its own table rather
-- than a string on the opportunity because the same employer appears across
-- several postings, and "every Stripe role" has to be one question.
CREATE TABLE jobradar.companies (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    domain       TEXT NOT NULL DEFAULT '' CHECK (length(domain) <= 255),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One company per name per workspace, case-folded.
--
-- The frontend already deduplicated case-insensitively on write
-- (findOrCreateCompany). Making it a UNIQUE index moves that rule from a
-- callback that runs when the UI happens to call it to something the
-- database cannot be talked out of — which matters now that a tool can
-- create rows too.
CREATE UNIQUE INDEX companies_name_idx
    ON jobradar.companies (workspace_id, lower(name));

-- ── opportunities ──────────────────────────────────────────────────────
--
-- ── Why `stage` is nullable and that is the whole lifecycle ────────────
-- Job Radar's lifecycle is binary, and the frontend states it plainly:
-- `tracking === null` means the record lives in Discover, and a non-null
-- tracking means it sits at one of six pipeline stages. NULL here is that
-- same fact, stored once. An `is_tracked` boolean beside a stage would let
-- the two disagree, and the row where they disagreed would be a record in
-- Discover holding an interview date.
CREATE TABLE jobradar.opportunities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    company_id   UUID NOT NULL REFERENCES jobradar.companies (id) ON DELETE RESTRICT,

    role         TEXT NOT NULL CHECK (length(role) BETWEEN 1 AND 200),
    salary       TEXT NOT NULL DEFAULT '' CHECK (length(salary) <= 120),
    location     TEXT NOT NULL DEFAULT '' CHECK (length(location) <= 200),
    -- Free-form tags as the posting stated them. An array rather than a join
    -- table because nothing queries across opportunities by stack yet, and a
    -- table would be modelling a question nobody asks.
    stack        TEXT[] NOT NULL DEFAULT '{}',
    description  TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 20000),

    -- Where the record came from. 'manual' today; a collector would write
    -- its own name here. Same single-source-of-truth label the frontend set.
    source       TEXT NOT NULL DEFAULT 'manual' CHECK (length(source) BETWEEN 1 AND 60),
    source_url   TEXT NOT NULL DEFAULT '' CHECK (length(source_url) <= 1000),

    -- 0..100 when something actually computed it. NULL is "nobody scored
    -- this", and it is deliberately not 0 — the frontend's own note says a
    -- value is never fabricated, and a 0 would read as a terrible match.
    match_percent INTEGER CHECK (match_percent IS NULL OR match_percent BETWEEN 0 AND 100),

    posted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ── tracking ───────────────────────────────────────────────────────
    -- The six stages, spelled exactly as the frontend spells them. This
    -- CHECK is the canonical vocabulary of the system: internal/jobradar/
    -- domain.PipelineStage mirrors it, and the tools read that enum rather
    -- than declaring a seventh copy of the list.
    stage             TEXT CHECK (stage IN ('saved', 'applied', 'interview', 'technical', 'offer', 'rejected')),
    -- First moment this record ever entered the pipeline.
    tracked_at        TIMESTAMPTZ,
    -- Moment the CURRENT stage was entered. Reset on every move; this is
    -- what "12 days in Applied" is computed from.
    stage_entered_at  TIMESTAMPTZ,
    next_action       TEXT NOT NULL DEFAULT '' CHECK (length(next_action) <= 500),

    -- The four note channels the detail modal writes.
    notes_general    TEXT NOT NULL DEFAULT '' CHECK (length(notes_general) <= 20000),
    notes_interview  TEXT NOT NULL DEFAULT '' CHECK (length(notes_interview) <= 20000),
    notes_technical  TEXT NOT NULL DEFAULT '' CHECK (length(notes_technical) <= 20000),
    notes_personal   TEXT NOT NULL DEFAULT '' CHECK (length(notes_personal) <= 20000),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Soft delete, matching every other module here. A removed opportunity
    -- keeps its stage history rather than orphaning it.
    deleted_at   TIMESTAMPTZ,

    -- The three tracking columns move together or not at all.
    --
    -- Without this a row could carry a stage and no `stage_entered_at`, and
    -- every "how long has this been stuck" answer would silently become
    -- NULL. Expressing it as one constraint is what makes "tracked" a state
    -- with one definition instead of three columns that usually agree.
    CONSTRAINT opportunities_tracking_consistent CHECK (
        (stage IS NULL AND tracked_at IS NULL AND stage_entered_at IS NULL)
        OR
        (stage IS NOT NULL AND tracked_at IS NOT NULL AND stage_entered_at IS NOT NULL)
    )
);

-- The listing read: one workspace's live records, newest first.
CREATE INDEX opportunities_workspace_idx
    ON jobradar.opportunities (workspace_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- Backs "every opportunity at this company", which is how a model resolves
-- "aquela vaga da Stripe" into an id.
CREATE INDEX opportunities_company_idx
    ON jobradar.opportunities (workspace_id, company_id)
    WHERE deleted_at IS NULL;

-- Backs the pipeline board, which reads one stage at a time.
CREATE INDEX opportunities_stage_idx
    ON jobradar.opportunities (workspace_id, stage)
    WHERE deleted_at IS NULL AND stage IS NOT NULL;

-- ── stage_events ───────────────────────────────────────────────────────
--
-- Every stage this opportunity has ever sat at, in order.
--
-- ── Why a table and not the JSON array the frontend kept ───────────────
-- The frontend stored `history: StageVisit[]` inside the record because it
-- had one document to write. A table costs the same to append to, can be
-- indexed, and cannot be silently replaced by a client that round-trips the
-- whole object — which is precisely the risk now that both a UI and a tool
-- write here.
--
-- ── Why this is not an audit trail ─────────────────────────────────────
-- It records what the pipeline did, not who asked. WHO asked — which agent,
-- which conversation, which arguments — is already recorded by
-- chat.tool_calls for every tool execution, and duplicating that here would
-- be the second audit system the architecture explicitly refuses. This
-- table answers "how did this opportunity move"; that one answers "who
-- moved it".
CREATE TABLE jobradar.stage_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL,
    opportunity_id UUID NOT NULL REFERENCES jobradar.opportunities (id) ON DELETE CASCADE,

    -- NULL means "came from Discover": the first entry into the pipeline has
    -- no stage before it, and writing 'saved' there would invent a visit
    -- that never happened.
    from_stage TEXT CHECK (from_stage IN ('saved', 'applied', 'interview', 'technical', 'offer', 'rejected')),
    to_stage   TEXT NOT NULL CHECK (to_stage IN ('saved', 'applied', 'interview', 'technical', 'offer', 'rejected')),

    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX stage_events_opportunity_idx
    ON jobradar.stage_events (workspace_id, opportunity_id, occurred_at);
