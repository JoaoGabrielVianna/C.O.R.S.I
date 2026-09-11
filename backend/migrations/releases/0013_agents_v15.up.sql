-- Agents 1.5.0: the same context, billed differently.
--
-- ── What this release is ───────────────────────────────────────────────
-- A cost change with no product change. The model receives exactly the same
-- characters, in the same order, before and after. One message per request
-- now carries a marker asking the provider to cache the prefix ending at
-- it, and the provider bills that prefix at a tenth of the input rate on
-- every subsequent request that matches it byte for byte.
--
-- ── Why it was possible now and not before ─────────────────────────────
-- The context engine classified prompt caching as FUTURE, gated on one
-- condition: "until the LiteLLM boundary is closed". That boundary was
-- declared VERIFIED with 1.4.0. The item was never reopened, and the token
-- economics audit found it by measuring where the money went.
--
-- ── What it is NOT ─────────────────────────────────────────────────────
-- It does not reduce the tool catalogue, truncate or summarise history,
-- reorder any context block, discover tools dynamically, route between
-- models, or change maxToolRounds. Nothing was taken away from the model to
-- make this cheaper, and the one known workflow that hits the round ceiling
-- still hits it — declared below as a limitation, with its own proposal.
--
-- ── Why MINOR ──────────────────────────────────────────────────────────
-- The history's convention. It adds three nullable columns, five optional
-- usage fields, a request-body shape used on one message, a report field, a
-- kill switch and a recalibrated estimator. It removes nothing, and every
-- existing behaviour stays. The two non-additive consequences — the cost
-- formula and the estimator's scale — are declared rather than glossed.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 1018  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
--  253  `grep -rhE '^func Test' internal/chat/*_integration_test.go | wc -l`
--   20  cache/usage tests across the wire adapter, the builder and integration
--    7  X3 live tests behind the `litellm` build tag
--   19  `ls migrations/chat/*.up.sql | wc -l`
--  361  frontend, `npx vitest run`
-- The gateway figures come from X3 runs against this deployment's own
-- LiteLLM on claude-opus-4-7, recorded in
-- docs/implementation/AGENTS-TOKEN-ECONOMICS-CACHING.md.

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.5.0', 1, 5, 0,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-30 12:00:00+00',
    TIMESTAMPTZ '2026-08-30 12:00:00+00',

    'A turn now asks the provider to cache the stable head of its prompt, and the provider '
    || 'bills that head at a tenth of the input rate on every request that matches it. Nothing '
    || 'the model reads changed: the same characters arrive in the same order, and the only '
    || 'difference on the wire is one message carrying a cache marker. The runtime also stopped '
    || 'discarding the cache and reasoning counts the gateway had been reporting all along, so '
    || 'the saving is measurable rather than asserted — and the cost formula now separates '
    || 'full-price input from cache reads and cache writes, priced from the gateway''s own rate '
    || 'card. A controlled A/B on one prefix-dominated workload went from US$ 0,130120 to '
    || 'US$ 0,019663. That is an experimental ceiling on a favourable shape, not a forecast.',

    '[
      {"name": "cache_control on the wire",      "note": "A message body can now be either a bare JSON string or an array of content blocks. It could not before: `content` was typed `string`, and cache_control lives inside a block, so caching was not merely unconfigured but inexpressible. Without a marker the field marshals to the identical bare string it always did."},
      {"name": "One stable-prefix breakpoint",   "note": "Placed on the last instruction message — the platform grounding policy and the agent''s own prompt, the only two things in a request that are the same bytes on every turn of an agent''s life. Exactly one marker, and no block was moved to make room for it."},
      {"name": "The tool catalogue rides inside it", "note": "The provider renders `tools` before `system`, so a marker on the instructions closes a prefix that already contains the tool declaration — the largest single line item in this system''s bill. Measured through this deployment''s gateway rather than inferred: a 66-character system message with a full-size catalogue read back 18.387 of 18.829 prompt tokens."},
      {"name": "Full usage preservation",        "note": "cache_read_input_tokens, cache_creation_input_tokens, prompt_tokens_details.cached_tokens, completion_tokens_details.reasoning_tokens and total_tokens are read, translated and stored. The adapter declared two fields and encoding/json discarded the rest — including every field that says whether caching did anything."},
      {"name": "ABSENT is not ZERO",             "note": "Every optional count is a pointer from the wire to the column. A gateway that reports nothing and a call that read nothing from cache would otherwise be indistinguishable, and every hit rate computed over them would silently average in calls nobody measured. UsageLine.cache_measured_messages is the denominator that travels with the sums."},
      {"name": "Per-round usage in the report",  "note": "ContextReport.rounds[].usage carries what the provider said about THAT call. A tool turn whose first call writes the entry and whose second reads it is the shape this field exists to make visible; a turn-level sum cannot say which call did which."},
      {"name": "Cache-aware cost",               "note": "Cost splits the prompt into full-price, cache-read and cache-creation and prices each from the rate the gateway publishes for the model, not from a hardcoded multiplier. prompt_tokens INCLUDES the cached share — measured, not assumed — so the three classes partition it rather than adding to it."},
      {"name": "CacheBreakpointAfter",           "note": "The report records which block the turn asked to cache up to, or nothing. A cache read of zero has two opposite causes — the turn never asked, or it asked and the prefix had moved — and the counts alone cannot tell them apart."},
      {"name": "Calibrated token estimation",    "note": "The estimator divided characters by four. Against 159 real turns that was low by nearly a factor of two, in the one direction a number feeding a spending limit must never be low in. It is now calibrated per model family from measured density, using each family''s MAXIMUM rather than its median."},
      {"name": "CHAT_DISABLE_PROMPT_CACHE",      "note": "A kill switch on an external boundary. Off by default, so the default behaviour is to cache. Set it and the request body returns to the pre-caching one, with no marker, no content blocks and no cache_control anywhere — without a code deploy."}
    ]'::jsonb,

    '[
      {"label": "Gateway accepts and forwards",  "value": "Against this deployment''s LiteLLM on claude-opus-4-7: a marked prefix reported creation=13965 on the first request and read=13965 on an identical repeat, while an unmarked control of the same prefix reported creation=0 and read=0. The control is what makes it evidence rather than correlation."},
      {"label": "Tool catalogue is cached",      "value": "A 66-character system message with a full-size tool array read back 18.387 of 18.829 prompt tokens on the repeat. One marker on the instructions covers the catalogue."},
      {"label": "prompt_tokens includes cache",  "value": "The same prompt reported 13.982 tokens on all three calls — cache write, cache read and no cache. Had the counts been additive the cached call would have reported 17. This is the fact the cost formula partitions on."},
      {"label": "Controlled A/B, same workload", "value": "Three turns, same agent, same prompt, same questions, one boolean apart. OFF: 25.939 prompt tokens all at full price, US$ 0,130120. ON: the same 25.939, of which 1.393 at full price and 24.546 served from cache, US$ 0,019663. Latency 4.784 ms against 4.816 ms."},
      {"label": "Tool round reuse",              "value": "Within one turn the prefix is untouched, and round 2 was served 8.223 of 8.805 prompt tokens from cache — 93,4%."},
      {"label": "Semantic equivalence",          "value": "The same context built twice, caching off and on, then flattened to roles, text and tool scaffolding and compared: the cache metadata is the only difference. Total characters, estimated tokens, every block and every exclusion are identical."},
      {"label": "Kill switch",                   "value": "A turn run through the production route with caching disabled carries no breakpoint, records none in its report, and differs from the enabled turn in nothing else: same messages, same order, same roles, same text, same tools, same parameters."},
      {"label": "Nothing private is cacheable",  "value": "Driven with real conversation content and a real tool exchange: every message at or before the marker is a system instruction. No user message, assistant reply, tool call, tool result or hydrated state is ever inside the marked prefix."},
      {"label": "Estimator against real turns",  "value": "On eight real turns from the audit corpus the retired characters/4 heuristic understated 8 of 8; the calibrated estimator understates 0 of 8."},
      {"label": "Backend gate",                  "value": "make -C backend ci green, integration suite included. 1018 Go test functions overall, 253 chat integration tests, 20 of them written for caching and usage."},
      {"label": "Frontend gate",                 "value": "361 tests, build clean, tsc -b clean, lint with no errors."}
    ]'::jsonb,

    '[
      {"text": "maxToolRounds = 3 is unchanged. A legitimate statement-import workflow was observed hitting it: prepare, then resolve_group, then commit is three tool rounds, and the runtime refuses the third before executing it. Fourteen turns ended that way, all Ledger, all in that flow. This release does NOT fix it. A policy exists and is deliberately not implemented here, because it is a semantic change and this release is a billing change.", "ref": "docs/implementation/AGENTS-TOOL-ROUND-SAFETY-POLICY.md"},
      {"text": "The 84,9% figure is an experimental ceiling, not a forecast. That workload is dominated by its stable prefix — three turns, short history — and the marked prefix therefore covers almost the whole request. On the real corpus, where a long conversation''s history reaches 82,9% of the input and sits OUTSIDE the marked prefix, the audit''s defensible projection is about 44,1% of total spend. Neither number is an observed production result, and the real saving must be read from telemetry as it accumulates.", "ref": "docs/audit/07-agents-token-economics.md"},
      {"text": "History is not cached. The breakpoint stops at the instructions because reference state is re-read every turn and tool evidence is recomputed every turn, and both sit between the instructions and the history. Extending the prefix over the conversation would need those blocks moved, which is a context-ordering decision this release did not take."},
      {"text": "History still has no character ceiling, and never had one. The context engine document claimed for three months that it did and that this closed the unpredictable cost hole; it did not. A single pasted message may be 100.000 bytes and is replayed on every turn it stays inside the message-count window. The documentation was corrected to match the runtime and no truncation was implemented — cutting history is the one economy on the table that reduces what the model knows.", "ref": "docs/modules/agents/v1/context-engine.md"},
      {"text": "Cache entries are scoped by the provider to the credential, not to a workspace. This installation uses one gateway key for every workspace, so no per-workspace cache isolation is claimed here — the gateway offers no way to prove one. What IS provable is the content: only the tool declaration, the grounding policy and the agent''s own system prompt can be inside a marked prefix, all three operator-authored configuration, and a match requires sending that identical prefix in the first place."},
      {"text": "Estimated token figures on turns recorded from now on are roughly twice what the same context would have reported before. That is a correction of a two-fold understatement, not inflation. Turns already stored are immutable and unchanged, so the timeline contains a visible scale change at this release."},
      {"text": "The grounding policy''s token budget was restated from 280 to 560. The policy text did not change by one character; the estimator that measures it did. Restating it preserved the same ~10% headroom over the same text rather than cutting the policy to fit a number that was always wrong."},
      {"text": "A cache entry lives five minutes and is not re-warmed. Turns more than five minutes apart pay the write premium again. The one-hour variant costs twice as much to write and choosing it is a decision that needs measured traffic, so the TTL is left at the default and is not sent explicitly."},
      {"text": "Prompt caching is a provider behaviour, not a guarantee this system can enforce. A prefix below the model''s minimum cacheable length is silently not cached, at no cost and with no error, and the runtime cannot tell that apart from a gateway that stopped honouring the marker. cache_measured_messages and CacheBreakpointAfter exist so that the difference is at least visible after the fact."}
    ]'::jsonb,

    '[
      {"text": "NOTHING IS TAKEN AWAY FROM THE MODEL TO MAKE THIS CHEAPER. The saving comes entirely from how a prefix is billed. Every economy that would have reduced what the model reads — smaller catalogue, truncated history, summarisation, a cheaper model — was available and was deliberately not taken."},
      {"text": "The breakpoint goes where nothing has to move for it to work. A second marker at the end of the history would be worth more, and would require reordering two blocks whose positions are argued decisions. One marker, placed at the boundary that already exists."},
      {"text": "The gateway was asked before the runtime was changed. Seven questions, answered with observed bytes against the real deployment, including a differential against an unmarked control — because a gateway that strips the field answers 200 and reports zero, exactly like a cache that did not hit."},
      {"text": "Cost is priced from the gateway''s rate card, not from published multipliers. The card is what will be billed; a multiplier is what a document says. The two agree today for claude-opus-4-7, and hardcoding the agreement would have put a pricing assumption into a system whose accounting exists to avoid one."},
      {"text": "ABSENT and ZERO are different facts at every layer, from the wire struct to the column. This is the same rule usage_source already applied to the two token counts, extended to the counts that say what those two were made of."},
      {"text": "The estimator is calibrated to each family''s MAXIMUM observed density rather than its median. An estimator right on average is wrong half the time, and half of those times it is low — which is the one direction a number feeding a spending limit cannot be wrong in."},
      {"text": "An unmeasured model gets the most pessimistic density in the table, not an average of the measured ones. An unknown model is where being wrong is most likely, so it is where the estimate leans hardest away from optimism."},
      {"text": "The kill switch is an opt-OUT because the safe default is the cheap one. Caching changes how a prefix is billed and not what the model reads, so a deployment that configures nothing should get it; a variable that had to be switched on would mean paying full price until somebody remembered."}
    ]'::jsonb,

    '[
      {"text": "One migration in the chat timeline: three nullable columns for cache read, cache creation and reasoning tokens, with non-negative CHECKs. NULL means the provider said nothing and is not a measured zero. Nineteen migrations now.", "ref": "migrations/chat/0019_messages_cache_usage.up.sql"},
      {"text": "`content` became a type with its own marshaller rather than an `any`, so the default case stays a bare JSON string and the decision lives in one place. An empty body keeps the string form even if marked: an empty text block is a documented rejection on several gateways, so the marker is dropped and the content is not.", "ref": "internal/chat/adapters/llm/client.go"},
      {"text": "The breakpoint is decided by the Context Builder and written down by the adapter. Only the builder knows which of eight system messages is the last stable one; the adapter has no way to know and is not asked.", "ref": "internal/chat/app/context.go"},
      {"text": "The X3 suite lives behind its own `litellm` build tag and is not part of any gate, for the reason the X1 suite gives: a gate that fails when someone else''s gateway is down teaches people to ignore red.", "ref": "internal/chat/litellm_cache_xtest_test.go"},
      {"text": "The captured usage frame of a real cache hit was added as a fixture. The frame captured for X1 predates caching, so every cache number in it is zero — which cannot distinguish a parser reading the right field from one reading the wrong field and getting zero anyway.", "ref": "internal/chat/adapters/llm/litellm_real_test.go"},
      {"text": "The token-economics audit that produced this release is preserved in full, including the findings it raised that this release does not address.", "ref": "docs/audit/07-agents-token-economics.md"}
    ]'::jsonb,

    '[
      {"label": "Sprint record",       "path": "docs/implementation/AGENTS-TOKEN-ECONOMICS-CACHING.md"},
      {"label": "Cost audit",          "path": "docs/audit/07-agents-token-economics.md"},
      {"label": "Tool-round proposal", "path": "docs/implementation/AGENTS-TOOL-ROUND-SAFETY-POLICY.md"},
      {"label": "Context engine",      "path": "docs/modules/agents/v1/context-engine.md"},
      {"label": "Agents module",       "path": "docs/modules/agents/CLAUDE.md"},
      {"label": "Previous release",    "path": "docs/implementation/FINANCE-STATEMENT-IMPORT.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
