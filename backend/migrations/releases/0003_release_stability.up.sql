-- Maturity, which is not the same fact as publication state.
--
-- The model shipped with one status column answering "has this been
-- declared?" (draft / published). It could not answer the other question a
-- release history is asked: "how much should I trust this version?".
--
-- Those are independent. A release can be published and still be a
-- release candidate; a stable version can sit unpublished while someone
-- decides. Collapsing them into one column would force a lie in one
-- direction or the other the first time a beta shipped.
--
-- So publication state stays in `status`, and maturity moves here.
--
-- ── Why the default is 'stable' ────────────────────────────────────────
-- A release recorded without qualification is an ordinary release.
-- Pre-release maturity is the thing a person opts into and says out loud,
-- which means the default that produces the fewest wrong rows is the
-- unqualified one. The existing Agents 1.0.0 draft takes this default, and
-- that matches the owner's decision of 2026-08-12 exactly.
ALTER TABLE releases.releases
    ADD COLUMN stability TEXT NOT NULL DEFAULT 'stable'
        CHECK (stability IN ('stable', 'beta', 'rc'));
