/**
 * Typed REST client for /chat/agents/{id}/tools and the audit trail.
 *
 * ── What this client can and cannot do ─────────────────────────────────
 * It authorizes and revokes capabilities the backend already implements.
 * There is deliberately no "create tool" call, because a tool is code: its
 * name, description and input contract ship with the binary. An endpoint
 * that accepted an arbitrary name and schema would let this file describe
 * an executor that does not exist.
 *
 * ── The default is DENY ────────────────────────────────────────────────
 * The catalogue comes back with every tool the build has, each flagged
 * `authorized` or not. An agent with nothing authorized declares no tools
 * to the model and behaves exactly as it did before this feature existed.
 */

import { apiFetch } from "@/lib/api/client";

/** The scalar types the backend's schema subset validates. */
export type ToolPropertyType = "string" | "number" | "integer" | "boolean";

export interface ApiToolProperty {
  type: ToolPropertyType;
  description?: string;
  max_length?: number;
}

export interface ApiToolSchema {
  properties: Record<string, ApiToolProperty>;
  required?: string[];
}

export interface ApiTool {
  /** The canonical, machine-readable name, e.g. `system.echo`. */
  name: string;
  /** The human name, for the row. */
  title: string;
  /** What it does. Written for the model; readable by a person. */
  description: string;
  /** `read` observes; `write` changes something. */
  effect: "read" | "write";
  /**
   * A tool that exists to exercise the machinery rather than to be useful.
   * It is shown, labelled as such: a capability you cannot see is one you
   * cannot revoke.
   */
  internal: boolean;
  schema: ApiToolSchema;
  /** Whether THIS agent may use it. Registry membership is not permission. */
  authorized: boolean;
}

export interface ToolsReport {
  items: ApiTool[];
  authorized_count: number;
  /**
   * Grants whose tool no longer exists in this build — a name authorized
   * before a deploy removed it. It grants nothing, and it is surfaced
   * rather than swallowed so it can be cleaned up.
   */
  stale?: string[];
}

/** One executed (or refused) tool call, as the audit trail stored it. */
export interface ApiToolCall {
  id: string;
  message_id: string;
  conversation_id: string;
  /** Which provider round asked for it, 1-based. */
  round: number;
  provider_call_id: string;
  tool_name: string;
  /** `null` means the payload was not retained. See `redacted`. */
  arguments: string | null;
  result: string | null;
  /**
   * `not_executed` is a call the runtime refused before it ran: an
   * unauthorized capability, arguments that did not validate. It is
   * told apart from `error` because a write receipt has to be able to
   * say whether something was ATTEMPTED, and code that only needs to
   * know it did not succeed should test for `!== "ok"`.
   */
  status: "ok" | "error" | "not_executed";
  error_code?: string;
  error_message?: string;
  duration_ms: number;
  /** Nothing redacts anything today; the field is the affordance for later. */
  redacted: boolean;
  created_at: string;
}

export function listAgentTools(agentId: string): Promise<ToolsReport> {
  return apiFetch<ToolsReport>(`/chat/agents/${agentId}/tools`);
}

/** Grants a tool. Granting twice is the same state as granting once. */
export function authorizeTool(agentId: string, toolName: string): Promise<ToolsReport> {
  return apiFetch<ToolsReport>(`/chat/agents/${agentId}/tools`, {
    method: "POST",
    body: JSON.stringify({ tool_name: toolName }),
  });
}

/** Revokes a grant. Revoking one that does not exist is not an error. */
export function revokeTool(agentId: string, toolName: string): Promise<void> {
  return apiFetch<void>(`/chat/agents/${agentId}/tools/${toolName}`, { method: "DELETE" });
}

/**
 * The tool calls of one conversation.
 *
 * A separate read rather than a field on every message, and the transcript
 * only issues it when a turn's context report says there were rounds — so a
 * thread that never used a tool never pays a request to find that out.
 */
export function listConversationToolCalls(conversationId: string): Promise<ApiToolCall[]> {
  return apiFetch<{ items: ApiToolCall[] }>(
    `/chat/conversations/${conversationId}/tool-calls`,
  ).then((page) => page.items ?? []);
}
