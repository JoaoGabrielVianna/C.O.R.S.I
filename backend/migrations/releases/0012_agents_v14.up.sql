-- Agents 1.4.0: a turn can be asked what it actually did.
--
-- ── The incident ───────────────────────────────────────────────────────
-- A live financial agent answered "8 transações importadas" in a turn that
-- made ZERO tool calls, against a ledger holding zero transactions. The
-- runtime behaved correctly and wrote nothing. The person was told the
-- opposite, because the product renders assistant prose and the ABSENCE of
-- a tool call is invisible at the presentation layer.
--
-- ── What this release does and does NOT claim ──────────────────────────
-- It does NOT make a model incapable of writing a false sentence. Nothing
-- here constrains what the model says, and the transcript proving it said
-- something untrue still exists.
--
-- The guarantee is narrower and checkable: WITHOUT A CONFIRMED RECEIPT,
-- THE PRODUCT DOES NOT PRESENT A WRITE AS A CONFIRMED EXECUTION. The
-- sentence and the fact are separated, and only the fact is rendered as
-- one.
--
-- ── Why MINOR ──────────────────────────────────────────────────────────
-- The history's own convention: 1.1.0, 1.2.0 and 1.3.0 were additive
-- machinery and 1.1.1 was a fix. This adds a stored column, a status
-- value, an API field, a stream field and a UI component; it removes
-- nothing and leaves every existing behaviour in place. The one thing that
-- is not purely additive is declared as a limitation rather than glossed.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 974  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
-- 246  `grep -rhE '^func Test' internal/chat/*_integration_test.go | wc -l`
--  10  `grep -c '^func Test' internal/chat/writereceipt_integration_test.go`
--  18  `ls migrations/chat/*.up.sql | wc -l`
-- 361  frontend, `npx vitest run`
--   5  fields on a WriteExecution, read from a live receipt over HTTP

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.4.0', 1, 4, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-28 18:00:00+00',
    TIMESTAMPTZ '2026-08-28 18:00:00+00',

    'The system can now answer, for any turn, whether it changed anything — and the answer does '
    || 'not come from the model. A live financial agent reported an import that never happened, '
    || 'in a turn that called nothing, and the product had no way to contradict it. This release '
    || 'gives it one. What a model SAYS and what the runtime CONFIRMED are separated, and only '
    || 'the second is rendered as a confirmed change. It does not stop a model from writing a '
    || 'false sentence; it stops the product from treating that sentence as evidence.',

    '[
      {"name": "Tool effect, persisted",     "note": "chat.tool_calls records whether the capability was a read or a write, captured at the moment it ran. Stored rather than looked up, because the registry describes the build running NOW and a receipt is a statement about a turn that already happened: a capability whose effect changed, or which left the build, must not be able to rewrite what a past turn is reported to have done."},
      {"name": "NOT_EXECUTED",               "note": "A third status. ok and error describe calls that RAN; a call the runtime refused before running it — an unauthorized capability, arguments that did not validate — is neither. It did not fail at its job, it never got one, and collapsing the two made \"was this attempted?\" unanswerable."},
      {"name": "WriteExecution",             "note": "One write capability''s outcome: REQUESTED, EXECUTED, FAILED or NOT_EXECUTED, with the tool call id, the capability name, when it happened and how long it took."},
      {"name": "WriteReceipt",               "note": "One assistant turn''s answer to \"did anything change?\". Carries the turn''s write executions and the counts of executed, failed and refused."},
      {"name": "WriteReceipt.Confirmed()",   "note": "The single predicate a surface may route through to say a change happened. It reads executed > 0, and the model''s sentence is not one of its inputs."},
      {"name": "Receipts on the transcript", "note": "GET /chat/conversations/{id}/messages returns write_receipts, one entry per assistant turn INCLUDING the turns where nothing ran. A missing key would be indistinguishable from a transcript nobody asked about, and a client would have nothing to render against a turn that only claimed a change."},
      {"name": "Receipt on the done frame",  "note": "The stream carries the finished turn''s receipt, so a live client never infers from the prose it just rendered whether anything changed. Built from the same audit rows the transcript reads, so the live view and a reload cannot disagree."},
      {"name": "One status vocabulary",      "note": "The tool frame now carries the call''s own status rather than deriving one from whether a failure was attached. A refusal read error on the stream and not_executed in the audit; they were two records of one event, disagreeing about it."},
      {"name": "WriteReceiptStrip",          "note": "Renders the receipt and nothing else. The component does not receive the message text, so there is no path through it by which prose can produce a confirmation. A turn that changed nothing renders nothing, and that absence is the contradiction."},
      {"name": "Redaction-compatible by construction", "note": "A receipt carries no arguments and no result — the query does not select them — so a Confidential capability produces exactly the same receipt as any other. It says what ran and how it ended, never what it touched."}
    ]'::jsonb,

    '[
      {"label": "The incident, reproduced",   "value": "Deterministically: a turn of pure prose carrying the sentence from the live failure produces a receipt with executed == 0 and an empty writes list."},
      {"label": "Backend gate",               "value": "make -C backend ci green in the project''s official parallel mode, integration suite included."},
      {"label": "Go test functions",          "value": "974 overall; 246 chat integration tests, 10 of them written for receipts."},
      {"label": "Frontend gate",              "value": "361 tests, six asserting that nothing is presented as confirmed without a receipt; build clean, tsc -b clean, lint with no errors."},
      {"label": "Four states proved",         "value": "A write that ran is EXECUTED; one that ran and failed is FAILED; one refused before running is NOT_EXECUTED with its error code; a turn with no call at all has an explicit empty receipt."},
      {"label": "Confidential intact",        "value": "A real finance.import.commit audit row carries null arguments and null result with redacted set, and its receipt exposes exactly five fields: tool_call_id, capability, status, occurred_at, duration_ms. No amount, description, category or content appears in it."},
      {"label": "Reads produce no receipt",   "value": "A read capability that ran leaves the turn''s receipt empty: a listing is not a change."},
      {"label": "Historical rows",            "value": "A row carrying the backfilled effect is never presented as a confirmed write, while remaining fully readable in the audit — verified against a real pre-migration finance.import.commit still present in the development database."},
      {"label": "Isolation",                  "value": "Receipts do not cross conversations, and another workspace cannot read the transcript they belong to."},
      {"label": "Stream and audit agree",     "value": "A refusal now reports the same status on the live frame and in the recorded row, asserted by comparing the two in one test."}
    ]'::jsonb,

    '[
      {"text": "This does not prevent a model from producing a false textual claim. Nothing here constrains what it writes. The guarantee is that the product does not use that prose as objective confirmation of a write, and a turn with no confirmed receipt is never rendered as a confirmed execution."},
      {"text": "A refusal that used to read error on the /tool-calls wire and on the tool stream frame now reads not_executed. Every consumer in this build tests for equality with ok and is unaffected, and all three client types were widened in the same change — but the value did change, and code testing for the literal \"error\" would miss a refusal."},
      {"text": "Rows recorded before this release carry the backfilled effect of read. The effect of a past call is genuinely unknown, and read is the value that claims the least: the consequence is that no historical turn can be presented as a confirmed write, which is the safe direction to be wrong in."},
      {"text": "The down migration collapses not_executed back into error before restoring the narrower CHECK. Reverting destroys the distinction between \"was stopped\" and \"broke\". It is declared, and it happens on the way down where losing it is the point of the operation rather than a surprise inside it.", "ref": "migrations/chat/0018_tool_call_effect.down.sql"},
      {"text": "A turn that changed nothing renders nothing, rather than an explicit denial. The asymmetry is deliberate — a badge on every answer becomes noise people stop reading — but it means the contradiction of a false claim is an absence, and a surface wanting to be louder must read executed itself."},
      {"text": "Confidential capabilities still leave no arguments or result in the audit, and the cross-turn evidence replay is built from those same rows. A model therefore still cannot see what it observed a turn ago in the same conversation. Finance works around it inside its own boundary. The collision is open and belongs to this module, and it is deliberately not addressed here."}
    ]'::jsonb,

    '[
      {"text": "NO TOOL RECEIPT, NO EXECUTION CLAIM. Any surface that tells a person something changed reads the receipt; the model''s sentence is not one of its inputs. Stated as a presentation and runtime invariant rather than as advice."},
      {"text": "Effect is a fact about the run, not a lookup at read time. The same reason the release history itself is not derived from the repository: a record of what happened must not move when the code does."},
      {"text": "Refused and failed are different states, because a receipt has to distinguish an agent that was correctly stopped from one that broke. What the MODEL is told is unchanged — both still arrive as the same shape of tool failure — so no prompt behaviour moved with this."},
      {"text": "The receipt carries no payload at all. That is what lets it be rendered, logged and returned without depending on redaction having been done correctly somewhere else — and the capabilities that most need a receipt are exactly the ones whose payload is withheld."},
      {"text": "Every assistant turn gets a receipt, including the empty ones. \"Nothing was executed\" is a statement; an absent key is not, and a client cannot render an absence it was never handed."},
      {"text": "Prompting a model not to claim what it did not do remains an additional defence and was never a sufficient one. There is a live transcript of it failing, which is why this release is a mechanism rather than a sentence in an instruction."}
    ]'::jsonb,

    '[
      {"text": "One migration in the chat timeline: an effect column with a read backfill, a widened status CHECK, and a partial index over write calls for the receipt query. Eighteen migrations now.", "ref": "migrations/chat/0018_tool_call_effect.up.sql"},
      {"text": "The receipt query selects neither arguments nor result, so redaction is not something it has to remember to do.", "ref": "internal/chat/adapters/repo/toolcalls.go"},
      {"text": "The done frame resolves its receipt through a function on the sink rather than a service handle, so the SSE adapter stays unaware of the application layer as every other frame already is.", "ref": "internal/chat/adapters/httpapi/stream.go"},
      {"text": "A guard test now fails if any file inside migrations/ parses as a migration without ending in .sql. It exists because a prepared but unauthorized release was parked as 0012_agents_v14.up.sql.candidate, and golang-migrate read the direction as up and the extension as sql.candidate: the gate''s disposable database published an Agents release nobody had approved, and the development database hid it because migrations had already run.", "ref": "internal/platform/preflight/migrationfiles_test.go"}
    ]'::jsonb,

    '[
      {"label": "Agents module",         "path": "docs/modules/agents/CLAUDE.md"},
      {"label": "Implementation record", "path": "docs/implementation/FINANCE-STATEMENT-IMPORT.md"},
      {"label": "First consumer",        "path": "docs/modules/finance/README.md"},
      {"label": "Previous release",      "path": "docs/implementation/THREADS-CONTENT-AGENT-FOUNDATION.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
