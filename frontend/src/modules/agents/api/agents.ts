/**
 * Typed REST client for /chat/agents.
 *
 * An agent is a named configuration of one model: which provider, which
 * system prompt, how it samples, and how much history it is fed.
 * Conversations point at an agent, so editing one changes every future
 * turn without rewriting the past ones.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

/**
 * Daily limits for one agent. Null is no limit; zero is a limit of zero,
 * which freezes the agent on purpose. The two are never the same thing.
 */
export interface ApiBudget {
  daily_token_limit: number | null;
  daily_cost_limit_usd: number | null;
}

/** How one limit is doing against the day so far. */
export interface ApiBudgetDimension {
  limit: number;
  used: number;
  remaining: number;
  /** 0..1, and can exceed 1 after an overshoot. */
  percent: number;
  state: "disabled" | "ok" | "warning" | "blocked";
}

/**
 * Why a turn was refused, in a form to branch on. These are the same
 * strings the API returns as the error `code`, so the chat can explain a
 * refusal without reading the sentence.
 */
export type BudgetBlockReason =
  | "token_limit_reached"
  | "cost_limit_reached"
  | "pricing_unavailable";

export interface ApiBudgetStatus {
  budget: ApiBudget;
  window: { from: string; to: string; timezone: string };
  usage: {
    tokens: number;
    /** A floor, not a total, whenever `unpriced_turns` is above zero. */
    known_cost_usd: number;
    unpriced_turns: number;
    turns: number;
  };
  status: {
    enabled: boolean;
    blocked: boolean;
    reason?: BudgetBlockReason;
    tokens?: ApiBudgetDimension;
    cost?: ApiBudgetDimension;
    unpriced_turns: number;
    /** False when the day contains turns whose cost nobody could read. */
    certain: boolean;
    next_turn_fits?: boolean;
  };
}

/**
 * Whether this agent may be asked to propose memories, and the user's
 * addendum to the rules such a proposal follows.
 *
 * `off` refuses the request outright. `on_request` allows it, and only when
 * the user asks: there is no automatic mode in this version, and the agent
 * never decides on its own to remember something.
 *
 * `notes` is an ADDITION to the policy the product ships, never a
 * replacement for it. What is worth remembering is decided by the backend;
 * this is where a person says "for this agent, also keep track of X".
 */
export interface ApiMemoryPolicy {
  mode: "off" | "on_request";
  notes: string;
}

export interface ApiAgent {
  id: string;
  workspace_id: string;
  provider_id: string;
  name: string;
  description: string;
  system_prompt: string;
  model: string;
  temperature: number;
  max_tokens: number;
  history_limit: number;
  accent: string;
  budget: ApiBudget;
  memory_policy: ApiMemoryPolicy;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface CreateAgentRequest {
  provider_id: string;
  name: string;
  description?: string;
  system_prompt?: string;
  /** Blank inherits the provider's default_model. */
  model?: string;
  temperature?: number;
  max_tokens?: number;
  history_limit?: number;
  accent?: string;
  daily_token_limit?: number | null;
  daily_cost_limit_usd?: number | null;
  budget?: ApiBudget;
  /** Omitted means the agent is born with the backend's default policy. */
  memory_policy?: ApiMemoryPolicy;
}

export type UpdateAgentRequest = Partial<
  Omit<CreateAgentRequest, "budget" | "memory_policy">
> & {
  /**
   * Omitted leaves the limits untouched; present replaces both, so a null
   * member removes that limit. "Not mentioned" and "set to no limit" are
   * different requests and stay different on the wire.
   */
  budget?: ApiBudget;
  /**
   * Same rule: omitted leaves the policy exactly as it was. The settings
   * page saves the form and this card separately, so most saves do not
   * mention it — and a save of the instructions must never clear it.
   */
  memory_policy?: ApiMemoryPolicy;
};

/** Where this agent stands against its daily limits, right now. */
export function getAgentBudget(id: string): Promise<ApiBudgetStatus> {
  return apiFetch<ApiBudgetStatus>(`/chat/agents/${id}/budget`);
}

export async function listAgents(): Promise<ApiAgent[]> {
  const env = await apiFetch<PageEnvelope<ApiAgent>>(`/chat/agents`);
  return env.items;
}

export function createAgent(body: CreateAgentRequest): Promise<ApiAgent> {
  return apiFetch<ApiAgent>(`/chat/agents`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateAgent(id: string, body: UpdateAgentRequest): Promise<ApiAgent> {
  return apiFetch<ApiAgent>(`/chat/agents/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteAgent(id: string): Promise<void> {
  return apiFetch<void>(`/chat/agents/${id}`, { method: "DELETE" });
}
