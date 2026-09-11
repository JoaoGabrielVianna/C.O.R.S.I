-- Agents v1.1.0, recorded and published.
--
-- ── Why a migration and not the publish endpoint ───────────────────────
-- Same reason 0002 and 0005 give: a release history has to be true on
-- every install of this software — production, a fresh clone, a restored
-- backup — and a migration is the only mechanism here that carries a
-- historical fact to all of them. The runtime publish flow stays the way a
-- release is published from the UI.
--
-- Unlike v1.0.0, this row arrives already published. That release was
-- drafted before its decision existed and waited three migrations for it;
-- this decision was taken on the evidence recorded below, on the same day,
-- so splitting it across two migrations would invent a gap that did not
-- happen.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 226  `grep -hE '^func Test' internal/chat/*_integration_test.go | wc -l`
-- 622  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--  40  `grep -oE 'r\.(Get|Post|Put|Patch|Delete)\(' internal/chat/adapters/httpapi/*.go | wc -l`
--  17  `ls migrations/chat/*.up.sql | wc -l`
--   7  the GitHub tools in internal/integrations/github/tools/tools.go
-- 163  frontend, `npx vitest run`
-- Costs and token counts come from provider usage frames on real turns,
-- read back out of chat.messages. Nothing here was inferred from prose.
--
-- ── What is deliberately NOT claimed ───────────────────────────────────
-- Autonomous investigation. It was designed, implemented as the smallest
-- possible change, and measured against two models on the real gateway. It
-- did not work. It is in `limitations`, not in `capabilities`, because a
-- release history that records intentions is a release history nobody can
-- use to answer "what could this version actually do?".

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.1.0', 1, 1, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-16 00:00:00+00',
    TIMESTAMPTZ '2026-08-16 00:00:00+00',

    'The release that gives Agents its first real external capability and teaches a '
    || 'conversation to remember what it observed. GitHub arrives read-only behind a '
    || 'per-repository authorization; a turn can be scoped with @; what a tool returned '
    || 'survives into the turns that follow; and the development database can no longer '
    || 'be destroyed by a misaimed test run.',

    -- Capabilities: what a person can do with this version, and where the
    -- claim stops. Every one has code, a test, and — for the four that
    -- touch a model — a transcript from the real gateway.
    '[
      {"name": "GitHub, read-only",        "note": "Seven read capabilities over repositories the operator authorized one by one. The token is sealed with AES-256-GCM and never leaves the server; no write verb exists in the API port to reach."},
      {"name": "Repository discovery",     "note": "github.repository.list returns language, size, last push, default branch, visibility and description as one compact line each. Thirty-two repositories cost fewer characters than the four-field version did."},
      {"name": "Capability selection (@)", "note": "The composer attaches capabilities to the next turn. Selection only narrows what may run: it can never grant, and the backend resolves the scope against the grants rather than trusting the client."},
      {"name": "Command menu (/)",         "note": "Slash commands execute immediately, which is the semantic that distinguishes them from @."},
      {"name": "Memory consolidation",     "note": "/lembrar proposes candidate memories from the conversation as it stood when the user asked, for review before anything is saved."},
      {"name": "Evidence continuity",      "note": "What a tool returned on an earlier turn of the same conversation is replayed into later ones, bounded, deduplicated and read from the audit trail. It never crosses a conversation or a workspace, and it never becomes a memory."},
      {"name": "Grounding policy",         "note": "A platform instruction, sent only to turns that have capabilities, about keeping observation apart from inference. It names no vendor, no integration and no tool."},
      {"name": "Effect on the wire",       "note": "A capability that changes something now declares that to the model. Reads declare exactly what their author wrote."},
      {"name": "Context Inspector",        "note": "Per-turn accounting of every block that reached the model, including replayed evidence and what was excluded, truncated, superseded or refused."},
      {"name": "Database safety guard",    "note": "The integration suites refuse to reset a database that does not carry a marker only a throwaway provisioner writes. The development database has never had it."}
    ]'::jsonb,

    -- Evidence: what was measured, on what, and what it said.
    '[
      {"name": "Backend gate",             "note": "make -C backend ci green, including the integration suite against a disposable Postgres. 622 Go tests, 226 of them chat integration tests."},
      {"name": "Frontend gate",            "note": "163 tests, build and lint clean."},
      {"name": "Evidence continuity",      "note": "19 unit tests and 18 integration tests covering cross-conversation and cross-workspace isolation, failed calls, bounds, truncation, revocation, budget and audit-trail neutrality. 14 semantic mutations, 14 killed."},
      {"name": "Database safety guard",    "note": "13 tests. The original incident was reproduced against a throwaway database: with the guard removed the suite silently replaced a real provider row; with it in place the run refuses and changes nothing. Verified read-only against the live development database."},
      {"name": "Live discovery",           "note": "claude-haiku-4-5 and claude-sonnet-4-6 both called github.repository.list unprompted and answered from the real result. Tool payload 3.062 characters for 32 repositories."},
      {"name": "Live evidence replay",     "note": "A follow-up turn carried 3.733 characters (~934 estimated tokens) of replayed evidence. Both models cited facts present only in that block."},
      {"name": "Live no-op check",         "note": "Asked something the replayed evidence already answered, both models made zero tool calls and answered correctly."},
      {"name": "Cost, observed",           "note": "Discovery US$ 0,0117 on haiku and US$ 0,0455 on sonnet. A follow-up answered from replayed evidence: US$ 0,0068 and US$ 0,0190."}
    ]'::jsonb,

    -- Limitations: what this version does not do, stated plainly enough to
    -- be acted on.
    '[
      {"name": "No autonomous investigation", "note": "Asked to judge something its metadata cannot settle, the agent recognises the gap and offers to investigate instead of investigating. Measured on both models, before and after a policy change written for exactly this. The user has to answer sim before it looks."},
      {"name": "Grounding is model-dependent","note": "claude-sonnet-4-6 refuses to opine without evidence and says so. claude-haiku-4-5 hedges but converts size and name into judgements of quality. Use a sonnet-class model for any agent with capabilities."},
      {"name": "Evidence costs input tokens", "note": "Up to ~2.000 estimated tokens per follow-up in a conversation that used tools. Zero for conversations that never did."},
      {"name": "Three tool rounds",           "note": "maxToolRounds is 3 and was reached once, by a turn explicitly told to investigate. Deliberately unchanged: no evidence yet that ordinary work needs more."},
      {"name": "Replay is not a sandbox",     "note": "Replayed tool output is framed as untrusted data that may contain instructions, and the framing is an instruction to the model rather than an enforced boundary."},
      {"name": "Guard covers the suites",     "note": "The marker is checked where each suite obtains its database. Code that connects to Postgres by another route is not covered by it."}
    ]'::jsonb,

    -- Decisions: the forks where another answer was available, and why this
    -- one was taken.
    '[
      {"name": "A marker table, not a naming rule", "note": "Every rule about the DSN string fails silently: a suffix is a name anybody can choose, localhost and 127.0.0.1 name the same database while comparing unequal, and an opt-in variable protects only its first run. The marker is a property of the database, so it does not care how it was addressed."},
      {"name": "The guard sits at the DSN, not the DROP", "note": "A check beside each destructive statement is a call a refactor deletes. Placed where the suite learns which database to use, there is no arrangement of calls that reaches a DROP without it."},
      {"name": "A refusal cannot be downgraded",    "note": "The guard takes an interface with Fatalf and no Skipf, so turning a misaimed run into a quiet skip is a compile error rather than a judgement call. A green run that tested nothing is the worst outcome available."},
      {"name": "Evidence is its own block",         "note": "Not tool messages: a tool message with no live call before it is a broken protocol exchange, and some gateways answer it as though a call were open. Not instructions either — text out of a README must never gain the standing of a platform rule."},
      {"name": "Evidence follows the history window","note": "Its scope is the turns already being replayed, so there is one window rather than two that can disagree."},
      {"name": "A sentence, not a planner",         "note": "The observed failure was that the policy told the model what to assert and never that a read it can take is not a read to offer. That is a missing rule, not a missing subsystem. Forcing tool_choice would have made the follow-up test pass by spending a tool call on every turn, including the ones already answered."},
      {"name": "The sentence did not work",         "note": "Measured on both models and reported as a limitation rather than escalated into a planner. The next decision is the owner''s."}
    ]'::jsonb,

    '[
      {"text": "Replayed evidence is read from chat.tool_calls, which already stored arguments, results, status and scope. No table, no column and no migration were added for it.", "ref": "internal/chat/app/evidence.go"},
      {"text": "The block is bounded at 8.000 characters per turn and 4.000 per result, deduplicated by tool and arguments, selected newest-first and rendered chronologically.", "ref": "TestEvidenceBudgetMatchesRenderedBlock"},
      {"text": "It is a system message of its own, never a tool message: a tool message with no live call before it is a broken protocol exchange.", "ref": "TestEvidenceIsNeverAToolProtocolMessage"},
      {"text": "Its scope is the history window the turn already replays, so evidence and history cannot disagree about which turns are in play.", "ref": "TestEvidenceLeavesWithTheTurnThatProducedIt"},
      {"text": "The tool effect the registry already recorded now reaches the model, generated from the field, so a capability registered later is covered the day it is registered.", "ref": "domain.ToolDefinition.DeclaredDescription"},
      {"text": "The safety marker lives in public, outside every schema the suites reset, so it survives the resets it authorizes.", "ref": "TestTheMarkerSurvivesTheResetItAuthorizes"},
      {"text": "Schema chat is on its own migration timeline with version table schema_migrations_chat. 17 migrations. This release added none."}
    ]'::jsonb,

    '[
      {"label": "Evidence continuity", "path": "docs/implementation/AGENTS-V11-EVIDENCE-CONTINUITY.md"},
      {"label": "Tool grounding",      "path": "docs/implementation/AGENTS-V11-TOOL-GROUNDING.md"},
      {"label": "GitHub integration",  "path": "docs/implementation/GITHUB-INTEGRATION-V1.md"},
      {"label": "Capability menu",     "path": "docs/implementation/AGENTS-V11-BATCH-04.md"},
      {"label": "Memory consolidation","path": "docs/implementation/AGENTS-V11-BATCH-02.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
