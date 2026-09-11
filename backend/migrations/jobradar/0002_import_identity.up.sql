-- Import identity: recognising a legacy record we have already migrated.
--
-- ── The problem this closes ────────────────────────────────────────────
-- The import endpoint creates. Running it twice created twice, and the
-- only protection was a `localStorage` flag in the browser — per ORIGIN, so
-- the same document offered at :5173, :5174 and :5175 could be imported
-- three times. A flag on the client cannot be the integrity boundary for
-- rows on the server.
--
-- ── Why not dedupe on company + role ───────────────────────────────────
-- Because two genuinely different postings for the same role at the same
-- employer exist, and collapsing them would silently destroy one. The same
-- objection rules out every heuristic on the visible fields: they describe
-- the JOB, and two jobs can be described identically. What distinguishes
-- two rows is not what they say but where they came from.
--
-- So identity comes from the SOURCE DOCUMENT: the id the legacy record
-- already carried (`op_mpbh75to_fl6bu`), qualified by which kind of
-- document it came from. Two postings at one company are two legacy ids and
-- stay two rows; the same legacy id arriving twice is one row, twice.
--
--   import_source       which document format produced this row, e.g.
--                       "jobradar.localstorage.v1". Qualifies the id so two
--                       importers cannot collide on a bare "1".
--   import_external_id  the identifier inside that document.
--
-- ── Why TEXT NOT NULL DEFAULT '' rather than nullable ──────────────────
-- Every row that was not imported has the same answer — "no import
-- identity" — and that is one fact, not a missing one. Empty strings also
-- let the partial index below use a simple predicate instead of reasoning
-- about NULLs in a composite unique key, where NULLs do not compare equal
-- and the constraint quietly stops constraining.
ALTER TABLE jobradar.opportunities
    ADD COLUMN import_source      TEXT NOT NULL DEFAULT '',
    ADD COLUMN import_external_id TEXT NOT NULL DEFAULT '';

ALTER TABLE jobradar.opportunities
    ADD CONSTRAINT opportunities_import_source_check
        CHECK (length(import_source) <= 120),
    ADD CONSTRAINT opportunities_import_external_id_check
        CHECK (length(import_external_id) <= 200),
    -- Half an identity is not one. A source with no id would make every row
    -- from that document collide; an id with no source would let two
    -- importers claim the same string.
    ADD CONSTRAINT opportunities_import_identity_complete
        CHECK ((import_source = '') = (import_external_id = ''));

-- ── Why the uniqueness is partial, and why deleted rows still count ────
-- Partial, because the overwhelming majority of rows are hand-created and
-- carry no import identity; without the predicate they would all collide on
-- ('', '').
--
-- Soft-deleted rows are deliberately NOT excluded. "This legacy record was
-- imported into this workspace" is a fact about history, and a user who
-- deleted an imported opportunity meant to delete it — re-running the
-- import should not quietly resurrect it. Excluding deleted rows would make
-- re-import a way to undo a deletion nobody asked to undo.
CREATE UNIQUE INDEX opportunities_import_identity_idx
    ON jobradar.opportunities (workspace_id, import_source, import_external_id)
    WHERE import_source <> '';
