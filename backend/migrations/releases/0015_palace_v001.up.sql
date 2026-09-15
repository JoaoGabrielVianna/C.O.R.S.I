-- Palace 0.0.1 — the first working baseline of the Second Brain.
--
-- ── What this release is ───────────────────────────────────────────────
-- A bounded context for the operator's persistent personal memory:
-- Rooms, Artifacts with items, Memories, Sources with provenance,
-- Relations and Sessions. It has its own schema, its own migration
-- timeline, and workspace isolation in every repository operation.
--
-- ── Why 0.0.1 and why `beta` ───────────────────────────────────────────
-- 0.0.x because the shape of the domain is still free to move: nothing
-- outside this module depends on it, and no compatibility promise is being
-- made. `beta` rather than `stable` because it has been used by one
-- operator for a short time, not because anything in it is known to be
-- wrong — the maturity claim is about exposure, not about defects.
--
-- ── Why the snapshot below says so little about the UI ─────────────────
-- Because there is none. Palace ships no screen and no HTTP route in this
-- version; the 24 capabilities ARE the surface and the chat is the
-- interface. That is a recorded decision, not an omission, and it is
-- written into the limitations rather than left for a reader to discover.
--
-- ── Why the shape of this snapshot is not 0008's ───────────────────────
-- `capabilities` is {name,note}, `evidence` is {label,value}, and
-- `limitations`/`decisions`/`technical_notes` are {text,ref}. Writing
-- {name,note} into the last three — which 0008 did — decodes to BLANK,
-- which is the legacy debt already recorded against six releases. The
-- shape ratchet refuses a new row that repeats it.
--
-- ── doc_refs is deliberately empty ─────────────────────────────────────
-- Earlier releases point at paths under `docs/`, which is private and not
-- in Git. From this release onward no public row references private
-- documentation. The earlier rows are immutable by trigger and are left
-- exactly as they are.
--
-- ── Every number below was counted, not quoted ─────────────────────────
--   24  `grep -rhoE 'Confidential: true' internal/palace/tools/*.go | wc -l`
--    0  `grep -rhoE 'External:\s*true' internal/palace/ | wc -l`
--    8  `grep -cE 'CREATE TABLE' migrations/palace/0001_init.up.sql`
--  337  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' internal/palace | wc -l`
-- 1439  backend, same command over `.`
--  385  frontend, `npm run test`
--    4  `grep -n 'const maxToolRounds' internal/chat/app/send.go`

-- Palace joins the module list. Positioned after Threads and before Job
-- Radar: it is a module the operator works in, and Job Radar is explicitly
-- backstage.
INSERT INTO releases.modules (key, name, description, status, position) VALUES
    (
        'palace',
        'Palace',
        'Persistent personal memory: what the operator wants to keep, what it came from, and how the pieces relate. Operated through an authorized agent — it has no screen of its own.',
        'active',
        27
    )
