-- Agents 1.6.0 — external read grounding.
--
-- ── Why this release exists ────────────────────────────────────────────
-- A content agent with a real Meta Threads connection answered
-- "1.535 seguidores, 40.055 views" in a turn that made ZERO tool calls.
-- Read through the capability minutes later, the true figures were 163 and
-- 224. Every layer behaved correctly — the gate allowed a call that was
-- never made, the tool worked, the audit recorded that nothing ran — and
-- the person read invented numbers, because the product renders prose and
-- the ABSENCE of a read is silent.
--
-- This is the read-side twin of the defect 1.4.0 answered for writes
-- ("8 transações importadas" with nothing written), and it has the same
-- answer: make the absence explicit rather than invisible.
--
-- ── Why MINOR and not PATCH ────────────────────────────────────────────
-- A new field on ToolDefinition, a new column on the audit trail, a new
-- primitive on every assistant turn and a new frame member. Additive:
-- nothing existing changed shape, WriteReceipt is untouched, and an agent
-- with no external capability sees no difference at all.
--
-- ── Why the shape of this snapshot is not 0008's ───────────────────────
-- `evidence` is {label,value}, `limitations`/`decisions`/`technical_notes`
-- are {text,ref}. Writing {name,note} there — which 0008 did — decodes to
-- BLANK, which is the legacy debt already recorded against six releases.
-- The shape ratchet added in 1.5.0 refuses a new row that repeats it.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 1059  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--   13  `grep -rhoE 'External:\s*true' --include='*.go' internal/integrations/`
--    6  meta_threads tools declaring External
--    7  github tools declaring External
--    8  `grep -cE '^func Test' internal/chat/domain/read_receipt_test.go`
--    8  `grep -cE '^func Test' internal/integrations/metathreads/grounding_integration_test.go`
--   20  `ls migrations/chat/*.up.sql | wc -l`
--  380  frontend, `npx vitest run`

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.6.0', 1, 6, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-09-07 12:00:00+00',
    TIMESTAMPTZ '2026-09-07 12:00:00+00',

    'A turn that claims something about a system this product does not own now carries, '
    || 'beside it, whether anything was actually read. The guarantee is exact: NO CURRENT '
    || 'EXTERNAL EVIDENCE, NO VERIFIED EXTERNAL CLAIM. It does NOT prevent the model from '
    || 'producing a false external claim; it prevents the product from presenting that claim '
    || 'as verified by a current external read. Derived from execution records, never from '
    || 'the answer: nothing reads the prose, looks for numbers, or judges whether a sentence '
    || 'resembles a metric.',

    '[
      {"name": "ToolDefinition.External",     "note": "A capability declares that its result describes a system this product does not own. Declared by the tool, never inferred by Agents — the same rule Confidential follows, and the reason no list of vendor names exists inside the module."},
      {"name": "external on the audit trail", "note": "chat 0020. Stored beside the outcome rather than looked up, because the registry describes the build running now and not the build that ran then. Backfilled false, the value that claims the least."},
      {"name": "ReadReceipt",                 "note": "One assistant turn''s external-evidence record, derived from its audit rows. Emitted for every turn including the ones that read nothing, because absence has to be a value rather than a missing key."},
      {"name": "Three states",                "note": "VERIFIED_EXTERNAL_READ, FAILED_EXTERNAL_READ, NO_EXTERNAL_READ. A failure is its own state and never the silence of having read nothing: the two call for different words and different next actions."},
      {"name": "externalNotice",              "note": "Appended to what an external capability declares to the model, generated from the field. Names no vendor, no module and no tool. Defence in depth, not the guarantee."},
      {"name": "Stale external evidence",     "note": "A paragraph in the platform grounding policy: an earlier reading of a system we do not own is stale, not weak. It applies to every agent and is not written per prompt."},
      {"name": "ExternalReadStrip",           "note": "Renders the receipt and never the message. Its asymmetry is INVERTED from WriteReceiptStrip on purpose: the dangerous state for a read is the ABSENCE of one, so silence is what gets labelled."},
      {"name": "13 capabilities declared",    "note": "Six Meta Threads and seven GitHub, every one a read. No internal capability is marked, and no external one is a write."}
    ]'::jsonb,

    '[
      {"label": "Backend gate",              "value": "make -C backend ci green, integration included. 1059 Go test functions overall."},
      {"label": "Frontend gate",             "value": "380 tests, build clean, lint with no errors, tsc -b exit 0."},
      {"label": "New tests",                 "value": "8 domain tests for the receipt, 8 integration tests through the real turn loop, 7 component tests for the strip."},
      {"label": "False claim, reproduced",   "value": "A scripted turn emits the incident''s sentence word for word — 1.535 seguidores, 40.055 views — and the receipt beside it reports NO_EXTERNAL_READ with zero reads."},
      {"label": "False claim, live",         "value": "Asked for the figures from memory with no tool call, the receipt reported NO_EXTERNAL_READ and the agent declined to answer from memory."},
      {"label": "Real read, live",           "value": "Quantos seguidores eu tenho? executed meta_threads.profile.insights against the real connection and the turn carried VERIFIED_EXTERNAL_READ, one read, source meta_threads."},
      {"label": "Failure is not emptiness",  "value": "Two keyword searches failed upstream in one turn: the receipt reported FAILED_EXTERNAL_READ with two failures, and the answer said it was an upstream failure rather than an empty history."},
      {"label": "Evidence does not travel",  "value": "A read in one turn leaves the next turn at NO_EXTERNAL_READ, in the same conversation and across conversations. Proved by test and observed live."},
      {"label": "Ours is not theirs",        "value": "A turn full of internal reads still carries NO_EXTERNAL_READ: reading our own Postgres says nothing about somebody else''s system."},
      {"label": "Confidential intact",       "value": "The receipt selects neither arguments nor result, so a Confidential capability produces the same receipt as any other. The redaction suite passes unchanged."},
      {"label": "WriteReceipt intact",       "value": "Untouched in shape and behaviour; 14 receipt tests pass. The write frame member and its strip are unchanged."},
      {"label": "Instructions cleaned",      "value": "Six mutable external snapshots removed from the Content agent''s instructions — a follower count, four view counts and a profile bio asserted as current — with no current figure put in their place."}
    ]'::jsonb,

    '[
      {"text": "This does not prevent the model from producing a false external claim. It prevents the product from presenting that claim as verified by a current external read. Nothing here reads the answer, and no layer can tell whether a sentence matches what a capability returned."},
      {"text": "The guarantee is about PRESENTATION, not truth. A turn marked VERIFIED_EXTERNAL_READ read something; it does not follow that the sentence beside it reports what was read correctly."},
      {"text": "External is declared, not detected. A capability that reads an outside system and forgets to say so produces no receipt entry, and the turn reads as NO_EXTERNAL_READ — which under-claims rather than over-claims, but is still wrong."},
      {"text": "Availability is computed from the agent''s CURRENT grants, so it is a fact about now rather than about the turn being reported on. It gates presentation only and carries no claim; the three statuses come from persisted execution records."},
      {"text": "Historical rows are backfilled external = false, so no turn that happened before this release can be shown as a verified external read."},
      {"text": "meta_threads.post.search remains PARTIAL. Meta answers 500 or a generic error for some queries and 200 for others with an identical request shape; it is upstream instability, not a permission, an App Review gate or a malformed request. Sample of four queries.", "ref": "docs/integrations/meta-threads/README.md"},
      {"text": "The Meta Threads connection expires 2026-11-06. Nothing refreshes it automatically: a refresh is an operator action through the Integrations card."},
      {"text": "Confidential and cross-turn evidence replay still interact: a redacted result cannot feed evidence continuity, so an agent re-reads through a capability. Recorded in 1.5.0 and unchanged here."}
    ]'::jsonb,

    '[
      {"text": "The distinction is ours-versus-theirs, not read-versus-write. A wrong claim about our own state is a refresh problem, checkable against our own database whenever somebody doubts it. A wrong claim about somebody else''s is not checkable inside the product, and looks exactly as authoritative either way."},
      {"text": "The tool declares External about itself. Teaching Agents which namespaces are external would put a list of vendor names inside the one module arranged never to contain one — the same argument that produced Confidential."},
      {"text": "The column is stored, not looked up. The registry describes the build running now; a capability whose declaration changed, or which was removed, would silently rewrite the answer for a turn that already happened."},
      {"text": "Absence is a value. A receipt is emitted for every assistant turn, including the ones that read nothing, because a missing key renders as nothing at all — and nothing at all is exactly what the interface showed when the figures were invented."},
      {"text": "The strip''s asymmetry is inverted from the write strip, deliberately. WriteReceiptStrip shows a confirmed change and stays quiet otherwise, because a badge on every unchanged turn is noise. For reads the dangerous state is the absence, so the absence is what is labelled — copying the write rule would have reproduced the bug."},
      {"text": "No claim detection. No regex, no number-spotting, no judgement about whether a sentence resembles a metric. A heuristic pretending to be a guarantee would make its first false negative indistinguishable from safety."},
      {"text": "The receipt carries no payload. Copying a follower count into it would persist somebody''s metrics a second time, in a record built for provenance, outside the rule that governs the first copy."},
      {"text": "Prompt is defence in depth and not the guarantee. Both the capability declaration and the platform policy tell the model the rule; the receipt holds whether or not the model reads either."},
      {"text": "Mutable external data does not belong in an agent''s instructions as current state. A follower count written into a prompt is the same failure with a different author: it ages, it reads as authoritative, and no capability was called to obtain it."}
    ]'::jsonb,

    '[
      {"text": "chat 0020 adds external BOOLEAN NOT NULL DEFAULT false plus a partial index on (workspace_id, message_id) WHERE external. Twenty migrations in the chat timeline.", "ref": "migrations/chat"},
      {"text": "The grounding policy''s size gate rose from 560 to 700 estimated tokens, with the raise recorded as a decision beside the two before it. The paragraph costs roughly 133 tokens and only on turns that declare capabilities."},
      {"text": "ReadReceipt precedence: any success makes the turn VERIFIED even alongside a failure, the failed sibling being reported in the counts rather than by downgrading the whole turn. Attempted-and-all-failed is FAILED. No external call at all is NO_EXTERNAL_READ."},
      {"text": "A refusal and an upstream error are one state. They differ in whose fault it was, which the error code carries; they do not differ in what the turn may claim."},
      {"text": "Source is derived from the capability name''s first segment and never stored, so it cannot disagree with the name it came from."}
    ]'::jsonb,

    '[
      {"label": "Meta Threads integration", "path": "docs/integrations/meta-threads/README.md"},
      {"label": "Agents module",            "path": "docs/modules/agents/CLAUDE.md"},
      {"label": "Write receipt, 1.4.0",     "path": "docs/implementation/AGENTS-TOKEN-ECONOMICS-CACHING.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
