-- The first history: the three things this installation actually versions,
-- and the one release that has a snapshot worth recording.
--
-- ── Why a migration and not a Go seeder ────────────────────────────────
-- A release row is a historical fact, and a migration is the only place in
-- this codebase where "this became true at this point in the timeline" is
-- already the semantics. It is reproducible: a fresh database and a
-- production database converge on the same rows, and `ON CONFLICT DO
-- NOTHING` means re-running it never overwrites a row an operator has
-- since published.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 123  `grep -hE '^func Test' internal/chat/*_integration_test.go | wc -l`
--  38  `grep -oE 'r\.(Get|Post|Put|Patch|Delete)\(' internal/chat/adapters/httpapi/*.go | wc -l`
--  12  `ls migrations/chat/*.up.sql | wc -l`
--   8  `grep -hoE 'CREATE TABLE chat\.[a-z_]+' migrations/chat/*.up.sql | sort -u | wc -l`
-- 8/8  docs/modules/agents/backlog.md §"Definition of Done"
--
-- Nothing here was copied from a prompt or inferred from prose.

INSERT INTO releases.modules (key, name, description, status, position) VALUES
    (
        'agents',
        'Agents',
        'Persistent AI agents: conversations, memory, sources, tools and the accounting behind every turn.',
        'active',
        10
    ),
    (
        'finance',
        'Finance',
        'Personal financial records: transactions, categories, cards, people and purchase plans.',
        'partial',
        20
    ),
    (
        'job-radar',
        'Job Radar',
        'Career pipeline prototype. Frozen: it is preserved as-is and receives no active work.',
        'frozen',
        30
    )
ON CONFLICT (key) DO NOTHING;

-- ── Agents v1.0.0 — recorded as a DRAFT, deliberately ──────────────────
--
-- The module is functionally complete (Definition of Done 8/8) and the
-- code is in production. The release is NOT declared: as of this
-- migration, docs/implementation/AGENTS-V1-RELEASE-CLOSURE.md ends in
-- `AGENTS v1.0.0 — NOT READY`, with one open blocker that is an owner
-- configuration, not code.
--
-- Publishing is a decision, and it is the owner's. Seeding this row as
-- `published` with a date would mean inventing both — writing down a
-- release that the project's own audit trail says has not happened. The
-- snapshot is therefore recorded in full and left as a draft, ready to be
-- published by the owner through POST .../publish the moment they decide.
INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.0.0', 1, 0, 0,
    'draft', NULL, NULL,

    'First functionally complete release of Agents. The module carries a conversation '
    || 'from prompt to persisted turn against a real LiteLLM gateway, with memory, '
    || 'sources, tools, per-agent budgets and full cost accounting.',

    -- Capabilities: what a person can do with this version. Each one has
    -- code and a test behind it; `note` says what the limit of the claim is.
    '[
      {"name": "Conversations",      "note": "Streaming turns over SSE, with reasoning separated from content, stop and regenerate."},
      {"name": "Memory",             "note": "Facts captured from a conversation that survive it and are replayed into later turns."},
      {"name": "Brain",              "note": "Visual projection of memory provenance. Rendered from stored memories; the graph itself is not persisted."},
      {"name": "Sources",            "note": "Reference material attached to an agent and injected into its context."},
      {"name": "Context Inspector",  "note": "Per-turn view of exactly what was sent to the model, round by round."},
      {"name": "Usage & Accounting", "note": "Prompt and completion tokens, usage source and cost per turn, summed across tool rounds."},
      {"name": "Budgets",            "note": "Per-agent daily spend ceiling in UTC, enforced by a preflight before every provider call."},
      {"name": "Tools",              "note": "Authorized capabilities with a deny-by-default grant model and a persisted audit trail."},
      {"name": "LiteLLM integration","note": "OpenAI-compatible gateway client: catalogue, pricing, streaming, error translation."}
    ]'::jsonb,

    '[
      {"label": "Integration tests", "value": "123"},
      {"label": "HTTP routes",       "value": "38"},
      {"label": "Migrations",        "value": "12"},
      {"label": "Tables",            "value": "8"},
      {"label": "MVP DoD",           "value": "8/8"}
    ]'::jsonb,

    -- Known limitations, taken from the closure document and the module
    -- docs. These are the things this version does NOT do, written down
    -- while they are still true.
    '[
      {"text": "Tool calling was verified against a real gateway using deepseek/deepseek-chat, the only model the disposable test key could reach. The production model was not exercised."},
      {"text": "The external circuit breaker is unset: max_budget on the production LiteLLM virtual key is not configured. Spend enforcement rests entirely on the in-product budget."},
      {"text": "Budget overshoot of up to one provider call is possible, and concurrent turns are not serialized against the same ceiling."},
      {"text": "The frontend has no test harness, so none of the Agents UI has automated regression."},
      {"text": "The 38 routes are not covered by any OpenAPI specification."},
      {"text": "Backend error messages reach the Portuguese interface in English."},
      {"text": "Workspace isolation partitions data but does not authenticate: any syntactically valid UUID is accepted. Identity does not exist on the platform."}
    ]'::jsonb,

    -- Decisions: approved and documented, each traceable to a document or
    -- a test that holds it in place. No decision was invented here.
    '[
      {"text": "The product domain is called Agents; the implementation namespace stays chat. No rename is authorized for aesthetic reasons.", "ref": "ADR-02"},
      {"text": "Budget enforcement belongs to C.O.R.S.I. The local gate never consults the gateway budget, and the two layers stay separate.", "ref": "TestLocalBudgetNeverConsultsTheGatewayBudget"},
      {"text": "The tool registry is code, not a table. There is no tool-definitions schema, because a tool ships with the binary.", "ref": "migrations/chat/0012_tools"},
      {"text": "Tools are deny-by-default, and a grant is the presence of a row. Revoking deletes it.", "ref": "migrations/chat/0012_tools"},
      {"text": "system.echo is a diagnostic, not a product capability: it is absent from the production registry and available only where internal tools are explicitly enabled.", "ref": "Release Closure GATE 6"},
      {"text": "Tool names cross the wire with dots encoded as double underscores. The gateway rejects a dotted function name, so the encoding is load bearing.", "ref": "TestLiveToolNameEncoding"}
    ]'::jsonb,

    '[
      {"text": "Schema chat, on its own migration timeline with version table schema_migrations_chat. 12 migrations, 8 tables."},
      {"text": "The LiteLLM adapter lives inside the bounded context as the driven adapter of ports.LLM. This is a known departure from the target Integrations boundary and is deliberately not corrected in this version."},
      {"text": "External boundary verified twice against a real gateway: the conversation flow (X1) and tool calling end to end, including a second provider round carrying the tool result."},
      {"text": "Provider credentials are sealed with AES-256-GCM before storage and never returned; responses carry only api_key_hint."}
    ]'::jsonb,

    '[
      {"label": "Release Closure", "path": "docs/implementation/AGENTS-V1-RELEASE-CLOSURE.md"},
      {"label": "LiteLLM X1",      "path": "docs/implementation/AGENTS-V1-X1-LITELLM.md"},
      {"label": "Module docs",     "path": "docs/modules/agents/README.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
