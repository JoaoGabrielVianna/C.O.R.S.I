-- One sprint, two module versions, both published.
--
-- ── Why two rows and not one ───────────────────────────────────────────
-- The same reason 0008 gives. The sequence produced a new bounded context
-- AND an addition to Agents, and those belong to different modules. What
-- Threads can do is not an answer to "what could Agents do in August".
--
-- The split is sharper here than it was for Job Radar, because the
-- temptation is stronger: hydration of a Thread reads a Thread. But the
-- MECHANISM is Agents' and the ENTITY is Threads', and recording the
-- entity's fields as an Agents capability would make the Agents history
-- grow a row every time some other module registers a provider.
--
-- ── Why PUBLISHED, and why the owner had to say so ─────────────────────
-- Both numbers are the owner's decision, taken explicitly, after the
-- foundation closed. `1.3.0` is deliberately reserved by two integration
-- tests as "a version the seeded history does not use", so claiming it is
-- not a documentation act — it moves a fixture that has now been moved
-- three times, at 1.1.0, at 1.2.0 and here.
--
-- ── Why both are `stable` ──────────────────────────────────────────────
-- The stability vocabulary is about the reliability of what shipped, not
-- about how much is left to build — the rule 0008 stated and this release
-- has to apply to a harder case. Threads has NO SCREEN. That is scope,
-- declared and recorded below as a limitation, exactly as Job Radar's
-- missing collector was. What did ship was proved: five capabilities, a
-- reference provider, per-turn hydration, workspace isolation, soft
-- delete, and 75 tests of its own.
--
-- A release is not `beta` because a surface it never claimed is absent.
--
-- ── Why the declared date is midday and not midnight ───────────────────
-- Agents 1.2.0 declares 2026-08-23 00:00:00+00. A second Agents release on
-- the same declared instant would make releases_released_at_idx — "which
-- version was active on date X" — unable to answer. Same product day,
-- strictly after the release it follows.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 807  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--  58  `grep -hE '^func Test' internal/threads/*_integration_test.go | grep -v TestMain | wc -l`
--  17  `grep -hE '^func Test' internal/threads/domain/*_test.go | wc -l`
-- 236  `grep -hE '^func Test' internal/chat/*_integration_test.go | wc -l`
--  17  `ls migrations/chat/*.up.sql | wc -l`
--   1  `ls migrations/threads/*.up.sql | wc -l`
--   1  `grep -hoE 'CREATE TABLE threads\.[a-z_]+' migrations/threads/*.up.sql | sort -u | wc -l`
--   5  distinct threads tool names in internal/threads/tools/tools.go
--   5  lifecycle statuses in internal/threads/domain/thread.go
--   6  `ls -d migrations/*/ | wc -l`
--   0  `grep -c 'r\.Route\|r\.Get\|r\.Post' internal/threads/module.go`
--   0  `grep -rl 'threads.thread' frontend/src | wc -l`
-- 291  frontend, `npx vitest run`

-- ── the module's identity ──────────────────────────────────────────────
--
-- Threads has to exist in this catalogue before it can have a release:
-- module_key is a foreign key with ON DELETE RESTRICT.
--
-- ── Why `active` and not `partial` ─────────────────────────────────────
-- `partial` is Finance's status, and it means something specific here: a
-- module whose declared scope is half built — two domain entities still
-- living in the browser, a route hidden from the navigation. Threads is
-- not that. Everything in its declared scope shipped, is exercised daily
-- through a real agent, and is covered by 75 tests.
--
-- The absence of a screen is SCOPE, not incompleteness, and the same rule
-- 0008 applied to Job Radar applies here: a module status describes the
-- module's lifecycle, not how much surface it has. No new status was
-- invented, and none was needed.
--
-- ── Why position 25 ────────────────────────────────────────────────────
-- Between Finance (20) and Job Radar (30), which puts the three real
-- modules ahead of the backstage one and renumbers nothing. Existing rows
-- are not touched.
INSERT INTO releases.modules (key, name, description, status, position) VALUES
    (
        'threads',
        'Threads',
        'Content work as durable state: an idea, the drafts it becomes, and the lifecycle between them. Operated through an authorized agent — it has no screen of its own.',
        'active',
        25
    )
ON CONFLICT (key) DO NOTHING;

