/**
 * TanStack Query hooks for Tools.
 *
 * Keyed by agent, like memories and sources: two agents never share a cache
 * entry, the same way they never share a grant.
 *
 * Both mutations return the whole catalogue, so the cache is replaced with
 * the server's answer instead of patched locally. `authorized_count` and
 * `stale` are computed server-side over the whole set, and a local patch
 * would leave the page describing a state the backend does not agree with.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  authorizeTool,
  listAgentTools,
  listConversationToolCalls,
  revokeTool,
  type ApiToolCall,
  type ToolsReport,
} from "@/modules/agents/api/tools";

export const toolsKey = (workspaceId: string, agentId: string) =>
  ["chat", "tools", workspaceId, agentId] as const;

export const toolCallsKey = (workspaceId: string, conversationId: string) =>
  ["chat", "tool-calls", workspaceId, conversationId] as const;

export function useAgentTools(agentId: string): UseQueryResult<ToolsReport> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: toolsKey(workspaceId, agentId),
    queryFn: () => listAgentTools(agentId),
    // The composer asks for this before it knows whether it has an agent —
    // a thread whose agent failed to load still renders. Without the guard
    // that becomes a request to `/chat/agents//tools`, which is a 400 the
    // page would then have to explain.
    enabled: Boolean(agentId),
    // The catalogue only changes on a deploy or on a grant, and a grant
    // writes the fresh answer straight into the cache.
    staleTime: 60_000,
  });
}

export function useSetToolAuthorization(
  agentId: string,
): UseMutationResult<ToolsReport, Error, { toolName: string; authorized: boolean }> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ toolName, authorized }: { toolName: string; authorized: boolean }) => {
      if (authorized) return authorizeTool(agentId, toolName);
      await revokeTool(agentId, toolName);
      return listAgentTools(agentId);
    },
    onSuccess: (report) => {
      qc.setQueryData(toolsKey(getApiWorkspaceId(), agentId), report);
    },
  });
}

/**
 * The audit trail of one thread.
 *
 * `enabled` is the whole point: the transcript passes false until it finds
 * a turn whose context report carries rounds. A conversation that never ran
 * a tool never issues this request.
 */
export function useConversationToolCalls(
  conversationId: string,
  enabled: boolean,
): UseQueryResult<ApiToolCall[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: toolCallsKey(workspaceId, conversationId),
    queryFn: () => listConversationToolCalls(conversationId),
    enabled: enabled && Boolean(conversationId),
    staleTime: 10_000,
  });
}
