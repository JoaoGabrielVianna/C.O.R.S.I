/**
 * Where an agent stands against its daily limits.
 *
 * Configuration is written through `useUpdateAgent` — a budget is part of
 * the agent — and only the *standing* has a query of its own, because it
 * changes on every turn while the configuration changes almost never.
 */

import { useQuery, type UseQueryResult } from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import { getAgentBudget, type ApiBudgetStatus } from "@/modules/agents/api/agents";

export const budgetKey = (workspaceId: string, agentId: string) =>
  ["chat", "budget", workspaceId, agentId] as const;

export function useAgentBudget(agentId: string): UseQueryResult<ApiBudgetStatus> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: budgetKey(workspaceId, agentId),
    queryFn: () => getAgentBudget(agentId),
    // Consumption moves with every turn, so this is deliberately short-
    // lived. It is a small read against an index the gate already uses.
    staleTime: 5_000,
  });
}
