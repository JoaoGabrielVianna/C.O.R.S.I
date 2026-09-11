/**
 * TanStack Query hooks for Agent Memory.
 *
 * Keyed by agent, because memory is scoped to the agent: two agents never
 * share a cache entry, the same way they never share a row.
 *
 * Every mutation invalidates the whole agent's list rather than patching the
 * cache. `in_context` and `used_characters` are computed by the server over
 * the *whole* set — editing one memory can push another out of the budget —
 * so a local patch would leave the counter describing a state that no longer
 * exists.
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
  createMemory,
  deleteMemory,
  listMemories,
  updateMemory,
  type ApiMemory,
  type CreateMemoryRequest,
  type MemoryPage,
  type UpdateMemoryRequest,
} from "@/modules/agents/api/memories";

export const memoriesKey = (workspaceId: string, agentId: string) =>
  ["chat", "memories", workspaceId, agentId] as const;

export function useMemories(agentId: string): UseQueryResult<MemoryPage> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: memoriesKey(workspaceId, agentId),
    queryFn: () => listMemories(agentId),
    // Short: a memory saved from the chat has to show up on the Memory page
    // without a reload, and the payload is small.
    staleTime: 10_000,
  });
}

function useInvalidateMemories(agentId: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: memoriesKey(getApiWorkspaceId(), agentId) });
  };
}

export function useCreateMemory(
  agentId: string,
): UseMutationResult<ApiMemory, Error, CreateMemoryRequest> {
  const invalidate = useInvalidateMemories(agentId);
  return useMutation({
    mutationFn: (body: CreateMemoryRequest) => createMemory(agentId, body),
    onSuccess: invalidate,
  });
}

export function useUpdateMemory(
  agentId: string,
): UseMutationResult<ApiMemory, Error, { id: string; body: UpdateMemoryRequest }> {
  const invalidate = useInvalidateMemories(agentId);
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateMemoryRequest }) =>
      updateMemory(agentId, id, body),
    onSuccess: invalidate,
  });
}

export function useDeleteMemory(agentId: string): UseMutationResult<void, Error, string> {
  const invalidate = useInvalidateMemories(agentId);
  return useMutation({
    mutationFn: (id: string) => deleteMemory(agentId, id),
    onSuccess: invalidate,
  });
}
