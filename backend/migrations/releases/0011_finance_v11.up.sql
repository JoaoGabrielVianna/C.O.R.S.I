-- Finance 1.1.0: a statement becomes a conversation, and a claim becomes a
-- receipt.
--
-- ── Scope, and what deliberately is NOT in it ──────────────────────────
-- Everything below is Finance's: staging, parsing, identity, the import
-- capabilities. The write-receipt machinery that made the flow trustworthy
-- is AGENTS' work — a persisted tool effect, a NOT_EXECUTED status, a
-- receipt on the transcript and on the stream — and it is recorded in the
-- Agents history, not here. Finance is a consumer of it.
--
-- Attributing another module's mechanism to this one is how a release
-- history stops being able to answer "what did this module ship".
--
-- ── The JSONB item shapes are not interchangeable ──────────────────────
--   capabilities     {name, note}
--   evidence         {label, value}
--   limitations      {text, ref?}
--   decisions        {text, ref?}
--   technical_notes  {text, ref?}
--   doc_refs         {label, path}
--
-- A key the struct does not name is dropped in silence. Writing {name,
-- note} into evidence produces a row that INSERTs cleanly, passes every
-- constraint, and renders as a list of empty entries — which is exactly
-- what happened to six earlier releases and 60 items. This snapshot was
-- validated field by field against the decoder before it was applied.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 970  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--  35  `grep -c '^func Test' internal/finance/tools/import_integration_test.go`
--  13  `grep -c '^func Test' internal/finance/app/statementparse_test.go`
--  14  `ls migrations/finance/*.up.sql | wc -l`
--  23  capabilities, from tools.New() at runtime, diffed empty against the
--      Ledger's 23 grants
--   9  read, 14 write, 23 of 23 Confidential
-- 361  frontend, `npx vitest run`

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'finance', '1.1.0', 1, 1, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-28 12:00:00+00',
    TIMESTAMPTZ '2026-08-28 12:00:00+00',

    'Finance learns to read a statement. A pasted CSV is parsed by a deterministic parser, '
    || 'staged, classified line by line and reported back as arithmetic that has to balance — '
    || 'and nothing enters the ledger until the operator says so. MINOR rather than MAJOR '
    || 'because every capability, table and contract from 1.0.0 is untouched: two tables, one '
    || 'entity and five capabilities were added beside them. '
    || 'The release is shaped by two defects found in live conversation rather than in tests. '
    || 'The identity a statement is deduplicated under used to be free text a model supplied, '
    || 'and a model spelled the same card two ways in consecutive turns; it is now an id this '
    || 'database issues. And an agent reported an import that never happened, which is why '
    || 'confirmation now comes from an execution receipt rather than from a sentence.',

    '[
      {"name": "finance.import.prepare",        "note": "Write. Parses a pasted CSV, classifies every line and stages it. Creates NO transaction: no total moves and no financial read returns anything different. It is declared write because ToolEffect defines read as \"running it twice changes nothing\", and this creates a batch — declaring it read would have been convenient and false."},
      {"name": "finance.import.resolve_group",  "note": "Write. Assigns an operator-chosen category to every unclassified line sharing a normalised description, so 150 lines are resolved in a handful of decisions. Idempotent: repeating a classification already made is success, because answering it with a failure once taught a live model that its batch had vanished."},
      {"name": "finance.import.commit",         "note": "Write. Materialises the ready lines in ONE set-based statement inside one transaction. Never imports invalid, uncategorised, ambiguous-internal or unresolved ambiguous-duplicate lines."},
      {"name": "finance.import_source.list",    "note": "Read. The accounts and cards whose statements can be imported, with the id prepare needs."},
      {"name": "finance.import_source.create",  "note": "Write. Registers where statements come from. An explicit act by the operator, like creating a category, because the id it returns becomes the namespace every transaction imported through it is deduplicated against."},
      {"name": "ImportSource, a persisted identity", "note": "kind (card, bank_account, other), institution, label, optional last4, optional card_id. NOT finance.Card: a bank account has no last4 in the card sense, no limit and no closing day, and pushing one into Card would make Card mean any account. A source may point at a card; it may not be one."},
      {"name": "Backend-derived dedup namespace", "note": "external_source is import-source:<uuid>, derived from the row id. No schema property accepts it, so there is no argument through which a model can spell the account identity — and therefore none to spell inconsistently. Renaming a source cannot orphan its history, because the label is not part of the key."},
      {"name": "Strong identity",               "note": "An identifier the institution issued and stands behind, such as an OFX FITID. A match is an ANSWER: the line is skipped as a duplicate without asking."},
      {"name": "Heuristic identity",            "note": "A fingerprint derived from account, date, cents, direction, normalised description and an occurrence ordinal within the group. It is a conservative dedup strategy and NEVER proof of identity."},
      {"name": "AMBIGUOUS_DUPLICATE",           "note": "A heuristic match is a QUESTION. One coffee exported twice and two coffees the same afternoon produce identical fields, so such a line is neither imported nor discarded: the operator decides. Discarding would delete a real purchase nobody would ever miss."},
      {"name": "Occurrence ordinals",           "note": "Scoped to the group rather than to the file, which is what lets two legitimately identical purchases on one day both import, and what keeps a wider re-export from renumbering days already imported."},
      {"name": "Staging that is not the ledger","note": "finance.import_batches and finance.import_rows. A tool schema here is a flat object of scalars, so 150 normalised rows cannot cross it in either direction; the rows live in staging between parse and commit. Nothing staged appears in transaction.list, summary.get, totals or the recurring summary."},
      {"name": "Reconciliation that must balance","note": "row_count equals ready + duplicate + ambiguous_duplicate + unresolved_category + ambiguous_internal + invalid, checked before commit, which ABORTS if it does not add up. After commit, eligible_ready equals created + skipped_concurrent. No line can vanish from the count."},
      {"name": "Deterministic CSV parsing",     "note": "Header required and columns matched by NAME, not position. Decimal text becomes int64 cents by integer arithmetic with no float anywhere on the path."},
      {"name": "Batch resumption by content",   "note": "Re-preparing the same statement for the same source returns the same batch instead of stacking a new one, matched on content_sha256. Without it a model that re-derives its state each turn discards its own classifications."}
    ]'::jsonb,

    '[
      {"label": "Backend gate",            "value": "make -C backend ci green in the project''s official parallel mode, integration suite included."},
      {"label": "Go test functions",       "value": "970 overall; 35 of them import integration tests and 13 parser unit tests."},
      {"label": "Frontend gate",           "value": "361 tests, build clean, tsc -b clean, lint with no errors."},
      {"label": "Capability registry",     "value": "23 capabilities derived from tools.New() at runtime — 9 read, 14 write, 23 of 23 Confidential — diffed empty against the Ledger''s 23 grants, with 0 stale and 0 outside finance."},
      {"label": "Deny-by-default",         "value": "Five other agents in the workspace hold 0 finance capabilities; the three import capabilities appeared in the catalogue ungranted before anyone granted them."},
      {"label": "Prepare writes no ledger","value": "Proved by test and live: after a preview the transaction count is unchanged and summary.get returns exactly what it returned before."},
      {"label": "Identity, live",          "value": "The same source selected from two different conversations, described with different words, recognised the same statement as already imported and created nothing."},
      {"label": "Model text is excluded",  "value": "external_source read back from Postgres is import-source:<uuid> for every imported row; a deliberately absurd source_label does not appear in it."},
      {"label": "Two identical purchases", "value": "Two coffees of the same amount on the same day both import, and a re-export of that file adds neither."},
      {"label": "Cents, in raw SQL",       "value": "Amounts read back as integers straight from Postgres rather than through any rendering path, so a formatting round trip could not hide a lost cent."},
      {"label": "Four-surface parity",     "value": "Postgres, HTTP totals, the Ledger''s own summary and the browser all report 101885 cents of expense and 800000 of income for the same imported month."},
      {"label": "Isolation",               "value": "A source id from another workspace is refused byte-for-byte identically to a fabricated one, and duplicate detection never reaches across a workspace."}
    ]'::jsonb,

    '[
      {"text": "The transport is pasted CSV or structured text. There is no upload boundary anywhere in the chat runtime — no multipart, no file handler — so a statement arrives as an argument."},
      {"text": "No PDF and no OCR import. Without verifiable parsing a number extracted by a model enters the ledger as a fact nobody checked, and a preview does not fix that because nobody audits 150 lines on a screen."},
      {"text": "OFX is not productized. Measured, a 150-transaction OFX is 18.3 KB against a 16 KiB tool argument ceiling, so roughly 135 fit; the same statement as CSV is 5.4 KB and roughly 465 fit. The format with the better identity has the worse transport, and closing that needs the upload boundary above."},
      {"text": "Heuristic identity can require the operator to decide. Without an institutional identifier, a line that matches an imported one by date, amount and description is reported as ambiguous rather than resolved, and a description the bank rewrites between exports defeats the fingerprint entirely."},
      {"text": "No invoice entity and no invoice balance. A card statement''s purchases import as individual transactions."},
      {"text": "Card carries closing_day and due_day and nothing reads them. The fields exist; the cycle they describe is not implemented, and no statement is attributed to an invoice period.", "ref": "docs/modules/finance/current-state.md"},
      {"text": "An installment line such as \"LOJA 3/10\" imports as the fact it is, with its description preserved. It does not reconstruct a PurchasePlan: CreatePurchasePlan writes all ten at once from a total, and importing ten when the statement shows the third would invent seven facts."},
      {"text": "Possible internal transfers stay ambiguous and are never inferred. A statement shows one leg and Finance models a transfer as two rows with opposite categories, so classifying one alone would either invent the counterparty or inflate expense with money that never left."},
      {"text": "transactions.account_id remains legacy and closed to new construction. It is an unconstrained uuid carrying a Card id, exposed to no capability, and the import path does not touch it."},
      {"text": "The transfer invariant — exactly two legs, opposite directions, equal amount and date — is still enforced only by the application. transfer_pair_id has no foreign key and no unique constraint."},
      {"text": "Confidential capabilities leave no arguments or result in the audit, and the cross-turn evidence replay is built from those same rows. A model therefore cannot see what it observed a turn ago in this conversation. Finance works around it — the preview carries the workspace''s categories, and re-preparing resumes the batch — but the collision itself is an open Agents question, not a Finance one."},
      {"text": "A batch is capped at 1000 lines, and the limit is human auditability rather than performance."}
    ]'::jsonb,

    '[
      {"text": "The identity a statement is deduplicated under is issued by the database, not written by the caller. It was free text, and in one live conversation a model produced nubank-4242 and then nubank-cartao-4242 for the same card, importing the same statement twice. Idempotence cannot rest on a language model reproducing a string byte-for-byte across turns, so the argument stopped existing."},
      {"text": "An import source is not a Card. A statement is not always a card statement, and forcing a bank account into finance.cards would make Card mean any account. The optional card_id link carries no rule and uses ON DELETE SET NULL, so archiving a card can never take an import identity with it."},
      {"text": "A strong match may skip; a heuristic match must ask. The asymmetry is the whole design: the cost of a wrong skip is a real purchase silently missing from someone''s ledger, and the cost of a wrong question is one answer."},
      {"text": "Preview persists staging and the ledger stays untouched. Writing staging is not writing money, and it is what makes the flow possible at all: a tool schema of flat scalars cannot carry 150 rows in either direction."},
      {"text": "Commit is one set-based statement inside one transaction. A loop of 150 inserts is 150 chances to be half written, and ON CONFLICT DO NOTHING is not silent because the difference between eligible and created is reported as skipped_concurrent."},
      {"text": "The importer never creates a category, in the preview or as a side effect of the commit. Categories are proposed to the operator, approved, and created through the ordinary capability."},
      {"text": "Categories resolve on an exact normalised name and never on a substring. Filing MERCADO CENTRAL under Mercado by similarity puts real money under a label the operator did not choose, and it looks correct on every screen afterwards."},
      {"text": "An unreadable line becomes INVALID rather than a guess. \"1.500\" is refused as ambiguous instead of being read as 1500 or 1.50, because being wrong by a thousand in silence is worse than refusing, and three decimal places are refused rather than rounded."},
      {"text": "A statement line is a fact that happened, never a recurrence. Noticing that a salary repeats is a conversation; creating a RecurringEntry is a separate decision with its own confirmation."},
      {"text": "Re-importing after a soft delete recreates the row. The unique index is partial on deleted_at, and the alternative would block a deliberate re-import forever with no way for the operator to see why."},
      {"text": "Confirmation of a change comes from an execution receipt, never from what the agent wrote. The mechanism belongs to Agents and is recorded there; this release is the reason it exists and the first module to depend on it."}
    ]'::jsonb,

    '[
      {"text": "Two migrations in the finance timeline, both additive: 0013 creates the staging tables and 0014 the import sources plus a nullable import_source_id on the batch. Fourteen migrations now.", "ref": "migrations/finance"},
      {"text": "import_rows_parsed_chk requires a date, an amount and a direction on every state except invalid, which makes \"commit never imports an unreadable line\" a property of the schema rather than a promise of the code."},
      {"text": "The fingerprint is a sha256 over account namespace, date, cents, direction, normalised description and occurrence ordinal, truncated to 40 hex characters to stay inside the 200-character external_id bound."},
      {"text": "unnest is used for the staging insert rather than CopyFrom, because CopyFrom is not on postgres.Querier and would open its own connection — escaping the transaction and leaving staged rows behind a rolled-back prepare.", "ref": "internal/finance/adapters/repo/importbatches.go"},
      {"text": "Statement dates become midday in FINANCE_TIMEZONE, matching what a conversationally supplied date already did: a statement gives a day, and a day has to become an instant somewhere."},
      {"text": "batch_id is optional on resolve and commit, defaulting to the most recent prepared batch in the workspace. A model asked to carry a 36-character uuid across turns produced one that was not a uuid at all, and being refused for that reason is a bad way for an import to fail."}
    ]'::jsonb,

    '[
      {"label": "Implementation record", "path": "docs/implementation/FINANCE-STATEMENT-IMPORT.md"},
      {"label": "Finance module",        "path": "docs/modules/finance/README.md"},
      {"label": "Current state",         "path": "docs/modules/finance/current-state.md"},
      {"label": "Previous release",      "path": "docs/implementation/FINANCE-LEDGER-DOMAIN-COVERAGE.md"},
      {"label": "Totals contract",       "path": "backend/docs/totals-contract.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
