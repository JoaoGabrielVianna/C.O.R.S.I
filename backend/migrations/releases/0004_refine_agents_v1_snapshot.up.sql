-- The last edit before the freeze.
--
-- 0002 recorded Agents 1.0.0 as a draft with nine coarse capabilities.
-- Nothing in it was false, and none of the measured evidence moved: the
-- counts were re-verified from the repository on 2026-08-12 and are still
-- 123 / 38 / 12 / 8 / 8-of-8. This migration does not touch them.
--
-- What it does is finish the record while it is still possible. A draft is
-- the only state in which a snapshot can be improved; after 0005 the
-- freeze trigger refuses every UPDATE, forever. Two things needed to be in
-- it before that:
--
--   1. Capabilities the implementation distinguishes and the snapshot did
--      not. Token budgets and money budgets are separate columns with
--      separate gates (`daily_token_limit`, `daily_cost_limit_usd`).
--      Authorizing a tool, running the execution loop, and recording the
--      audit trail are three different mechanisms in three different
--      places. Listing them as one line each was a summary, and a release
--      history is read to answer "what exactly was in it".
--
--   2. The owner's decision of 2026-08-12 on max_budget, which is what
--      turned a release blocker into an accepted risk. A snapshot that
--      omitted it would leave the published release unexplained: the
--      limitation is still there, and the reason it did not block is the
--      decision.
--
-- ── What was deliberately NOT added ────────────────────────────────────
-- Comparison Mode / multi-agent panes. The code exists
-- (`useChatPanes.ts`), and its own header says it is FROZEN and out of
-- scope for v1.0.0 by owner decision D8, with nothing reading it any more.
-- Recording it as a capability of this release would be describing the
-- product as larger than the owner decided it is.

