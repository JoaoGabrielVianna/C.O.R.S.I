/**
 * TanStack Query hooks for usage and spend.
 *
 * Usage is a read of figures the database already froze, so it is a plain
 * query with a short stale time and no network call to the gateway behind
 * it. Provider spend IS a live round trip that lags reality by minutes
 * anyway, so it is cached longer and never refetched on window focus —
 * hammering the gateway would not make the number any fresher.
 *
 * ── Where "today" comes from ───────────────────────────────────────────
 * The server takes two absolute instants and refuses to guess where a day
 * begins. This browser knows, so it is the one that computes local midnight
 * and sends it. Nothing here invents a timezone policy.
 */

import { useQueries, useQuery, type UseQueryResult } from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  getAgentUsage,
  getConversationUsage,
  getProviderSpend,
  getWorkspaceUsage,
  type ProviderSpend,
  type UsageReport,
  type UsageWindow,
} from "@/modules/agents/api/usage";

/** The named ranges the UI offers. `all` is the lifetime total. */
export type UsagePeriod = "today" | "7d" | "30d" | "all";

export const USAGE_PERIOD_LABEL: Record<UsagePeriod, string> = {
  today: "Hoje",
  "7d": "7 dias",
  "30d": "30 dias",
  all: "Tudo",
};

/**
 * Turns a named range into the half-open window the API takes.
 *
 * Anchored on local midnight rather than "24 hours ago": a turn from 23:00
 * last night belongs to yesterday at 08:00 today, and an elapsed-hours
 * cutoff would bill it to the wrong day. `to` is left open so a turn
 * finishing while the page is up still counts.
 */
export function usageWindow(period: UsagePeriod, now: Date = new Date()): UsageWindow {
  if (period === "all") return {};
  const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const days = period === "today" ? 0 : period === "7d" ? 6 : 29;
  midnight.setDate(midnight.getDate() - days);
  return { from: midnight.toISOString() };
}

/**
 * The window key must be a value, not a Date: a fresh `new Date()` on every
 * render would give the query a new key every render.
 */
const windowKey = (w: UsageWindow) => `${w.from ?? ""}..${w.to ?? ""}`;

export function useWorkspaceUsage(w: UsageWindow = {}): UseQueryResult<UsageReport> {
  const ws = getApiWorkspaceId();
  return useQuery({
    queryKey: ["chat", "usage", "workspace", ws, windowKey(w)],
    queryFn: () => getWorkspaceUsage(w),
    staleTime: 15_000,
  });
}

export function useAgentUsage(
  agentId: string | null,
  w: UsageWindow = {},
): UseQueryResult<UsageReport> {
  const ws = getApiWorkspaceId();
  return useQuery({
    queryKey: ["chat", "usage", "agent", ws, agentId, windowKey(w)],
    queryFn: () => getAgentUsage(agentId as string, w),
    enabled: !!agentId,
    staleTime: 15_000,
  });
}

/**
 * Usage for several agents at once — what the Home cards read.
 *
 * Deliberately the same query keys as `useAgentUsage`, so a screen showing
 * both the per-agent figure and an aggregate fetches each report once and
 * reads the second view straight from the cache.
 */
export function useAgentsUsage(
  agentIds: string[],
  w: UsageWindow = {},
): Array<UseQueryResult<UsageReport>> {
  const ws = getApiWorkspaceId();
  return useQueries({
    queries: agentIds.map((id) => ({
      queryKey: ["chat", "usage", "agent", ws, id, windowKey(w)],
      queryFn: () => getAgentUsage(id, w),
      staleTime: 15_000,
    })),
  });
}

export function useConversationUsage(
  conversationId: string | null,
): UseQueryResult<UsageReport> {
  const ws = getApiWorkspaceId();
  return useQuery({
    queryKey: ["chat", "usage", "conversation", ws, conversationId],
    queryFn: () => getConversationUsage(conversationId as string),
    enabled: !!conversationId,
    staleTime: 15_000,
  });
}

export function useProviderSpend(providerId: string | null): UseQueryResult<ProviderSpend> {
  const ws = getApiWorkspaceId();
  return useQuery({
    queryKey: ["chat", "spend", "provider", ws, providerId],
    queryFn: () => getProviderSpend(providerId as string),
    enabled: !!providerId,
    staleTime: 120_000,
    refetchOnWindowFocus: false,
    // The gateway can be down without the module being broken; don't retry
    // a live third-party call into the ground.
    retry: 1,
  });
}
