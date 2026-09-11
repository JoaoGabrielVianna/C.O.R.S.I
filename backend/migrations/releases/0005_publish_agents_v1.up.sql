-- The event: Agents v1.0.0 is declared released.
--
-- Owner decision of 2026-08-12, recorded verbatim in
-- docs/implementation/AGENTS-V1-RELEASE.md:
--
--     AGENTS v1.0.0 ESTÁ APROVADO PARA RELEASE.
--     Status: Stable · Release state: Published · Date: 2026-08-12
--     External LiteLLM max_budget is not a release requirement.
--
-- ── Why a migration and not the publish endpoint ───────────────────────
-- The application does have a publish flow (POST .../publish), it is the
-- runtime path, and it stays the way a release is published from the UI.
-- It is not the way this one reaches production, for a plain reason: the
-- endpoint mutates whichever database the caller can reach, and a release
-- history has to be true on every install of this software — the
-- production instance, a fresh clone, a restored backup. A migration is
-- the only mechanism in this codebase that carries a historical fact to
-- all of them, and it is how the draft itself arrived (0002).
--
-- What matters is that no rule is bypassed by taking this route. The
-- invariants the domain enforces are the same ones the schema enforces,
-- and they all apply here:
--
--   · releases_published_has_date  — published rows must carry both dates
--   · releases_freeze_published    — a published row can never be updated
--   · WHERE status = 'draft'       — the same guard the repository uses,
--                                    so this cannot re-publish or re-date
--                                    a release someone already declared
--
-- That last clause also makes it idempotent: if the owner clicks Publish
-- in the UI before this migration runs, the row is already published, no
-- row matches, and this becomes a no-op rather than overwriting the date
-- that was actually recorded.

UPDATE releases.releases
   SET status       = 'published',
       -- The product fact: the day the owner declared the release, at
       -- midnight UTC. Not the moment this SQL ran, which would make the
       -- release date depend on when a container happened to boot.
       released_at  = TIMESTAMPTZ '2026-08-12 00:00:00+00',
       -- The bookkeeping fact: when the record was written. Different
       -- question, different column, and the CHECK requires it.
       published_at = now()
 WHERE module_key = 'agents'
   AND version    = '1.0.0'
   AND status     = 'draft';
