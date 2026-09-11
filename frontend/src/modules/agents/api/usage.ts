/**
 * Typed REST client for the usage/spend endpoints.
 *
 * Two different numbers live here, and they must not be confused:
 *
 *   - Usage (workspace/agent/conversation) is OURS: the token counts each
 *     turn recorded, priced at the rate that turn was stamped with when it
 *     ran. It is called an estimate because it is priced from a rate card
 *     rather than from an invoice — not because it is recomputed. It is
 *     not: reading the same window tomorrow returns the same number even
 *     if the model's price changed overnight.
 *   - Spend (provider) is the REAL billed amount, read straight from the
 *     gateway's record for that virtual key. It lags a few minutes behind a
 *     call, the way the gateway's own dashboard does.
 *
 * Neither falls back to the other. Seeing them disagree is the signal.
 *
 * ── Unknown is not zero ────────────────────────────────────────────────
 * A turn whose price could not be read carries no cost at all, and is
 * counted in `unpriced_messages` instead of quietly adding 0.00 to the
 * total. `priced` is false exactly when that count is above zero, and it
 * means "this total understates" — never "the gateway was down".
 */

import { apiFetch } from "@/lib/api/client";

export interface UsageLine {
  model: string;
  messages: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  /** Null when the group spans a price change, or carries no price at all. */
  input_cost_per_token: number | null;
  output_cost_per_token: number | null;
  /** Sums only the turns that recorded a cost. */
  cost: number;
  /** How many turns in this line have no known cost. */
  unpriced_messages: number;
  provider_messages: number;
  estimated_messages: number;
  unknown_usage_messages: number;
}

export interface UsageReport {
  currency: string;
  /** The window this report covers, echoed back. Null bounds are open. */
  from: string | null;
  to: string | null;
  /** False when at least one turn has no known cost — the total is short. */
  priced: boolean;
  lines: UsageLine[];
  messages: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_tokens: number;
  estimated_cost: number;
  provider_messages: number;
  estimated_messages: number;
  unknown_usage_messages: number;
  /** How many turns in this report carry no cost. Zero means the total is whole. */
  unpriced_messages: number;
}

export interface ProviderSpend {
  key_alias: string;
  /** Real billed spend for this key, in the gateway's currency (USD). */
  spend: number;
  /** Null when the key is uncapped. */
  max_budget: number | null;
  models: string[];
}

/**
 * An absolute, half-open window: `from` inclusive, `to` exclusive.
 *
 * Both are RFC 3339 instants because "today" depends on where the reader
 * is. The caller knows its own timezone and sends the two moments it means;
 * the server never decides where a day starts.
 */
export interface UsageWindow {
  from?: string;
  to?: string;
}

function windowQuery(w: UsageWindow | undefined): string {
  if (!w?.from && !w?.to) return "";
  const q = new URLSearchParams();
  if (w.from) q.set("from", w.from);
  if (w.to) q.set("to", w.to);
  return `?${q.toString()}`;
}

export function getWorkspaceUsage(w?: UsageWindow): Promise<UsageReport> {
  return apiFetch<UsageReport>(`/chat/usage${windowQuery(w)}`);
}

export function getConversationUsage(id: string, w?: UsageWindow): Promise<UsageReport> {
  return apiFetch<UsageReport>(`/chat/conversations/${id}/usage${windowQuery(w)}`);
}

export function getAgentUsage(id: string, w?: UsageWindow): Promise<UsageReport> {
  return apiFetch<UsageReport>(`/chat/agents/${id}/usage${windowQuery(w)}`);
}

/**
 * Reads the provider key's real spend from the gateway. Allowed a longer
 * timeout: it is a live round trip to a third-party endpoint.
 */
export function getProviderSpend(id: string): Promise<ProviderSpend> {
  return apiFetch<ProviderSpend>(`/chat/providers/${id}/spend`, { timeoutMs: 20_000 });
}