/* ── Threads 1.0.0 ───────────────────────────────────────────────────── */

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'threads', '1.0.0', 1, 0, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-23 12:00:00+00',
    TIMESTAMPTZ '2026-08-23 12:00:00+00',

    'The first operational baseline of Threads: content work stored as durable state rather '
    || 'than as messages in a conversation. A bounded context with its own Postgres schema '
    || 'and migration timeline, one entity with a five-state lifecycle, and five capabilities '
    || 'an authorized agent can be granted. The scope of this release is explicitly backend '
    || 'plus Agent tools plus the Chat interface: there is no Threads screen, and that is a '
    || 'decision rather than a gap.',

    '[
      {"name": "Own bounded context",      "note": "internal/threads — domain, ports, application service and adapters. It imports no other bounded context; the tools subpackage imports the Agents module PORTS, which is dependency inversion and the same seam GitHub and Job Radar already use."},
      {"name": "Postgres persistence",     "note": "Schema threads, one table, own migration timeline (schema_migrations_threads). Six timelines now, each with its own version table; omitting -table would make the runner read finance''s and create no schema at all."},
      {"name": "Thread, not Conversation", "note": "A durable unit of content work with title, content and status. A conversation may work on a Thread; the Thread outlives it. Nothing in the domain names Chat, agents or conversations."},
      {"name": "Five-state lifecycle",     "note": "idea, draft, review, published, archived. domain.Status is the only copy that validates: the CHECK constraint is a backstop and the tool schemas describe themselves from the enum."},
      {"name": "thread.list",              "note": "Read. Filters by status or free text over title and content, carries a bounded excerpt per row for disambiguation, and reports the unbounded total so a truncated listing cannot read as a complete one."},
      {"name": "thread.get",               "note": "Read. One thread with its complete current text — abbreviating it would produce an edit that silently truncates the user''s work."},
      {"name": "thread.create",            "note": "Write. Title and content required, status optional and defaulting to idea."},
      {"name": "thread.update",            "note": "Write. Partial: title, content and status are independently optional and absent means unchanged. This is the operation the domain exists for."},
      {"name": "thread.delete",            "note": "Write. Soft delete, the same operation any other surface would call."},
      {"name": "Context reference provider","note": "Threads answers for threads.thread, so a conversation can be ABOUT a piece of content. Identity only: the label is the title and there is no subtitle."},
      {"name": "Per-turn hydration",       "note": "Declares freshness per_turn and supplies title, status and the current text to the turn that needs them. Gated on the threads.thread.get grant."},
      {"name": "Workspace isolation",      "note": "Every operation is workspace-scoped in SQL. No tool schema has a workspace property, so the model has no argument through which to name one."},
      {"name": "Deny-by-default",          "note": "None of the five is authorized on any agent by existing. Capability comes from a grant an operator can see and revoke, and from nowhere else."},
      {"name": "Audit through Chat",       "note": "Tool executions land in chat.tool_calls with tool, arguments, result, status, round, duration and conversation. No second audit log was built."}
    ]'::jsonb,

    '[
      {"name": "Backend gate",           "note": "make -C backend ci green, integration suite included. 807 Go test functions overall; 75 of them are new here — 58 Threads integration tests and 17 domain unit tests."},
      {"name": "Frontend gate",          "note": "291 tests, build clean, lint with no errors. Zero frontend files were changed."},
      {"name": "Six turns, live",        "note": "create, update, update, status change, list and delete executed by a real agent against a real LiteLLM, each confirmed by an independent readback in Postgres."},
      {"name": "Evolution on one thread","note": "An idea became a LinkedIn draft, then had its hook rewritten, then moved to review — one row throughout, with the stored text actually changing and the status change leaving the text untouched."},
      {"name": "Criticism is not delete","note": "\"Esse texto ficou ruim\" produced zero tool calls; the thread stayed at review and the agent asked what had not worked."},
      {"name": "Ambiguity, live",        "note": "A delete request whose stated reason did not match the thread''s state was confirmed with the user before executing, not guessed."},
      {"name": "Workspace isolation",    "note": "Another workspace''s live thread, a fabricated id and a soft-deleted thread all return the same refusal, so the difference cannot be used to probe."},
      {"name": "Soft delete, proved",    "note": "A removed thread leaves every read path while the row and its text stay on disk with deleted_at stamped."},
      {"name": "Empty content refused",  "note": "An update carrying blank content is refused rather than stored: content replaces, and an empty replacement would erase the piece while reporting success."},
      {"name": "Deny-by-default, live",  "note": "On the running system the existing Scout agent sees all five capabilities in its catalogue and holds none, and the Content agent had zero before anyone granted anything."},
      {"name": "Revoke, live",           "note": "With create revoked mid-conversation the agent reported it no longer had the capability, and no row was created."},
      {"name": "Final state clean",      "note": "The exercise ended with 0 active Threads; the fixture rows are soft-deleted, which is the designed behaviour."}
    ]'::jsonb,

    '[
      {"name": "No frontend of its own",        "note": "No page, board, editor, navigation item, filter or calendar. The interface is a conversation with an authorized agent. This is the declared scope of the release, not an unfinished part of it."},
      {"name": "No public HTTP CRUD",           "note": "The module registers no routes. A surface with no caller is one nobody is testing; adding it the day a screen exists is a Register method and a handler, over a domain and a schema that are already proved."},
      {"name": "No content version history",    "note": "content is one column. A rewrite replaces what was there, and draft A cannot be recovered after draft B. Deliberate — versioning brings questions nobody has asked yet — and the most likely follow-up to become a real need."},
      {"name": "Soft-deleted rows accumulate",  "note": "Removal is reversible by design and nothing purges. Harmless at this size and unbounded in principle. Same state as Job Radar."},
      {"name": "No publishing of any kind",     "note": "The published status records that the USER published something somewhere. Nothing in this system posts, schedules or observes a post."},
      {"name": "No Telegram or WhatsApp",       "note": "Tool results and reference state are semantic and channel-agnostic, which is the only thing that needed doing now. No second channel was implemented."},
      {"name": "No scheduling, calendar, analytics, attachments or media", "note": "None of them has a question in the open. Every one is a field that would be written and never read."},
      {"name": "No channel or format field",    "note": "A piece reshaped from LinkedIn to a newsletter has nowhere to record that but its own text. The trigger for adding one is a question this shape cannot answer, asked by someone with enough pieces for the answer to matter."}
    ]'::jsonb,

    '[
      {"name": "A content Thread is not a Conversation", "note": "The two were checked for collision before the name was taken: the backend had zero identifiers called thread, and no user-facing string in this product has ever said the word. Conversation is the canonical name of the chat entity everywhere it is stored, routed or typed. Nothing was renamed."},
      {"name": "update is the centre of the domain",     "note": "Unlike Job Radar, where an agent can create, move and delete but not edit, editing is the whole point here. Content evolves in place: an idea becomes a draft becomes a rewritten draft, and each step is the same row."},
      {"name": "The tool persists the result, never the instruction", "note": "\"Deixa mais provocativo\" is an instruction to the agent. The agent writes the new version and sends the new version. Storing the instruction where a draft was would destroy the work and report success — which is why the contract states it in the imperative."},
      {"name": "Criticism is not removal",              "note": "\"Esse texto ficou ruim\" is feedback, and the answer to feedback is a rewrite. The asymmetry decided it: deleting on a bad review destroys work the user did not ask to lose, while leaving a weak draft costs one line in a listing. A piece the user gave up on is archived, not deleted."},
      {"name": "The lifecycle is not a state machine",  "note": "Every transition is allowed in both directions. A published post is pulled back to draft to be reworked and an archived idea revives when it suddenly matters; encoding an order would make the system refuse the truth because it disagreed with a diagram."},
      {"name": "ready does not exist in this version",  "note": "With a single operator and no approval workflow, \"marca para revisão\" and \"está pronto\" are the same person''s judgement minutes apart. A state told apart only by which sentence was said gets used inconsistently, which makes every later question of it quietly wrong. Adding it is one CHECK and one constant."},
      {"name": "Absent means unchanged",                "note": "An update names only what it changes. Full replacement would make the caller resend fields it is not touching, and the day it resent a stale copy of a long draft the edit would revert work in silence. Three optional fields is not a patch language."},
      {"name": "Delete is soft, with a reason of its own","note": "Content acquires links: a published post is referenced from outside this system, and a conversation that worked on a thread for a week holds a context reference to it. A hard delete would turn both into dangling ids."},
      {"name": "Intelligence is the agent''s, state is Threads''", "note": "The domain does not rewrite a hook, judge a paragraph or decide a draft is good. That is why content is one opaque string with a length bound and no structure: a domain that modelled a hook would be a domain with an opinion about writing."},
      {"name": "No screen is a decision",               "note": "Recorded as a limitation above and stated in the module description. The absence is scope, and describing it as debt would be the release history disagreeing with the decision that produced it."}
    ]'::jsonb,

    '[
      {"text": "One migration in this module''s timeline, creating schema threads and one table. Six timelines now exist, each with its own version table.", "ref": "migrations/threads"},
      {"text": "The listing orders by updated_at rather than created_at: content work is returned to, so the thread touched an hour ago is the one being asked about."},
      {"text": "A listing row carries at most 160 characters of content and declares when it cut. Three drafts of the same piece are told apart by their text, and an excerpt that did not declare itself would be indistinguishable from a very short thread."},
      {"text": "The module registers no HTTP routes and the composition root calls no Register for it — the first module wired that way, deliberately.", "ref": "cmd/corsi/main.go"}
    ]'::jsonb,

    '[
      {"label": "Foundation record",    "path": "docs/implementation/THREADS-CONTENT-AGENT-FOUNDATION.md"},
      {"label": "Modular architecture", "path": "docs/architecture/target-modular-architecture.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;

/* ── Agents 1.3.0 ────────────────────────────────────────────────────── */

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.3.0', 1, 3, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-23 12:00:00+00',
    TIMESTAMPTZ '2026-08-23 12:00:00+00',

    'The reference and hydration machinery introduced in 1.2.0 takes a second provider, and '
    || 'the cost of that is the measure of whether it generalised: one line in the '
    || 'composition root, no change to any interface, no change to the registry. MINOR '
    || 'rather than PATCH because the build gained a reference type and a second module '
    || 'became operable by an agent — additive, with nothing existing altered.',

    '[
      {"name": "A second reference provider",     "note": "threads.thread is registered beside job_radar.opportunity. The registry, the resolver interface and the hydrator interface were unchanged: the provider arrives through the seam 1.2.0 built, wired at the composition root, and Agents still cannot name either module."},
      {"name": "Hydration of a second entity",    "note": "The per_turn policy now carries a provider whose state is title, status and the current text. The mechanism proved it can serve an entity whose most valuable field is long and rewritten several times inside one conversation, not only a short one that moves weekly."},
      {"name": "Hydration stays grant-gated",     "note": "The new type names threads.thread.get as its capability, checked by the same function against the same grant set the tool executor uses. A provider cannot opt out by declining to name one, and the registry refuses at start-up a policy that names none."},
      {"name": "Content Agent as configuration",  "note": "A row created through the ordinary agent infrastructure: instructions, a model and authorized tools. No code path, no branch and no special case exists for it."}
    ]'::jsonb,

    '[
      {"name": "Backend gate",             "note": "make -C backend ci green. 807 Go test functions, 236 of them chat integration tests, 17 migrations in the chat timeline."},
      {"name": "Frontend gate",            "note": "291 tests, build clean, lint with no errors."},
      {"name": "Freshness, live",          "note": "In one conversation: the model quoted a draft and its status, the entity was rewritten outside the conversation in SQL, and the next turn answered with the new text and the new status — with the model''s own earlier message still in the history and zero tool calls in the whole thread."},
      {"name": "Authorization, live",      "note": "With threads.thread.get revoked and the reference intact, the model reported that it could not verify rather than repeating the status it had stated twice."},
      {"name": "An unrelated grant is not the grant", "note": "An agent holding list, create, update and delete but not get receives no hydrated state: the policy names one capability and it is the one whose read this is."},
      {"name": "Nothing is written back",  "note": "The conversation''s context_references column was read straight from Postgres after two hydrated turns: identity only, no status word and no content."},
      {"name": "A reference grants nothing","note": "An agent with the reference and no grant received the subject''s title — which the user gave it by attaching it — and no word of the text; the tool call refused with tool_not_authorized through the same door."},
      {"name": "Admission is workspace-scoped", "note": "Over real HTTP, another workspace''s thread, a fabricated id and a soft-deleted thread produced the identical context_reference_not_found."},
      {"name": "Reference after delete",   "note": "A conversation whose thread was removed still opens, keeps its historical label and marks the reference unavailable."},
      {"name": "Grants are per capability, live", "note": "The existing Scout agent sees the five new capabilities in its catalogue and holds none of them; the Content agent holds those five and no GitHub or Job Radar capability."}
    ]'::jsonb,

    '[
      {"name": "Two reference types",           "note": "job_radar.opportunity and threads.thread. A third costs the same one line."},
      {"name": "Hydrated text is bounded",      "note": "Above 4000 characters the content is DESCRIBED rather than carried, because a truncated draft is worse than no draft: the model would rewrite from it and return a shortened piece believing it was complete. The bound is the provider''s declaration, not a rule Agents imposes."},
      {"name": "One read per subject per turn", "note": "Unchanged from 1.2.0 and capped at eight subjects. Batching would be additive."},
      {"name": "A turn narrowed by @ does not hydrate", "note": "Unchanged from 1.2.0. Conservative by construction and visible in the context report."},
      {"name": "Hydration writes no audit row", "note": "Unchanged from 1.2.0. chat.tool_calls records what the MODEL asked to run; the context report carries the hydration instead."},
      {"name": "The frontend registers no presentation for threads.thread", "note": "presentationFor falls back to the provider namespace and a generic glyph, which is the documented behaviour for a type the browser has not been taught. Correct for a release with no frontend work in it."},
      {"name": "tool_not_authorized was not reproduced live", "note": "Agents declares only granted tools, so a real model has no path to call one it does not hold. The refusal is covered by integration tests through the real executor and turn loop; live, the agent could not reach a write it lacked and nothing was written."}
    ]'::jsonb,

    '[
      {"name": "A reference is not a capability",       "note": "Unchanged and re-proved against a second provider. Attaching a thread says what the conversation is ABOUT; reading a word of it needs a grant."},
      {"name": "Authorization is independent of reference", "note": "An agent holding the reference and not the grant stays unable to read the state, and revoking the grant stops the read on the next turn without touching the reference."},
      {"name": "Hydration does not bypass authorization","note": "It asks the same question the agent''s own tool call asks, against the same grant, in the same vocabulary."},
      {"name": "Hydration does not bypass workspace isolation", "note": "The provider is handed the workspace untouched and filters in SQL; the registry holds no database handle and no way to widen it."},
      {"name": "Hydrated state outranks a stale claim", "note": "The failure this exists for, reproduced on the entity where it costs most: a model editing from its own earlier message returns a rewrite of the OLD text and silently discards everything since."},
      {"name": "The domain''s capabilities are not Agents''", "note": "What a Thread is, which statuses it has and what update means belong to Threads 1.0.0 and are recorded there. Agents gained a provider registration and a hydration policy — recording the entity''s fields here would grow this history every time another module registers one."},
      {"name": "An agent is a row, not a branch",       "note": "Content joins Scout as configuration. A test asserts that an agent literally named Content receives no privilege from its name, which is what makes the absence of such a branch a property rather than a habit."}
    ]'::jsonb,

    '[
      {"text": "The composition root gained one line for the second provider: referenceResolvers concatenates what each module offers, and neither module is imported by Agents.", "ref": "cmd/corsi/main.go"},
      {"text": "The reference type threads.thread is two segments and the tool threads.thread.get is three, so the two vocabularies stay distinguishable — the rule 1.2.0 introduced, exercised by a second provider."},
      {"text": "A thread has no stable fact below its title, so its resolver writes no subtitle at all. The lesson is 1.2.0''s: a subtitle is written once into a column never rewritten, and Job Radar''s frozen stage word is what taught it."},
      {"text": "The Content agent holds five grants and no others. Its instructions carry role, lifecycle vocabulary and tone, and deliberately carry nothing about authorization, workspace or security — those are structural, and repeating them in a prompt would suggest the prompt is what holds them."}
    ]'::jsonb,

    '[
      {"label": "Foundation record", "path": "docs/implementation/THREADS-CONTENT-AGENT-FOUNDATION.md"},
      {"label": "Agents module",     "path": "docs/modules/agents/CLAUDE.md"},
      {"label": "Previous release",  "path": "docs/implementation/SCOUT-JOBRADAR-CHAT-CLOSURE.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
