-- One sprint sequence, two module versions, both published.
--
-- ── Why two rows and not one ───────────────────────────────────────────
-- A release belongs to a module. The sequence that produced it does not:
-- it moved Job Radar from a browser document to a bounded context with its
-- own capabilities, and separately gave Agents a way for a conversation to
-- be ABOUT something. Recording both under one module would make the
-- history answer "what could Agents do in August" with facts about a
-- different module.
--
-- ── Why PUBLISHED, and why the owner had to say so ─────────────────────
-- Both numbers are the owner's decision, taken explicitly. Until then these
-- rows could not exist: `1.2.0` is deliberately reserved by two integration
-- tests as "a version the seeded history does not use", so claiming it is
-- not a documentation act — it moves a fixture that was already moved once
-- when `1.1.0` shipped for real.
--
-- ── Why both are `stable` ──────────────────────────────────────────────
-- Job Radar's `1.0.0` is stable rather than beta because the vocabulary is
-- about the reliability of what shipped, not about how much is left to
-- build. The module is BACKSTAGE in SCOPE — no collector, no ingestion —
-- and that is a limitation, recorded below as one. What did ship was
-- proved: five capabilities executed live, workspace isolation, soft
-- delete, import identity, 81 integration tests of its own.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 731  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--  81  `grep -hE '^func Test' internal/jobradar/*_integration_test.go | wc -l`
-- 236  `grep -hE '^func Test' internal/chat/*_integration_test.go | wc -l`
-- 291  frontend, `npx vitest run`
--   2  `ls migrations/jobradar/*.up.sql | wc -l`
--  17  `ls migrations/chat/*.up.sql | wc -l`
--   5  distinct job_radar tool names in internal/jobradar/tools/tools.go
--   7  distinct github tool names in internal/integrations/github/tools/tools.go

-- ── the module's own lifecycle ─────────────────────────────────────────
--
-- `frozen` was true when Job Radar was React over a localStorage document
-- that nothing else could read. It has a schema, a migration timeline, five
-- capabilities, a place in the navigation and now a published release, and
-- a module row still calling it frozen is the release history disagreeing
-- with the product. `active` is the existing status for exactly this; no
-- new one was invented. Modules are not immutable — only published
-- releases are.
UPDATE releases.modules
   SET status = 'active', updated_at = now()
 WHERE key = 'job-radar' AND status = 'frozen';

