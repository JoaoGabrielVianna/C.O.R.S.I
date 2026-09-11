-- Finance 1.0.0, and the module status that finally stops being provisional.
--
-- ── Why `partial` was true and is no longer ────────────────────────────
-- 0009 defined Finance's status precisely, and used it as the contrast
-- that justified giving Threads `active`: "a module whose declared scope
-- is half built — two domain entities still living in the browser, a route
-- hidden from the navigation."
--
-- Both clauses were retired by name, not by re-reading:
--
--   the hidden route      Finance left HIDDEN_ROUTES on 2026-08-26 and is
--                         reached through the same predicate Agents and
--                         Job Radar use. No exception was added for it.
--   the second entity     FixedExpense moved to Postgres (0009 in the
--                         finance timeline) and was then generalised into
--                         recurring_entries (0010). One entity remains in
--                         the browser — Account — and that is a decision
--                         recorded as a limitation below, not a half-built
--                         part: Finance does not model balance, and an
--                         Account that names no balance names nothing the
--                         backend has an opinion about.
--
-- A status describes the module's lifecycle. The two facts that made this
-- one `partial` are gone, so leaving it there would be the release history
-- disagreeing with the schema.
--
-- ── Why `stable`, with the limitations listed below ────────────────────
-- Same rule 0008 stated and 0009 applied to a harder case: stability is
-- about the reliability of what shipped, not about how much is left to
-- build. What shipped is a ledger whose money has one representation, whose
-- clock has one source, and whose eighteen capabilities were exercised
-- against a real model. What is absent — balance, invoices, import,
-- reconciliation — was never claimed by this release.
--
-- One limitation is uncomfortable and is recorded as such rather than
-- softened: seven frontend consumers still apply their own totals
-- semantics instead of the frozen contract. It is known, it is written
-- down, and it does not move the numbers the Ledger or the backend report.
--
-- ── Why 12:00 on the declared day ──────────────────────────────────────
-- The convention 0009 set. Nothing else was published on 2026-08-26, so
-- the "which version was active on date X" index has an unambiguous answer.
--
-- ── The JSONB item shapes are not interchangeable ──────────────────────
-- Each of the six snapshot columns is decoded into a specific Go struct,
-- and a key the struct does not name is dropped in silence:
--
--   capabilities     domain.Capability  {name, note}
--   evidence         domain.Metric      {label, value}
--   limitations      domain.Note        {text, ref?}
--   decisions        domain.Note        {text, ref?}
--   technical_notes  domain.Note        {text, ref?}
--   doc_refs         domain.DocRef      {label, path}
--
-- Writing {name, note} into evidence, limitations or decisions produces a
-- row that INSERTs cleanly, passes every constraint, and renders as a list
-- of empty entries. That has already happened: of the seven releases
-- published before this one, only agents 1.0.0 uses the right shapes, and
-- the other six carry 60 items that the API returns blank. Those rows are
-- published and frozen, so they stay as they are and the defect is
-- reported rather than patched here. This migration does not add to it.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 914  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--  88  `grep -rhE '^func Test' internal/finance --include='*_test.go' | grep -v TestMain | wc -l`
--   2  of those 88 are behind the `litellm` build tag, so 86 run in the gate
--  12  `ls migrations/finance/*.up.sql | wc -l`
--   7  `ls -d migrations/*/ | wc -l`
--   6  `SELECT tablename FROM pg_tables WHERE schemaname='finance'`
--  18  capabilities, derived from tools.New() at runtime, not from grep
--   8  of them read, 10 write, 18 of 18 Confidential
--  18  `SELECT count(*) FROM chat.agent_tools JOIN chat.agents ON ... name='Ledger'`
--      — and the two sets diff to empty, so the grant list is the registry
--  17  `grep -cE '^  /' api/openapi/finance.yaml`   (27 schemas, per cmd/swagger)
-- 355  frontend, `npx vitest run` — 29 files

-- ── the module's lifecycle ─────────────────────────────────────────────
--
-- No INSERT: the finance row has existed since 0001. Only the two facts
-- that changed are written, and `position` is untouched so nothing
-- renumbers around it.
UPDATE releases.modules
   SET status      = 'active',
       description = 'Personal financial records as a ledger: transactions, categories, '
                  || 'cards, people, purchase plans and recurring entries. Operated from '
                  || 'its own screens and, since 1.0.0, by an authorized agent over the '
                  || 'same application layer.',
       updated_at  = now()
 WHERE key = 'finance';