UPDATE releases.releases SET
    capabilities = '[
      {"name": "Conversations",            "note": "Threads that belong to an agent, each addressable by URL: create, rename, delete, truncate from a user turn and regenerate."},
      {"name": "Streaming",                "note": "Turns arrive incrementally over SSE and persist in every outcome, including gateway failure and cancellation."},
      {"name": "Reasoning channel",        "note": "Models that emit reasoning have it carried on its own frame, never mixed into the answer text."},
      {"name": "Memory",                   "note": "Deterministic capture of facts that outlive a conversation and are replayed into later turns of the same agent."},
      {"name": "Brain view",               "note": "Memory provenance as a graph. A pure projection over stored memories; the graph itself is never persisted."},
      {"name": "Sources",                  "note": "Reference documents attached to an agent and injected into its context."},
      {"name": "Context Builder",          "note": "One place composes every turn: system prompt, sources, memory and history up to history_limit."},
      {"name": "Context Inspector",        "note": "Per-turn record of exactly what was sent to the model, round by round, including tool rounds."},
      {"name": "Usage accounting",         "note": "Prompt and completion tokens with their usage source, per round and summed across a turn."},
      {"name": "Historical cost accounting","note": "Price is stamped onto the turn when it happens, so a later change to a rate card cannot reprice the past."},
      {"name": "Token budgets",            "note": "Optional daily token ceiling per agent, in UTC, checked by a preflight before every provider call."},
      {"name": "Money budgets",            "note": "Optional daily USD ceiling per agent, enforced by the same preflight. Overshoot is bounded at one provider call."},
      {"name": "Tools",                    "note": "A registry that ships with the binary. There is no tool-definitions table, because a tool is code."},
      {"name": "Tool authorization",       "note": "Deny by default. A grant is the presence of a row and revoking deletes it; a grant whose tool a deploy removed goes stale rather than silently working."},
      {"name": "Tool execution loop",      "note": "Provider asks, CORSI resolves the name, checks the grant, validates arguments, executes, returns the result, and the model continues. Bounded by a per-conversation round limit."},
      {"name": "Tool audit trail",         "note": "Every call persisted with the gateway tool_call_id, arguments, result and duration."},
      {"name": "LiteLLM integration",      "note": "OpenAI-compatible gateway: model catalogue, pricing, streaming, key info and actionable error translation. Credentials sealed with AES-256-GCM and never returned."}
    ]'::jsonb,

    decisions = '[
      {"text": "Agents v1.0.0 is approved for release by the owner on 2026-08-12, with the residual limitations below knowingly accepted.", "ref": "Owner decision, 2026-08-12"},
      {"text": "External LiteLLM max_budget is not a release requirement for Agents v1.0.0. The absence of that external circuit breaker is an accepted risk, not a blocker.", "ref": "Owner decision, 2026-08-12"},
      {"text": "Budget enforcement is owned by C.O.R.S.I. The local gate never consults the gateway budget, and the two layers stay separate.", "ref": "D5 · TestLocalBudgetNeverConsultsTheGatewayBudget"},
      {"text": "A real LiteLLM flow is a release gate. It was met twice: the conversation flow in X1, and tool calling end to end on 2026-08-11.", "ref": "D7"},
      {"text": "Comparison Mode is out of scope for v1.0.0. The code is preserved rather than deleted, and removal stays a separate explicit decision.", "ref": "D8"},
      {"text": "The product domain is called Agents; the implementation namespace stays chat. No rename is authorized for aesthetic reasons.", "ref": "ADR-02"},
      {"text": "The tool registry is code, not a table. There is no tool-definitions schema, because a tool ships with the binary.", "ref": "migrations/chat/0012_tools"},
      {"text": "Tools are deny-by-default, and a grant is the presence of a row. Revoking deletes it.", "ref": "migrations/chat/0012_tools"},
      {"text": "system.echo is a diagnostic, not a product capability: absent from the production registry, available only where internal tools are explicitly enabled.", "ref": "Release Closure GATE 6"},
      {"text": "Tool names cross the wire with dots encoded as double underscores. The gateway rejected a dotted function name with an explicit pattern error, so the encoding is load bearing and must not be removed.", "ref": "TestLiveToolNameEncoding"}
    ]'::jsonb,

    -- The limitations are unchanged in substance. The max_budget line is
    -- rewritten only to say that it is accepted rather than outstanding —
    -- the fact it describes is identical, and softening it into
    -- disappearance would hide the risk the owner actually took.
    limitations = '[
      {"text": "The external circuit breaker is unset: max_budget on the production LiteLLM virtual key is not configured. Accepted by the owner for this release; spend enforcement rests entirely on the in-product budget."},
      {"text": "Tool calling was verified against a real gateway using deepseek/deepseek-chat, the only model the disposable test key could reach. The production model was not exercised."},
      {"text": "Budget overshoot of up to one provider call is possible, and concurrent turns are not serialized against the same ceiling."},
      {"text": "The frontend has no test harness, so none of the Agents UI has automated regression."},
      {"text": "The 38 routes are not covered by any OpenAPI specification."},
      {"text": "Backend error messages reach the Portuguese interface in English."},
      {"text": "Workspace isolation partitions data but does not authenticate: any syntactically valid UUID is accepted. Identity does not exist on the platform."}
    ]'::jsonb,

    technical_notes = '[
      {"text": "Schema chat, on its own migration timeline with version table schema_migrations_chat. 12 migrations, 8 tables, 38 routes."},
      {"text": "The LiteLLM adapter lives inside the bounded context as the driven adapter of ports.LLM. This is a known departure from the target Integrations boundary and is deliberately not corrected in this version."},
      {"text": "External boundary verified twice against a real gateway: the conversation flow (X1, 2026-08-10) and tool calling end to end (2026-08-11), the latter including a second provider round that carried the tool result and summed across both calls."},
      {"text": "Provider credentials are sealed with AES-256-GCM before storage and never returned; responses carry only api_key_hint. Rotating SECRETS_KEY invalidates every stored credential by design."}
    ]'::jsonb
WHERE module_key = 'agents'
  AND version    = '1.0.0'
  -- Only while it is still a draft. If the release was already published
  -- by the time this runs, the freeze is in force and this must not be the
  -- thing that quietly edits history.
  AND status     = 'draft';