/* ── Job Radar 1.0.0 ─────────────────────────────────────────────────── */

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'job-radar', '1.0.0', 1, 0, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-23 00:00:00+00',
    TIMESTAMPTZ '2026-08-23 00:00:00+00',

    'Job Radar stops being a browser document. It gains a bounded context with its own '
    || 'schema and migration timeline, an HTTP surface the existing screens read and write '
    || 'through, and five capabilities an authorized agent can be granted. The pipeline, '
    || 'the manual entry form and the stage history are the ones that were already there; '
    || 'what changed is that they are now reachable by something other than one browser tab.',

    '[
      {"name": "Postgres-backed pipeline",   "note": "Own schema, own migration timeline (schema_migrations_jobradar). The screens kept their shape: readStored/writeStored became HTTP calls and every action kept its name and arguments."},
      {"name": "Manual entry, unchanged",    "note": "Add manually, the board, the stage columns and the detail modal work as before. Nothing about how a person uses the module was redesigned."},
      {"name": "Visible in the navigation",  "note": "Since 2026-08-18, alongside Agents."},
      {"name": "opportunity.list",           "note": "Read. Filters by company, stage or free text, and reports the unbounded total so a truncated listing cannot read as a complete one."},
      {"name": "opportunity.get",            "note": "Read. One opportunity in full."},
      {"name": "opportunity.create",         "note": "Write. Company and role are the only required fields; every other property is optional and left empty rather than guessed."},
      {"name": "opportunity.move",           "note": "Write. Performs the transition and records the stage event in one transaction."},
      {"name": "opportunity.delete",         "note": "Write. The same soft delete the web UI performs, through the same application operation."},
      {"name": "Context reference resolver", "note": "Job Radar answers for job_radar.opportunity, so a conversation can be ABOUT one of its records. Identity and stable recognition text only."},
      {"name": "Per-turn hydration",         "note": "Declares freshness per_turn and supplies company, role, stage and location to the turn that needs them. Gated on the job_radar.opportunity.get grant."},
      {"name": "Import identity",            "note": "A legacy record carries its own id through the import, so re-running the migration recognises what it already migrated instead of duplicating it."}
    ]'::jsonb,

    '[
      {"name": "Backend gate",         "note": "make -C backend ci green, integration suite included. 731 Go test functions overall, 81 of them Job Radar integration tests."},
      {"name": "Frontend gate",        "note": "291 tests, build clean, lint with no errors."},
      {"name": "Tools, live",          "note": "create, move and delete executed by a real agent against a real LiteLLM, each confirmed by an independent readback through the API and through Postgres."},
      {"name": "Soft delete, proved",  "note": "A removed record leaves the active listing and the get route, and stays on disk with deleted_at stamped and its stage history intact."},
      {"name": "Workspace isolation",  "note": "Another workspace''s id answers not-found for delete, indistinguishable from a fabricated one, and the row is untouched. create has no workspace property in its schema at all."},
      {"name": "Import, proved",       "note": "Import, re-import the same payload, and the row count does not move: created 2, then already_imported 2. A partial failure retried without duplicating the items that had succeeded."},
      {"name": "History preserved",    "note": "A three-visit legacy timeline arrives as three transitions, the first with no predecessor, instead of one synthetic event for wherever the record currently sits."},
      {"name": "Clock preserved",      "note": "Historical created_at and updated_at survive the import, read back from the columns rather than from the API."}
    ]'::jsonb,

    '[
      {"name": "No collector",                    "note": "Nothing ingests postings. Every record is entered by a person or by an authorized agent, and that is the declared scope."},
      {"name": "No update capability",            "note": "An agent can create, move and delete, and cannot edit a field. Correcting a salary means the form."},
      {"name": "create carries no description or stack", "note": "Deliberate: every property in the schema is one more field a model can fill in from nothing. A pasted job description has to go through the form."},
      {"name": "Duplicates across conversations", "note": "Two conversations asking for the same job produce two records. Consistent with the form, and with the decision not to dedupe on company and role."},
      {"name": "Soft-deleted rows accumulate",    "note": "Removal is reversible by design and nothing purges. Harmless at this size and unbounded in principle."},
      {"name": "source does not distinguish origin", "note": "A record typed into the form and one dictated to an agent are both manual. Chosen so the two paths stay identical; the cost is that provenance cannot be queried."},
      {"name": "The historical browser pipeline was never found", "note": "Investigated across every browser profile on the machine: corsi.module.jobradar.v1 held an empty document in all three localhost origins. The import mechanism is built, hardened and unused."}
    ]'::jsonb,

    '[
      {"name": "Tools call the application layer", "note": "Not HTTP, not the repository. The rules a write must pass — resolving the company, validating the entity, performing the transition, writing the stage event in the same transaction — live in app, and a tool holding a repository would be a second writer with its own idea of what a move is."},
      {"name": "Delete is soft",                   "note": "The same operation the web UI''s DELETE route performs. Agents did not get a second removal semantics, and a removed record keeps its history so a person can undo what an agent did."},
      {"name": "Removal is not rejection",         "note": "A rejected application stays on the board at the rejected stage; move records that. The asymmetry decided it: a rejection recorded as a deletion destroys the evidence that an application happened, and the pipeline stops being able to answer how many were sent."},
      {"name": "Import identity is the legacy id", "note": "Not company and role: two genuinely different postings for the same role at the same employer exist, and any heuristic over what a posting SAYS would collapse them. What distinguishes two rows is where they came from."},
      {"name": "Unknown stages fail the item",     "note": "The legacy document contains stages this domain does not have. Mapping one silently would invent a history the user never had, and the invention would be invisible forever after."}
    ]'::jsonb,

    '[
      {"text": "Two migrations in this module''s timeline: the initial schema and import identity.", "ref": "migrations/jobradar"},
      {"text": "The unique index on import identity is partial and deliberately counts soft-deleted rows: a record the user deleted must not come back because the import ran again."},
      {"text": "The frontend store kept every action name and argument; the actions became async because a write that pretends to be synchronous when it crosses a network is a lie the UI eventually trips over.", "ref": "pages/app/modules/job-radar/store.ts"},
      {"text": "The subtitle of a context reference carries stable facts only. The stage was removed from it: it is written once, at attach time, into a column that is never rewritten, and it is the field most likely to move while a conversation about it is happening."}
    ]'::jsonb,

    '[
      {"label": "Closure record",       "path": "docs/implementation/SCOUT-JOBRADAR-CHAT-CLOSURE.md"},
      {"label": "Modular architecture", "path": "docs/architecture/target-modular-architecture.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;

/* ── Agents 1.2.0 ────────────────────────────────────────────────────── */

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.2.0', 1, 2, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-23 00:00:00+00',
    TIMESTAMPTZ '2026-08-23 00:00:00+00',

    'A conversation can now be ABOUT something. Context References attach an entity from '
    || 'another module to a thread, and hydration puts that entity''s present state in '
    || 'front of the model on every turn that needs it — gated on the same grant the '
    || 'agent''s own tool call is gated on. MINOR rather than PATCH: a new persisted '
    || 'primitive, a new context block, and a new column pair in the chat timeline.',

    '[
      {"name": "Context References",       "note": "type + id + label + stable recognition text, persisted on a conversation and on a message. A separate primitive from TurnReference, which selects capabilities: one narrows what a turn may DO, the other says what it is ABOUT."},
      {"name": "Provider-authored labels", "note": "The client sends identity; the words come from the module that owns the entity. A client that could author the label could write anything into the permanent record and into the model''s context."},
      {"name": "Reference hydration",      "note": "A provider declares a freshness policy per type. per_turn reads the entity''s present state before the model reasons, so freshness stops depending on the model choosing to look."},
      {"name": "Hydration authorization",  "note": "Gated on a real, grantable tool name, checked with the same function the tool executor uses, against the same grant set resolved once for the turn."},
      {"name": "Reference state block",    "note": "Its own block in the context report, with its own exclusion reasons. An operator can answer, months later, whether a turn had the present state in front of it."},
      {"name": "Backend identity",         "note": "The platform''s health probes report which product answers, so a frontend cannot silently proxy to another project''s API."}
    ]'::jsonb,

    '[
      {"name": "Backend gate",            "note": "make -C backend ci green. 731 Go test functions, 236 of them chat integration tests, 17 migrations in the chat timeline."},
      {"name": "Frontend gate",           "note": "291 tests, build clean, lint with no errors."},
      {"name": "Freshness, live",         "note": "In one conversation: the stage read as applied, the entity moved to interview outside the conversation, and the next turn answered interview — with the model''s own earlier sentence still in the history and zero tool calls in the whole thread."},
      {"name": "Authorization, live",     "note": "With the read capability revoked and the reference intact, the model reported that it could not check rather than asserting a stage it had said twice already."},
      {"name": "Persistence, proved",     "note": "The conversation column carries identity and no stage word, read straight from Postgres after three hydrated turns and a stage change."},
      {"name": "Chat, through a browser", "note": "Playwright against the running product: create, move, reload, delete, all through the composer, with independent readback."},
      {"name": "Reference after delete",  "note": "A conversation whose entity was removed still opens, keeps its historical label, and marks the reference unavailable."}
    ]'::jsonb,

    '[
      {"name": "One reference type",           "note": "job_radar.opportunity. The registry takes a second provider without a code change, and none was added."},
      {"name": "One read per subject per turn","note": "Acceptable for a small entity and capped at eight subjects. The interface takes one reference at a time; batching would be additive."},
      {"name": "A turn narrowed by @ does not hydrate", "note": "Hydration is gated on the turn''s capability set, which an explicit selection narrows. Conservative by construction, and visible: scoping a turn away from the read capability costs it the entity''s state."},
      {"name": "Hydration writes no audit row","note": "chat.tool_calls records what the MODEL asked to run. A hydration row would have no provider call id and would make the transcript claim a call that never happened. The context report carries it instead, stamped on the message."},
      {"name": "Builds older than backend identity read as foreign", "note": "The dev preflight refuses a port held by a service that does not identify itself, which includes any C.O.R.S.I. build predating this release."}
    ]'::jsonb,

    '[
      {"name": "A reference grants no capability",      "note": "Holding one lets an agent know the subject exists and what it is called — which the user just told it by attaching it — and nothing more. State needs a grant, and the grant is the same one the tool call needs."},
      {"name": "Freshness is structural, not prompted", "note": "Two prompt revisions failed at this. From the model''s side its own recent answer is the best evidence it has, and instructing it to distrust that competes with instructing it to use evidence at all. So the runtime stopped asking and put the present in the context."},
      {"name": "Identity is persisted, state is not",   "note": "A reference that carried a copy would be a snapshot that starts lying the moment the entity moves. What is stored is what the item IS; what is read fresh is what it is DOING."},
      {"name": "Presentation belongs to the channel",   "note": "A tool and a reference return semantic results. Web draws a card, a text channel draws a line, and neither is in the contract — which is what keeps WhatsApp and Telegram from needing React."},
      {"name": "A port is not an identity",             "note": "Two products held :8080 simultaneously, one per address family, neither failing to bind. Checking that a port is free cannot catch that; asking the service what it is can."}
    ]'::jsonb,

    '[
      {"text": "chat 0017 added context_references to conversations and messages, both JSONB. Availability is recomputed on read rather than stored.", "ref": "migrations/chat"},
      {"text": "Reference types are two segments (provider.entity) and tool names are three (provider.entity.action). A test holds them apart so the two vocabularies cannot merge."},
      {"text": "The identity block withholds a frozen subtitle for any subject whose present state is in the same context, so the model is never handed two answers to one question."},
      {"text": "Scout is an Agent row: instructions, a model and authorized tools. There is no branch anywhere on its name, and a test asserts an agent called Scout receives no privilege."}
    ]'::jsonb,

    '[
      {"label": "Closure record",     "path": "docs/implementation/SCOUT-JOBRADAR-CHAT-CLOSURE.md"},
      {"label": "Agents module",      "path": "docs/modules/agents/CLAUDE.md"},
      {"label": "GitHub integration", "path": "docs/implementation/GITHUB-INTEGRATION-V1.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
