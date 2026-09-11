-- Releases: the product's own version history.
--
-- This context answers one question that no other part of the system can:
-- *what did a module look like when we shipped it?* Every other surface
-- describes the code as it exists right now. A release is the opposite —
-- it is a statement about a moment that has already passed, and it has to
-- keep being true after the code moves on.
--
-- ── Why this is not derived ────────────────────────────────────────────
-- Nothing here is computed from the repository at read time. If the number
-- of integration tests in `internal/chat` changes tomorrow, the row that
-- records what Agents v1.0.0 shipped with does not move. A history that
-- recomputes itself is not a history; it is a second, lagging view of the
-- present, and it would quietly rewrite the past every deploy.
--
-- ── Why there is no workspace_id ───────────────────────────────────────
-- Every other table in this database carries `workspace_id NOT NULL`,
-- because every other table holds a user's data. This one does not hold
-- user data. A release is a property of the deployed build: the same rows
-- are true for every workspace on the installation, and partitioning them
-- would mean the same release could be described differently to two
-- readers of the same binary. That is a deliberate, documented departure
-- from the platform's partitioning rule, not an oversight — see
-- docs/implementation/RELEASES-V1.md.

CREATE SCHEMA IF NOT EXISTS releases;

-- ── modules ────────────────────────────────────────────────────────────
--
-- The stable identity of a versioned thing. `key` is the primary key and
-- is what the API and the frontend route on: it is chosen once and never
-- reused, so a URL recorded in a document keeps resolving.
--
-- A module row is NOT a feature flag and does not control navigation.
-- `frontend/src/lib/navVisibility.ts` remains the only thing with an
-- effect on what the sidebar shows. Two sources that both claim to decide
-- visibility is exactly the drift this table must not add.
CREATE TABLE releases.modules (
    key         TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL,

    -- The module's own lifecycle, which is not the same fact as the status
    -- of its newest release. A module can be `active` with no published
    -- release at all, and a `frozen` module keeps whatever history it has.
    status      TEXT NOT NULL
                CHECK (status IN ('active', 'partial', 'frozen')),

    -- Display order. Explicit because alphabetical order is an accident of
    -- naming, and the reading order of this page is a product decision.
    position    INTEGER NOT NULL DEFAULT 0,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── releases ───────────────────────────────────────────────────────────
--
-- One row is one published (or drafted) version of one module.
--
-- The five snapshot columns are JSONB, and the choice is driven by how
-- they are read: they are ordered lists that are always fetched whole,
-- with the release, to be rendered. Nothing filters or joins on an element
-- inside them. Modelling them as five child tables would buy row-level
-- query power that no screen and no audit question asks for, and would
-- cost five joins and an ordering column each. If a future question needs
-- "which release introduced capability X", a GIN index on `capabilities`
-- answers it without reshaping the schema.
CREATE TABLE releases.releases (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_key  TEXT NOT NULL REFERENCES releases.modules (key) ON DELETE RESTRICT,

    -- The canonical SemVer string, e.g. '1.0.0', stored without a leading
    -- 'v'. The three integer columns beside it are the same fact in
    -- sortable form: ordering by the text column would put '1.10.0' before
    -- '1.9.0', which is the classic way a version list starts lying.
    version     TEXT NOT NULL,
    major       INTEGER NOT NULL CHECK (major >= 0),
    minor       INTEGER NOT NULL CHECK (minor >= 0),
    patch       INTEGER NOT NULL CHECK (patch >= 0),

    -- draft   → recorded, editable, invisible as "current"
    -- published → immutable forever (see the trigger below)
    status      TEXT NOT NULL CHECK (status IN ('draft', 'published')),

    -- When the version was released, as a product fact. Null while draft,
    -- and the CHECK makes "published with no date" unrepresentable rather
    -- than merely discouraged.
    released_at TIMESTAMPTZ,

    summary     TEXT NOT NULL,

    capabilities    JSONB NOT NULL DEFAULT '[]'::jsonb,
    evidence        JSONB NOT NULL DEFAULT '[]'::jsonb,
    limitations     JSONB NOT NULL DEFAULT '[]'::jsonb,
    decisions       JSONB NOT NULL DEFAULT '[]'::jsonb,
    technical_notes JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Pointers to the documents that justify this snapshot. References,
    -- never the source: the page renders the row, and does not read
    -- Markdown to reconstruct what shipped.
    doc_refs    JSONB NOT NULL DEFAULT '[]'::jsonb,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,

    -- A module cannot ship the same version twice.
    CONSTRAINT releases_unique_version UNIQUE (module_key, version),

    -- The two states, spelled out so the database refuses the incoherent
    -- middle rather than trusting every future writer to remember.
    CONSTRAINT releases_published_has_date CHECK (
        (status = 'draft'     AND released_at IS NULL     AND published_at IS NULL)
     OR (status = 'published' AND released_at IS NOT NULL AND published_at IS NOT NULL)
    ),

    -- The sortable columns must agree with the string. Without this, a
    -- writer could store version='2.0.0' with major=1 and every ordering
    -- in the product would be wrong in a way no test of the API would see.
    CONSTRAINT releases_version_matches_parts CHECK (
        version = major::text || '.' || minor::text || '.' || patch::text
    )
);

-- The timeline query: newest first, for one module.
CREATE INDEX releases_timeline_idx
    ON releases.releases (module_key, major DESC, minor DESC, patch DESC);

-- "Which version was active on date X" walks published releases by date.
CREATE INDEX releases_released_at_idx
    ON releases.releases (module_key, released_at DESC)
    WHERE status = 'published';

-- ── Immutability, enforced where it cannot be bypassed ─────────────────
--
-- The application service also refuses to modify a published release, but
-- an application rule is a promise and this is a guarantee. It survives a
-- future handler that forgets, a migration that means well, and a person
-- with psql who is sure they are only fixing a typo.
--
-- The one transition it must allow is the publish itself, which is an
-- UPDATE whose OLD row is still a draft. Everything else about a row that
-- is already published — including deleting it — raises.
CREATE OR REPLACE FUNCTION releases.freeze_published()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'published' THEN
        RAISE EXCEPTION
            'release %/% is published and cannot be modified or deleted',
            OLD.module_key, OLD.version
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN CASE TG_OP WHEN 'DELETE' THEN OLD ELSE NEW END;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER releases_freeze_published
    BEFORE UPDATE OR DELETE ON releases.releases
    FOR EACH ROW EXECUTE FUNCTION releases.freeze_published();
