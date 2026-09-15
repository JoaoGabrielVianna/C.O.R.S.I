/**
 * Typed REST client for /chat/conversations.
 *
 * Sending a message is not here — it streams, and lives in `./stream`.
 * This module covers the thread list and the transcript read.
 */

import { apiFetch } from "@/lib/api/client";
import type {
  ApiContextReference,
  ContextReferenceInput,
} from "@/modules/agents/context-references/contract";
import type { ReadReceipt, WriteReceipt } from "./stream";

export type MessageRole = "user" | "assistant";

/**
 * Why a streamed reply stopped. `stop` is the ordinary case.
 *
 * `tool_round_limit` is ours rather than the provider's: the turn asked to
 * run tools more times than one turn may, and the backend stopped the loop.
 * `tool_calls` is never stored — it is a mid-turn state the loop consumes —
 * so it does not appear here.
 */
export type FinishReason = "stop" | "length" | "aborted" | "error" | "tool_round_limit";

export interface ApiConversation {
  id: string;
  workspace_id: string;
  agent_id: string;
  /** Empty until the first message, which the backend derives it from. */
  title: string;
  last_message_at?: string;
  /**
   * What this whole thread is about, set when it was opened from an entity
   * ("Conversar com Scout" from a Job Radar card).
   *
   * Absent for every conversation started from the composer, which is the
   * ordinary case and must render exactly as it did before this existed.
   * `unavailable` on an entry is recomputed by the backend on each read.
   */
  context_references?: ApiContextReference[];
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

/**
 * How much a turn's token counts can be trusted. The backend is the only
 * author of this vocabulary: "unknown" means the numbers beside it are
 * placeholders, not measurements.
 */
export type UsageSource = "unknown" | "provider" | "estimated";

/** One contributor to a turn's context. The set is closed by the backend. */
export type ContextBlockKind =
  | "instructions"
  // What the agent may execute. It rides in the request's own `tools` field
  // rather than as a message, and it is a block because it is billed as
  // input on every call of the turn.
  | "tools"
  | "memory"
  | "sources"
  | "history"
  | "current_message"
  // What the tools returned, plus the request the model made for them. Both
  // are re-sent on the next call of the same turn, and both are paid for.
  | "tool_results"
  // What the tools of EARLIER turns of the same conversation returned,
  // replayed so a follow-up question is answered from the observation rather
  // than from the assistant's prose about it. Distinct from `tool_results`:
  // nothing ran this turn to produce it.
  | "tool_evidence"
  // What earlier turns of the same conversation DID: the write capabilities
  // that ran and how each ended. Distinct from `tool_evidence`, which is the
  // payload those turns observed — this block carries no payload at all, and
  // a capability whose payload is withheld still appears in it.
  | "execution_evidence";

/**
 * Why something a turn could have carried did not reach the model.
 *
 * The backend is the only author of this vocabulary. The client never
 * derives a reason, never recomputes a budget, and never invents a
 * category — it renders what it was told.
 */
export type ContextExclusionReason =
  | "empty_turn"
  | "budget"
  | "too_large"
  | "unavailable"
  // Something the agent was authorized to use and the user did not attach
  // to this turn. Only the `tools` block carries it, and only on a turn the
  // composer scoped with an explicit selection. Nothing failed and nothing
  // was too big — the turn was narrowed on purpose.
  | "not_selected"
  // The tail of an item that WAS carried, cut so the rest would fit.
  // `characters` is what was dropped and `items` is zero: nothing was left
  // out, part of something was.
  | "truncated"
  // A recorded tool call that observed nothing because it failed, so it is
  // not replayed as though it had.
  | "failed"
  // A recorded tool call an identical, later one replaced.
  | "superseded";

export interface ApiContextExclusion {
  reason: ContextExclusionReason;
  items: number;
  characters: number;
}

export interface ApiContextBlock {
  kind: ContextBlockKind;
  items: number;
  /** Exact, counted in characters. */
  characters: number;
  /** Approximate. Never present it as a measurement. */
  estimated_tokens: number;
  exclusions?: ApiContextExclusion[];
}

/**
 * The account of what a turn actually carried, stamped when it happened.
 *
 * It is a snapshot, not a view of the present: the memories it counted may
 * have been deleted since, the sources rewritten, the instructions changed.
 * Nothing here points at those records, precisely so nothing can "resolve"
 * them against today's data and turn a historical record into a fiction.
 */
export interface ApiContextReport {
  blocks: ApiContextBlock[];
  total_characters: number;
  total_estimated_tokens: number;
  /**
   * One entry per call this turn made to the provider, present only when it
   * made more than one — which today means a turn that used tools.
   *
   * Before tools, "one turn" and "one provider call" were the same thing and
   * the blocks above described the whole turn. They are not the same any
   * more: context grows between calls, and tokens are spent once per call.
   * Showing the first call's snapshot as though it described all of them
   * would be the Inspector's first lie.
   */
  rounds?: ApiContextRound[];
}

/** One provider call inside a turn: what it added, and what it cost. */
export interface ApiContextRound {
  round: number;
  /** What this call carried beyond the previous one. Zero on the first. */
  added_characters: number;
  added_estimated_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  usage_source: UsageSource;
  /** Null when this call could not be priced. Never read a null as zero. */
  cost: number | null;
  finish_reason?: string;
  /** The tools this call asked to run, in the order it asked. */
  tools?: ApiRoundToolCall[];
}

/** One tool call's outcome, at report granularity. No payloads. */
export interface ApiRoundToolCall {
  name: string;
  /**
   * `not_executed` is a call the runtime refused before it ran: an
   * unauthorized capability, arguments that did not validate. It is
   * told apart from `error` because a write receipt has to be able to
   * say whether something was ATTEMPTED, and code that only needs to
   * know it did not succeed should test for `!== "ok"`.
   */
  status: "ok" | "error" | "not_executed";
  /** Empty on success. One of the backend's tool error codes otherwise. */
  error_code?: string;
  duration_ms: number;
}

/** The families a turn reference can belong to. Closed by the backend. */
export type ApiReferenceKind = "tool";

/**
 * One capability the user attached to a turn, as it was stored.
 *
 * A snapshot, like the context report: `label` is how it read at the moment
 * it was attached, frozen by the backend from its own registry. The
 * transcript renders it as it is and never resolves it against the
 * catalogue as it stands now — the grant may have been revoked, the tool
 * renamed, or the build that implemented it deployed away, and a turn that
 * happened must go on saying what happened.
 */
export interface ApiTurnReference {
  kind: ApiReferenceKind;
  /** The canonical identity — for a tool, its name. Never a display string. */
  id: string;
  label: string;
}

export interface ApiMessage {
  id: string;
  workspace_id: string;
  conversation_id: string;
  role: MessageRole;
  content: string;
  /** The model's chain of thought, when it emitted one. */
  reasoning?: string;
  /** Wall-clock ms spent reasoning before the first answer token. */
  reasoning_ms?: number;
  model?: string;
  /**
   * What the provider reported this turn consumed. Read `usage_source`
   * before trusting them: "unknown" means these are placeholders.
   */
  prompt_tokens: number;
  completion_tokens: number;
  usage_source?: UsageSource;
  /**
   * What the Context Builder predicted the input would be, before the
   * call. Kept beside `prompt_tokens` so the estimate can be compared with
   * the measurement instead of replaced by it.
   */
  estimated_prompt_tokens?: number;
  /**
   * The rate card frozen at the moment of the turn, and the resulting cost.
   * **Absent means unknown, never zero.** A surface that renders a missing
   * cost as "$0.00" is claiming a turn was free.
   */
  input_cost_per_token?: number | null;
  output_cost_per_token?: number | null;
  cost?: number | null;
  /**
   * Absent on user turns, and on assistant turns written before the report
   * was recorded. Absent is "not recorded", not "nothing was sent".
   */
  context_report?: ApiContextReport;
  /**
   * What the user attached to this turn. Present only on user turns that
   * made an explicit selection.
   *
   * **Absent means no selection was made**, which is the legacy behaviour:
   * the turn declared every tool the agent was authorized to use. It does
   * NOT mean "no capabilities were available", and a surface that renders
   * the two the same way is saying something the backend never said.
   */
  references?: ApiTurnReference[];
  /**
   * The entities this turn was about.
   *
   * A different field from `references` and a different fact: that one is
   * the CAPABILITIES the turn was scoped to, this one is the SUBJECTS it
   * discussed. See `context-references/contract.ts`.
   */
  context_references?: ApiContextReference[];
  finish_reason?: FinishReason;
  /** Set when the turn failed; the content is whatever arrived first. */
  error?: string;
  created_at: string;
  /** Monotonic ordering key — reliable where timestamps tie. */
  seq: number;
}

export interface ConversationPage {
  items: ApiConversation[];
  limit: number;
  offset: number;
  /**
   * How many threads the request matched *before* the page bounds. It is
   * what lets a list say "20 of 34" instead of stopping without a word.
   */
  total: number;
}

export interface ListConversationsParams {
  /** Narrows to one agent. Omitted lists the whole workspace. */
  agentId?: string;
  limit?: number;
  offset?: number;
}

/**
 * One page of threads, newest activity first.
 *
 * `agentId` is a filter over the collection, not a different collection:
 * the server applies the workspace scope either way, so this narrows what
 * the workspace already owns and can never reach outside it.
 */
export async function listConversations(
  params: ListConversationsParams = {},
): Promise<ConversationPage> {
  const query = new URLSearchParams();
  if (params.agentId) query.set("agent_id", params.agentId);
  if (params.limit !== undefined) query.set("limit", String(params.limit));
  if (params.offset) query.set("offset", String(params.offset));
  const qs = query.toString();
  return apiFetch<ConversationPage>(`/chat/conversations${qs ? `?${qs}` : ""}`);
}

/**
 * Reads one thread by id.
 *
 * Needed because a link can name a conversation the list has not paged in
 * yet — a bookmark from three months ago is the ordinary case. 404 covers
 * both "deleted" and "belongs to another workspace"; the server does not
 * distinguish them and neither should the caller.
 */
export function getConversation(id: string): Promise<ApiConversation> {
  return apiFetch<ApiConversation>(`/chat/conversations/${id}`);
}

export function createConversation(
  agentId: string,
  title?: string,
  contextReferences?: readonly ContextReferenceInput[],
): Promise<ApiConversation> {
  return apiFetch<ApiConversation>(`/chat/conversations`, {
    method: "POST",
    body: JSON.stringify({
      agent_id: agentId,
      title: title ?? "",
      // Identity only. The backend resolves each one against the workspace
      // and writes the label itself, so a thread can never be created
      // holding a subject this workspace may not see.
      context_references: contextReferences ?? [],
    }),
  });
}

export function renameConversation(id: string, title: string): Promise<ApiConversation> {
  return apiFetch<ApiConversation>(`/chat/conversations/${id}`, {
    method: "PATCH",
    body: JSON.stringify({ title }),
  });
}

export function deleteConversation(id: string): Promise<void> {
  return apiFetch<void>(`/chat/conversations/${id}`, { method: "DELETE" });
}

export interface Transcript {
  items: ApiMessage[];
  /**
   * One receipt per assistant turn, keyed by message id, saying what the
   * SYSTEM knows that turn changed.
   *
   * Every assistant turn has an entry, including the ones where nothing
   * ran: a missing key would be indistinguishable from a transcript that
   * was never asked, and a client would have nothing to render against a
   * turn that merely CLAIMED a change.
   *
   * Optional on the wire because the read degrades rather than failing —
   * and when it is absent nothing is shown as confirmed, which errs the
   * safe way.
   */
  write_receipts?: Record<string, WriteReceipt>;
  /** Keyed by assistant message id. Present for every assistant turn,
   *  including the ones that read nothing — absence has to be a value. */
  read_receipts?: Record<string, ReadReceipt>;
  /**
   * The ceiling the server applied. A transcript that came back exactly
   * this long was cut: older turns exist above it. Reported rather than
   * hardcoded here so the two sides cannot drift apart.
   */
  limit: number;
}

/** Reads the trailing turns of a thread, in reading order. */
export function listMessages(id: string): Promise<Transcript> {
  return apiFetch<Transcript>(`/chat/conversations/${id}/messages`);
}

/**
 * Drops the message at `seq` and everything after it.
 *
 * Backs regenerate and edit: both mean "replace this turn". Appending the
 * question again instead would leave the thread reading as if it had been
 * asked twice, and would feed the model that duplicate forever after.
 * The backend only allows cutting from a user turn.
 */
export async function truncateFrom(id: string, seq: number): Promise<number> {
  const res = await apiFetch<{ deleted: number }>(`/chat/conversations/${id}/messages/${seq}`, {
    method: "DELETE",
  });
  return res.deleted;
}
