-- Palace 0.0.2 — Spatial Foundation.
--
-- ── What this release is ───────────────────────────────────────────────
-- Palace gains surfaces. 0.0.1 had none: the capabilities were the whole
-- interface and the chat was the only way in. This version adds two
-- read-only projections of the same semantic Palace — a conventional
-- Library and a spatial one — and nothing else.
--
-- ── Why still 0.0.x and still `beta` ───────────────────────────────────
-- The domain did not move: no entity, no column, no capability and no
-- migration of the palace schema. What changed is what can be looked at.
-- `beta` for the same reason as 0.0.1: the claim is about exposure, not
-- about known defects.
--
-- ── Why no capability appears in this snapshot ─────────────────────────
-- Because none was added. The count is still 24, still all Confidential,
-- still none External, and the tools return exactly what they returned in
-- 0.0.1 — there is a test that says so. The new visibility policy is
-- additive, has a safe zero value, and is switched on only by the two
-- surfaces.
--
-- ── doc_refs is deliberately empty ─────────────────────────────────────
-- Same rule 0015 set: no public row references documentation under
-- `docs/`, which is private and not in Git.
--
-- ── Every number below was counted, not quoted ─────────────────────────
--    8  `grep -cE 'r\.Get\(' internal/palace/adapters/httpapi/handler.go`
--    0  `grep -cE 'r\.(Post|Put|Patch|Delete)\(' …/handler.go`
--   24  `grep -rhoE 'Confidential: true' internal/palace/tools/*.go | wc -l`
--    0  `grep -rhoE 'External:[[:space:]]*true' internal/palace/ | wc -l`
--  396  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' internal/palace | wc -l`
-- 1498  backend, same command over `.`
--  556  frontend, `npm run test`
--    0  `git diff --name-only palace/v0.0.1..HEAD -- backend/migrations | wc -l`
--    0  same, over `frontend/package.json backend/go.mod`
--   75  same, over everything