ON CONFLICT (key) DO NOTHING;

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'palace', '0.0.1', 0, 0, 1,
    'published', 'beta',
    TIMESTAMPTZ '2026-09-15 12:00:00+00',
    TIMESTAMPTZ '2026-09-15 12:00:00+00',

    'The first working baseline of the operator''s Second Brain. Palace keeps what a '
    || 'person decides is worth keeping — notes, lists, projects, decisions, the material '
    || 'those rest on, and the links between them — inside a bounded context with its own '
    || 'schema and workspace isolation on every read and write. There is no screen: the '
    || '24 palace.* capabilities are the surface and the conversation is the interface. '
    || 'Every capability withholds its payload from the audit trail, so the record says '
    || 'what ran and never what it was about. An interrupted turn can be continued rather '
    || 'than repeated, because a write now says which entity it touched without saying '
    || 'anything about what that entity contains.',

    '[
      {"name": "Rooms",            "note": "Named areas that group what belongs together. A Room carries its own sensitivity, so the area can be more reserved than the things inside it."},
      {"name": "Artifacts",        "note": "The kept things: a note, a list, a project. The kind is fixed at creation, because a list that silently became a note would leave its entries orphaned."},
      {"name": "Artifact Items",   "note": "Entries on a list, ordered, individually completable and removable. Unitary by design: one entry is one call, and the schema is a flat object of scalars."},
      {"name": "Memories",         "note": "What was concluded, as opposed to what was said. Kinds separate a fact from a decision from a preference from something learned."},
      {"name": "Sources and provenance", "note": "The original material a conclusion rests on, kept apart from the conclusion. A Memory may cite a Source; the link is append-only in this version and a Memory can never be less reserved than the evidence behind it."},
      {"name": "Relations",        "note": "Typed links between entities, over a closed matrix. SUPERSEDES has a frozen direction — from the new to the old — so reversing it is a code change and not a caller''s mistake."},
      {"name": "Sessions",         "note": "A working context: what the operator is on right now, and what it is focused on. One open session per workspace in this version."},
      {"name": "Sensitivity",      "note": "Three levels across Rooms, Artifacts, Memories and Sources. The most reserved material stays out of ordinary listings and is returned only when it is asked for explicitly."},
      {"name": "24 palace.* capabilities", "note": "Eight read and sixteen write. Every one of them is Confidential and none is External. They are the whole surface: there is no HTTP route and no generic query."},
      {"name": "Chat-first interaction", "note": "An authorized agent operates Palace through conversation. Authorization is a grant per capability, granted one at a time and revocable one at a time."}
    ]'::jsonb,

    '[
      {"label": "palace.* capabilities",         "value": "24 — 8 read, 16 write"},
      {"label": "Confidential capabilities",     "value": "24 of 24"},
      {"label": "External capabilities",         "value": "0 of 24"},
      {"label": "Tables in the palace schema",   "value": "8, on a migration timeline of their own"},
      {"label": "Tests in internal/palace",      "value": "337"},
      {"label": "Backend tests",                 "value": "1.439"},
      {"label": "Frontend tests",                "value": "385"},
      {"label": "Tool rounds per turn",          "value": "up to 3 executions across 4 provider calls"},
      {"label": "Validation against a real model", "value": "claude-opus-4-7, through the production runtime and a disposable database"},
      {"label": "Validation by use",             "value": "User Beta on the operator''s own environment, including one real duplication incident that this version answers"}
    ]'::jsonb,

    '[
      {"text": "Palace has no interface of its own. There is no screen and no HTTP route; everything happens through an authorized agent in the chat. This is a decision, not a gap — but it means a person cannot browse the Palace without asking for it."},
      {"text": "Retrieval is structured and literal. Listings filter on titles and fields; there is no semantic or vector search. Finding something by the words inside an entry may take the agent more than one attempt, or a hint about where to look."},
      {"text": "There are no reminders and no scheduler. Palace records what is true; nothing in it wakes up or notifies."},
      {"text": "There is no Telegram, no voice and no speech-to-text. The only way in is the chat."},
      {"text": "There is no Trello integration."},
      {"text": "Nothing consolidates on its own. Palace never decides that something is worth keeping — only an explicit request writes."},
      {"text": "A Palace belongs to one workspace and is not shared. There is no second reader and no permission model beyond workspace isolation."},
      {"text": "One open Session per workspace, enforced in the schema. A deliberate restriction of this version rather than a property of the concept."},
      {"text": "Palace publishes nothing and acts on nothing outside this product. Every capability reads or writes the operator''s own database; none is External."},
      {"text": "The link between a Memory and its Source is append-only. A citation made in error stays, and correcting one is a change this version does not have."}
    ]'::jsonb,

    '[
      {"text": "Original material and interpretation are separate entities. A Source is what the operator supplied; a Memory is what was concluded from it. Collapsing them would make it impossible to tell a quote from a judgement after the fact."},
      {"text": "The capability declares that it is Confidential, rather than the audit trail deciding. Only the capability knows what it carries, and teaching the recorder which namespaces are sensitive would put a list of module names inside Agents."},
      {"text": "Workspace isolation is a property of every query, not of the caller''s diligence. The workspace is the first parameter of every repository operation, and a cross-workspace read collapses to the same not-found as a missing row."},
      {"text": "Chat history is not long-term memory. What a conversation said does not become a Memory; only an explicit write does."},
      {"text": "The effect reference is a type and a UUID, never a name. A free-text identifier reaching the model would be a channel around redaction, and a title cannot parse as a UUID."},
      {"text": "Recording that a write ran is not the same as knowing what it wrote. The receipt proves execution, the effect reference identifies the effect, and only a read proves state."},
      {"text": "The tool round ceiling bounds depth, not volume. Several independent calls fit in one round; what is limited is how many times the model may look at a result before deciding the next call."},
      {"text": "Palace ships no screen in this version, and the absence is recorded rather than scheduled. Building one is a separate decision with its own scope."}
    ]'::jsonb,

    '[
      {"text": "Own bounded context: internal/palace, schema palace, and the schema_migrations_palace timeline. Nothing imports it except the composition root, and it imports no other module."},
      {"text": "Confidential audit: a palace.* call is recorded with the capability, the round, the outcome, the duration and the error code, and without its arguments or its result. The redaction is what makes the trail safe to keep."},
      {"text": "Execution continuity: every assistant turn carries a compact record of the writes earlier turns performed, derived from the audit trail and never from the model''s prose. It exists because an agent whose payload is withheld could not otherwise know its own write had happened."},
      {"text": "Effect references: a capability that creates may declare which entity it created. Stored beside the outcome as a type and a uuid, with the database refusing anything else, and absent whenever the capability declines to name one."},
      {"text": "Safe Resume: a turn stopped by the round ceiling can be continued through a dedicated route bound to that turn. It writes no user message, tells the model what already executed and what is still pending, and instructs it to read rather than reconstruct."},
      {"text": "Tool depth: four provider calls per turn, so up to three rounds of tool execution. Raised from three after a live battery showed that discover, read and act is three dependent steps and did not fit."},
      {"text": "Temperature is optional and unset by default, so nothing is sent and the provider applies its own. The valid value is per-model and value-exact, which makes any platform default wrong somewhere."},
      {"text": "Money, dates and identifiers cross the tool boundary as the owning domain states them. No capability accepts free text where an identifier belongs."}
    ]'::jsonb,

    '[]'::jsonb
);
