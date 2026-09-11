/**
 * TanStack Query hooks for Agent Sources.
 *
 * Keyed by agent, like memories: two agents never share a cache entry, the
 * same way they never share a row.
 *
 * Every mutation invalidates the whole agent's list rather than patching
 * the cache. `in_context` and `used_characters` are computed by the server
 * over the *whole* set — enabling one source can push another out of the
 * budget — so a local patch would leave the page describing a state that no
 * longer exists.
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
  createSource,
  deleteSource,
  getSource,
  listSources,
  updateSource,
  type ApiSource,
  type CreateSourceRequest,
  type SourcePage,
  type UpdateSourceRequest,
} from "@/modules/agents/api/sources";

export const sourcesKey = (workspaceId: string, agentId: string) =>
  ["chat", "sources", workspaceId, agentId] as const;

export const sourceKey = (workspaceId: string, agentId: string, id: string) =>
  ["chat", "sources", workspaceId, agentId, id] as const;

export function useSources(agentId: string): UseQueryResult<SourcePage> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: sourcesKey(workspaceId, agentId),
    queryFn: () => listSources(agentId),
    staleTime: 10_000,
  });
}

/**
 * One source, with its text.
 *
 * A separate query because the list deliberately leaves documents in the
 * database. `enabled` is false while the editor is composing a new source,
 * which has no id to read.
 */
export function useSource(agentId: string, id: string | undefined): UseQueryResult<ApiSource> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: sourceKey(workspaceId, agentId, id ?? ""),
    queryFn: () => getSource(agentId, id as string),
    enabled: Boolean(id),
    // The editor is the only reader, and it should always open the text
    // that is actually stored rather than one a stale cache remembers.
    staleTime: 0,
  });
}

function useInvalidateSources(agentId: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: sourcesKey(getApiWorkspaceId(), agentId) });
  };
}

export function useCreateSource(
  agentId: string,
): UseMutationResult<ApiSource, Error, CreateSourceRequest> {
  const invalidate = useInvalidateSources(agentId);
  return useMutation({
    mutationFn: (body: CreateSourceRequest) => createSource(agentId, body),
    onSuccess: invalidate,
  });
}

export function useUpdateSource(
  agentId: string,
): UseMutationResult<ApiSource, Error, { id: string; body: UpdateSourceRequest }> {
  const invalidate = useInvalidateSources(agentId);
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateSourceRequest }) =>
      updateSource(agentId, id, body),
    onSuccess: invalidate,
  });
}

export function useDeleteSource(agentId: string): UseMutationResult<void, Error, string> {
  const invalidate = useInvalidateSources(agentId);
  return useMutation({
    mutationFn: (id: string) => deleteSource(agentId, id),
    onSuccess: invalidate,
  });
}