-- The module description from 0015 said Palace "has no screen of its
-- own". That was true when it was written and is false now. The module
-- row is not frozen — only published releases are — so it is corrected
-- rather than left lying.
UPDATE releases.modules
   SET description = 'Persistent personal memory: what the operator wants to keep, what it came from, and how the pieces relate. Written through an authorized agent; read through a Library and a spatial Palace.'
 WHERE key = 'palace';

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'palace', '0.0.2', 0, 0, 2,
    'published', 'beta',
    TIMESTAMPTZ '2026-09-18 23:50:00+00',
    TIMESTAMPTZ '2026-09-18 23:50:00+00',

    'Palace becomes something a person can look at. Two read-only projections of the same '
    || 'record: a Library, which is a conventional listing with filters, search and '
    || 'pagination, and a spatial Palace, where each Room is an isometric scene and the '
    || 'artifacts in it are objects with shapes that mean something. The arrangement is '
    || 'computed from the record every time and never stored, so the whole visual layer '
    || 'could be deleted without losing a byte of knowledge. Editing a title does not move '
    || 'anything, and creating something new does not move anything either. What the '
    || 'operator marked as most reserved is absent from both surfaces, and so is anything '
    || 'inside it, with no count and no placeholder left behind to hint that something was '
    || 'held back. Nothing here writes: the way into the Palace is still a conversation.',

    '[
      {"name": "Palace Library",          "note": "Rooms, artifacts and memories as listings, with filters, literal search, numeric pagination and a visible total. Navigation state lives in the URL, so a filtered view is a link."},
      {"name": "Palace Map",              "note": "The entrance: one vignette per Room, drawn as architecture. A shelf of vignettes rather than a floor plan, because a plan would place rooms next to each other and no such relation exists."},
      {"name": "Room Spatial Scene",      "note": "A Room as an isometric cutaway. Objects stand for artifacts, are reachable by mouse and by keyboard, and open onto the real content."},
      {"name": "Deterministic layout",    "note": "Where everything sits is a pure function of the record: creation order, fixed anchors per kind, twelve slots per group and a pile for the rest. No coordinate is stored anywhere."},
      {"name": "Artifact affordances",    "note": "A project is a work desk, a list is a drawer, a plan is an ordered shelf, a note is a wall board. The shape states what the thing is; all four open the same inspector."},
      {"name": "Room memory surface",     "note": "One surface per Room that has memories, in a cell no group can ever reach. One surface, never one object per memory: the geometry is told that memories exist and never how many."},
      {"name": "Surface visibility",      "note": "Visual containment inherits visibility, not sensitivity. An ordinary artifact inside a reserved Room is absent from every listing, count, search and deep link, and so is a memory attached to it."},
      {"name": "Safe relation neighbours","note": "An artifact''s relations, resolved under the same visibility as the surface. A withheld neighbour is never loaded rather than filtered after loading, and no number reveals that it was dropped."},
      {"name": "Read-only HTTP surface",  "note": "Eight GET routes under /palace, guarded by the workspace middleware. No write verb exists on this surface in any form."},
      {"name": "Keyboard and screen reader", "note": "Every object is reachable by keyboard and carries a name. Focus order is the order a person would be walked through the room, never the visual order, and decoration is unreachable by construction."},
      {"name": "Small-viewport fallback", "note": "When objects would scale below a pressable size the Room shows its Library instead, says so, and offers the spatial view explicitly. A target too small to press is an affordance that lies."},
      {"name": "Navigation continuity",   "note": "One route boundary instead of six, page transitions that never start invisible, route code fetched on hover and on focus, and listings that keep the rows already on screen while a new query travels."}
    ]'::jsonb,

    '[
      {"label": "HTTP routes added",            "value": "8, every one a GET"},
      {"label": "Write routes under /palace",   "value": "0"},
      {"label": "palace.* capabilities",        "value": "24, unchanged from 0.0.1"},
      {"label": "Migrations added or changed",  "value": "0"},
      {"label": "Dependencies added",           "value": "0"},
      {"label": "Tests in internal/palace",     "value": "396, up from 337"},
      {"label": "Backend tests",                "value": "1.498"},
      {"label": "Frontend tests",               "value": "556, up from 385"},
      {"label": "Decoration combinations",      "value": "18, equally weighted, seeded only by the room id"},
      {"label": "Objects shown per group",      "value": "12, with the remainder as a pile in one further cell"},
      {"label": "Touch-size floor",             "value": "44 px to hand over to the Library, 52 px to take the room back"},
      {"label": "Palace bundle",                "value": "56,08 kB, 12,90 kB gzipped, loaded only on the route"},
      {"label": "Validation by use",            "value": "User Beta on the operator''s own environment, plus keyboard and navigation regression in a real browser"}
    ]'::jsonb,

    '[
      {"text": "Nothing on these surfaces writes. Every change to the Palace still goes through an authorized agent in the chat, and there is no button anywhere that creates, edits, archives or deletes."},
      {"text": "Retrieval is still structured and literal. The Library filters on titles and fields; there is no semantic or vector search, and this is unchanged from 0.0.1."},
      {"text": "A Room with more than 100 active artifacts draws only the first hundred in its scene. The Library reaches the rest, and the scene does not say that it stopped."},
      {"text": "There is no archive chest in the room. Archived artifacts are reachable only through the Library''s status filter."},
      {"text": "The relation neighbours endpoint has no interface yet. It is implemented and tested, and nothing on screen consumes it."},
      {"text": "The neighbours response reads a bounded number of relations and cannot report that it stopped, because a field able to say so would be the count this endpoint exists in order not to carry."},
      {"text": "A room listing called without an explicit page size reports a window of zero while applying twenty-five. No screen depends on it; the envelope is still describing a window that was not the one used."},
      {"text": "The interactive layer of a room appears one frame after the drawing, on the first time a room is opened in a session."},
      {"text": "The Library search box drops characters when typed faster than about one key every fifty milliseconds. It behaves the same as it did before this release and is not a regression."},
      {"text": "There is no panning and no zooming. The room is fitted to the space available and that is the only camera there is."},
      {"text": "There is no marker saying where the operator is. An open session left behind would point at a room abandoned days ago, and a wrong marker is worse than none."},
      {"text": "Palace screens may show a state up to thirty seconds old. There is no write from the browser, so there is no local action able to refresh them sooner."},
      {"text": "The first load of the application still ships more JavaScript than it should. It is measured, it is recorded, and it is not addressed here."},
      {"text": "Job Radar did not receive the freshness policy the other modules did. It does not use the query layer the policy is expressed in, and changing that is work on Job Radar."}
    ]'::jsonb,

    '[
      {"text": "Visual containment inherits visibility, not sensitivity. If a Room cannot appear, nothing spatially inside it can appear either, and the rule is transitive down to a memory attached to a withheld artifact."},
      {"text": "No surface reports how much was withheld. No count, no placeholder, no divergent total, no empty chip. Withholding the text while leaking the number withholds nothing that matters."},
      {"text": "Absent, missing and forbidden give the same answer: 404, never 403. A 403 confirms that the thing exists."},
      {"text": "No coordinate is ever stored, in the database, in the browser, or in any cache. The visual Palace is a disposable projection, and deleting both surfaces would lose no knowledge."},
      {"text": "Editing meaning must not move space. The layout cannot read a title, a status or a modification time because those are not in the type it is given, so the guarantee is a compile error rather than care."},
      {"text": "Adding does not move. A new artifact takes the next free place and nothing already drawn shifts, including at the boundary where the pile begins."},
      {"text": "An empty group leaves a gap rather than closing up. Compacting would mean that creating a first project pushes every other group across the room."},
      {"text": "The map is a shelf of vignettes, not a floor plan. Rooms are not next to each other, so the entrance refuses to draw anything that suggests they are."},
      {"text": "Decoration is seeded only by the room''s identifier, with constant density. Not the name, which is the operator''s own words; not a count, because density that varies with quantity communicates quantity."},
      {"text": "An affordance opens onto real content or it is not drawn. An empty container does not render, and decoration can never be reached."},
      {"text": "Anticipating a navigation may spend bandwidth and may never read the operator''s data. Code is versioned and identical for everyone; a query would appear in the log because a pointer crossed a word."},
      {"text": "Known content is never replaced by a loading state. A new query preserves the rows already on screen and says that it is updating; only the absence of any known answer shows a skeleton."},
      {"text": "The box that decides whether the room fits holds nothing. When the measured element contained what the verdict changed, the decision was an input to itself and the same window gave opposite answers."}
    ]'::jsonb,

    '[
      {"text": "One predicate builder is shared by the listing, the count and the per-room aggregate, so a count cannot disagree with the listing it summarises. The rule is a correlated EXISTS in SQL, not a filter applied after the rows arrive."},
      {"text": "The visibility type''s zero value is the Core''s own posture, so a future surface that forgets to switch the policy on becomes more restrictive, never less."},
      {"text": "Responses are assembled field by field. The domain redacts itself when serialised, so a whole entity cannot be leaked by accident, and a test asserts that no response ever carries the redaction marker."},
      {"text": "Neighbour resolution costs a fixed number of queries regardless of how many relations an artifact has, and the direction of supersedes is derived rather than read off the raw edge."},
      {"text": "Geometry, presentation and rendering are three layers with one direction. The engine decides where, the presentation decides what that place looks like, and the renderer draws; a position computed in two places is how an object ends up under a cabinet."},
      {"text": "Painting order and focus order are different questions and are answered separately: back to front for the drawing, semantic for the keyboard."},
      {"text": "The route shell has exactly one suspense boundary, and a structural test fails if a second appears. Six sibling boundaries produced three hundred milliseconds of blank screen on any navigation that crossed between them, with no network involved."},
      {"text": "Route code is imported in exactly one module, so the route and the preloader always name the same chunk. Two copies would drift and the symptom would be warming code the click does not use."}
    ]'::jsonb,

    '[]'::jsonb
);