/* ── Finance 1.0.0 ───────────────────────────────────────────────────── */

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'finance', '1.0.0', 1, 0, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-26 12:00:00+00',
    TIMESTAMPTZ '2026-08-26 12:00:00+00',

    'Finance stops being a set of screens over a database and becomes a ledger with one '
    || 'source of truth, one representation of money, one clock, and eighteen capabilities '
    || 'an authorized agent can be granted. The screens and the agent call the same '
    || 'application layer, so there is no second writer and no second idea of what a '
    || 'transaction is. The release also closes the two things that made the module '
    || '`partial`: the route is in the navigation, and the last entity that lived in the '
    || 'browser by accident now lives in Postgres — generalised, on the way, from '
    || '"fixed expense" into a recurring entry whose direction comes from its category, so '
    || 'salary and rent are one concept rather than two tables waiting to disagree.',

    '[
      {"name": "One application layer, two callers", "note": "internal/finance/tools calls app.Service — the same methods the HTTP handlers call. There is no SQL in the tools package and no way to reach any from it. A tool holding a repository would be a second writer with its own idea of what a transaction is, and the first thing it would get wrong is the one thing the composite foreign key exists to prevent: an expense filed under an income category."},
      {"name": "Eighteen capabilities",             "note": "8 read and 10 write, derived from tools.New() at runtime. transaction list/get/create/update/delete; category list/create/update; card list/update; person list/create/update; recurring_entry list/create/update/summary; summary.get."},
      {"name": "Deny-by-default authorization",     "note": "The absence of a chat.agent_tools row IS the denial. Grants are re-read every turn, so a revoke takes effect on the next one. There is no `if agent.name == \"Ledger\"` anywhere in the repository."},
      {"name": "All eighteen Confidential",         "note": "ToolDefinition.Confidential is true on every one, and the recorder drops arguments and result and stamps redacted. A financial audit trail that stored what was audited would be the leak it exists to prevent."},
      {"name": "Money is int64 cents, once",        "note": "finance.transactions.amount_cents is BIGINT with CHECK (> 0). The domain carries int64, the API accepts int64, the tool schemas declare integer — and TypeInteger rejects fractional JSON, so a model cannot spend 19.99. Rendering to text happens in one direction, in money.go, and nothing parses text back into cents."},
      {"name": "Direction comes from the Category", "note": "Value has no sign. `type` is derived from the referenced category on every write, in app, and can never drift from it. The DB composite foreign key is the belt around those suspenders."},
      {"name": "One clock",                         "note": "ports.Clock reads SELECT now() from Postgres and app.Service.Now exposes it. The REALIZED/PROJECTED split is computed by SQL against the database''s now(); a surface stamping occurred_at from its own process would occasionally file the present as the future, and \"quanto gastei hoje\" would answer zero with no error anywhere."},
      {"name": "Reporting periods resolved server-side", "note": "period.go resolves month, week and named periods in FINANCE_TIMEZONE, read from config and validated at boot. Nothing in a conversation turn tells the model what day it is, so nothing may ask it."},
      {"name": "RecurringEntry, not FixedExpense",  "note": "One concept for anything that repeats — rent, a subscription, the gym, and salary. Direction is the category''s, never a column. Cancelling is ends_at rather than a delete, so \"what was I paying in March\" keeps having an answer."},
      {"name": "Recurrence is never materialised",  "note": "Creating one creates no transaction, in any layer. Materialising would put values nobody confirmed into the ledger, indistinguishable from real ones after the fact."},
      {"name": "Two summaries that never add up",   "note": "finance.summary.get reports the totals contract; finance.recurring_entry.summary reports monthly income and expense separately, normalised (an annual entry contributes a twelfth). They are deliberately not summable: a recurring entry already paid appears in both."},
      {"name": "Transaction has one source of truth","note": "The UI stopped merging localStorage rows into what it aggregates, and the development seed became opt-in behind corsi.module.finance.demo. Before that, a fixture could inflate a month total while every row on screen read zero."},
      {"name": "External identity, unused on purpose","note": "external_source + external_id, both-or-neither, unique per workspace among live rows. No capability and no handler sets them. They exist so a future import can be idempotent without retrofitting a constraint onto a populated table — and the pair rather than the id alone, because two future origins can mint colliding ids."},
      {"name": "Workspace isolation in SQL",        "note": "Every operation is workspace-scoped. No tool schema has a workspace property, so a model has no argument through which to name one. person_id''s foreign key is workspace-blind at the DB level, so the service resolves the person in the same workspace before insert."},
      {"name": "Audit through Chat",                "note": "Executions land in chat.tool_calls with tool, status, round, duration and conversation — and, for these eighteen, without arguments or result. No second audit log was built."},
      {"name": "Context reference and hydration",   "note": "finance.transaction is a reference type with per-turn hydration, gated on the finance.transaction.get grant. Attaching a transaction says what the conversation is about; reading it needs the grant."},
      {"name": "Finance in the navigation",         "note": "Removed from HIDDEN_ROUTES and reached through the same isRouteHidden predicate Agents and Job Radar use — sidebar and command palette both. No per-module exception was added and no navigation item was duplicated."}
    ]'::jsonb,

    '[
      {"label": "Backend gate",              "value": "make -C backend ci green in the project''s official parallel mode, integration suite included, exit 0 twice."},
      {"label": "Go test functions",         "value": "914 overall; 88 of them Finance''s, of which 86 run in the gate and 2 are behind the litellm build tag."},
      {"label": "Frontend gate",             "value": "355 tests across 29 files, build clean, lint with no errors, tsc -b clean."},
      {"label": "Semantics proved live",     "value": "Against a real model: a hypothesis and a stated intention wrote nothing, criticism deleted nothing, a correction altered the same row rather than adding a second, and an ambiguous request asked instead of writing."},
      {"label": "Money precision",           "value": "Amounts read back from Postgres as integers rather than through any rendering path, so a formatting round trip could not hide a lost cent."},
      {"label": "Two clocks, caught twice",  "value": "The first occurrence made a just-recorded purchase PROJECTED with roughly 400ms of skew; the second appeared in CreateRecurringEntry as time.Now() and was caught by the tests written for the first."},
      {"label": "Installment write",         "value": "Mutation-tested: reverting editWritesToBackend to the old planId condition makes the new tests fail. The bug it fixes reported success for an edit that was never written."},
      {"label": "Audit redaction",           "value": "chat.tool_calls rows for finance.* carry null arguments and null result with redacted set, read straight from the table rather than through an API that could be filtering."},
      {"label": "Deny-by-default",           "value": "The diff between the 18 capabilities derived from tools.New() at runtime and Ledger''s 18 granted tool names is empty. Other agents see the eighteen and hold none."},
      {"label": "Plan installment invariant","value": "Uniqueness of (plan_id, installment_number) was documented in code and unenforced; verified zero violations, then enforced by a partial unique index."},
      {"label": "Fixture hygiene",           "value": "Every exercise ran in isolated fixture workspaces against fixture data; 0 remain. No personal financial record was used for a destructive test."}
    ]'::jsonb,

    '[
      {"text": "No balance, anywhere. Finance records flow. The net of a period is not what the operator has, and nothing in the domain claims otherwise \u2014 which is the reason Account is not a backend entity."},
      {"text": "Account lives in the browser, by decision rather than omission. It does not represent a balance and Finance does not model balance, so moving it to Postgres would persist a label with no invariant attached to it."},
      {"text": "account_id on transactions is misnamed: an unconstrained uuid that carries a Card id. The correction is deliberately deferred and nothing new may be built on it. It is exposed to no capability, so an agent handed that id could not resolve it anywhere.", "ref": "migrations/finance"},
      {"text": "Seven frontend consumers still apply their own totals semantics instead of the frozen contract. Known, written down and outside the MVP \u2014 and it does not move what the backend or the Ledger report, which read the contract.", "ref": "backend/docs/totals-contract.md"},
      {"text": "No invoices and no statement periods. A card has no closing day, no due day and no invoice; \"what is on the card this month\" is answered by the transactions, not by a statement the domain does not have."},
      {"text": "No import and no bulk write. external_source and external_id are ready and unused: no import capability, no bulk endpoint, and no heuristic deduplication \u2014 a financial fingerprint that guessed would merge two real purchases of the same amount on the same day."},
      {"text": "No reconciliation. Nothing compares the ledger against an external source of record."},
      {"text": "Transfers and purchase plans are not agent-operable. Both exist in the backend and on the screens; neither has a capability. A transfer is two legs that must be created and removed together and a plan writes a series \u2014 write shapes that deserve their own authorization design first."},
      {"text": "An agent cannot delete a category, a person or a card. The soft delete has no in-use guard, so removing a category would leave history unlabelled. transaction.delete is the only destructive capability granted."},
      {"text": "Soft-deleted rows accumulate. Removal is reversible by design and nothing purges \u2014 harmless at this size, unbounded in principle. The same state as Job Radar and Threads."},
      {"text": "Identity is absent platform-wide. The workspace middleware accepts any syntactically valid UUID without validating it and the frontend sends a public constant; the upstream gateway the code presupposes does not exist. Finance inherits this rather than introducing it, and it is why the operator is single."}
    ]'::jsonb,

    '[
      {"text": "The tools call the application layer, never SQL and never localhost HTTP. The rules a write must pass live there: the category is resolved and the type derived there, the person is checked against the workspace there, the domain validates there, the transfer-pair rule is enforced there. Any other entry point would be a second writer.", "ref": "internal/finance/tools"},
      {"text": "One representation of money and no second one: int64 cents from the schema to the tool schema. No float, no parseable monetary string, no conversion of text into cents. The rendering in money.go is one-directional by construction."},
      {"text": "Direction is the category''s, never a column \u2014 for transactions and recurring entries alike. Storing it would let a row disagree with its own category, and the disagreement would be invisible until a total was wrong."},
      {"text": "The clock is the database''s, because the classification it feeds is computed there. A second clock is not a rounding difference; it is a record filed on the wrong side of now."},
      {"text": "Recurrence is a template and a transaction is money that moved. Creating one never creates the other, and the two summaries are never added. Cancelling is ends_at, so the past stays answerable."},
      {"text": "FixedExpense was generalised rather than paired with a FixedIncome. Salary and rent are the same entity and only the category differs; a parallel income table would have duplicated the lifecycle, the frequency vocabulary and the summary, and then drifted."},
      {"text": "No aliases were kept for the renamed capabilities. No published release depended on finance.fixed_expense.*, so compatibility would have been owed to nobody, and the Ledger''s grants were migrated explicitly in the same change rather than left to resolve by name."},
      {"text": "The rename is a rename, not a drop and recreate: ALTER TABLE ... RENAME with the enum types, indexes and constraints renamed by name. A drop-and-recreate on a populated table would have been data loss dressed as a migration.", "ref": "migrations/finance/0010_recurring_entries.up.sql"},
      {"text": "External identity is a pair rather than an id: external_source + external_id, both-or-neither, unique among live rows per workspace. Two future origins can mint colliding ids, and a unique index on the id alone would refuse a legitimate second source''s row."},
      {"text": "No ritual confirmation, but material ambiguity is resolved. The agent does not ask permission to do what it was asked to do; it asks when the answer would change what gets written, and it does not invent an establishment, a date, a category or a recurrence."},
      {"text": "Audit privacy was decided before stable, not after. Confidential is a property of the tool definition rather than a filter in the recorder, so a capability declares its own sensitivity and a future finance tool cannot forget to be redacted by being added somewhere the filter does not look."},
      {"text": "Transaction has exactly one source of truth: the backend. The UI aggregates nothing the Ledger cannot also read, and the development fixture is opt-in \u2014 a seed that runs by default is a fixture that eventually gets reported as a number."},
      {"text": "account_id stays wrong on purpose for now. Renaming a column carrying live Card ids is a migration with a real failure mode and no urgency behind it: recorded as a limitation, closed to new construction, and invisible to every capability."}
    ]'::jsonb,

    '[
      {"text": "Twelve migrations in the finance timeline, which is the platform default schema_migrations. Seven timelines now exist, each naming its own version table — omitting -table makes the runner read finance''s and conclude there is nothing to apply.", "ref": "migrations/finance"},
      {"text": "Six tables: transactions, categories, cards, persons, purchase_plans, recurring_entries."},
      {"text": "The tool schemas are flat objects of scalar properties. Nesting would be a shape a model has to assemble correctly before authorization is even consulted."},
      {"text": "Every tool output is a flat map of semantic fields — an id, a word, an integer of cents, a date, a rendered amount. No markdown, no card, no colour, no component name: the same result draws a table on the web and a line in a text channel.", "ref": "internal/finance/tools/tools.go"},
      {"text": "FINANCE_TIMEZONE is loaded with time.LoadLocation at boot and an invalid name fails the boot rather than silently falling back to UTC.", "ref": "cmd/corsi/main.go"},
      {"text": "The HTTP surface is 34 route registrations over 17 documented OpenAPI paths and 27 schemas.", "ref": "api/openapi/finance.yaml"},
      {"text": "The fix that made installment edits reach the backend is a named predicate rather than an inline condition, because the failure it caused was silent success.", "ref": "frontend/src/modules/finance/api/transactions.ts"}
    ]'::jsonb,

    '[
      {"label": "Agent tools foundation", "path": "docs/implementation/FINANCE-AGENT-TOOLS-FOUNDATION.md"},
      {"label": "Domain coverage",        "path": "docs/implementation/FINANCE-LEDGER-DOMAIN-COVERAGE.md"},
      {"label": "Data consistency",       "path": "docs/implementation/FINANCE-DATA-CONSISTENCY.md"},
      {"label": "Totals contract",        "path": "backend/docs/totals-contract.md"},
      {"label": "Finance module",         "path": "docs/modules/finance/README.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
