/**
 * TanStack Query hooks for the Agents aggregate.
 *
 * Agents change rarely and are read on nearly every screen in the module
 * (the picker, the thread header, the settings tab), so the list is cached
 * with a generous staleTime and invalidated wholesale on write.
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
  createAgent,
  deleteAgent,
  listAgents,
  updateAgent,
  type ApiAgent,
  type CreateAgentRequest,
  type UpdateAgentRequest,
} from "@/modules/agents/api/agents";

export const agentsRootKey = (workspaceId: string) => ["chat", "agents", workspaceId] as const;

export function useAgents(): UseQueryResult<ApiAgent[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: agentsRootKey(workspaceId),
    queryFn: () => listAgents(),
    staleTime: 60_000,
  });
}

export function useCreateAgent(): UseMutationResult<ApiAgent, Error, CreateAgentRequest> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateAgentRequest) => createAgent(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: agentsRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useUpdateAgent(): UseMutationResult<
  ApiAgent,
  Error,
  { id: string; body: UpdateAgentRequest }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateAgentRequest }) => updateAgent(id, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: agentsRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useDeleteAgent(): UseMutationResult<void, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteAgent(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: agentsRootKey(getApiWorkspaceId()) });
    },
  });
}
