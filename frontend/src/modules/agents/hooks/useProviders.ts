/**
 * TanStack Query hooks for the Providers aggregate.
 *
 * Query keys embed the workspace id, matching the convention the finance
 * module already uses, so cache scoping survives the eventual switch to
 * real multi-workspace auth.
 *
 * No optimistic updates here: creating a provider is a rare, deliberate act
 * whose server response carries fields the client cannot predict
 * (`api_key_hint`, timestamps). Showing a guessed row and correcting it a
 * moment later would be worse than waiting for the round trip.
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
  createProvider,
  deleteProvider,
  listProviderModels,
  listProviders,
  updateProvider,
  type ApiModel,
  type ApiProvider,
  type CreateProviderRequest,
  type UpdateProviderRequest,
} from "@/modules/agents/api/providers";

export const providersRootKey = (workspaceId: string) => ["chat", "providers", workspaceId] as const;

export function useProviders(): UseQueryResult<ApiProvider[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: providersRootKey(workspaceId),
    queryFn: () => listProviders(),
    staleTime: 60_000,
  });
}

export function useCreateProvider(): UseMutationResult<ApiProvider, Error, CreateProviderRequest> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateProviderRequest) => createProvider(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: providersRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useUpdateProvider(): UseMutationResult<
  ApiProvider,
  Error,
  { id: string; body: UpdateProviderRequest }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateProviderRequest }) =>
      updateProvider(id, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: providersRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useDeleteProvider(): UseMutationResult<void, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteProvider(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: providersRootKey(getApiWorkspaceId()) });
    },
  });
}

/**
 * Fetches the provider's model catalog on demand.
 *
 * Exposed as a mutation rather than a query because it is an action the
 * user takes ("test this connection", "refresh the model list"), not state
 * the page should keep in sync — and because it costs a real round trip to
 * a third-party endpoint.
 */
export function useProviderModels(): UseMutationResult<ApiModel[], Error, string> {
  return useMutation({
    mutationFn: (id: string) => listProviderModels(id),
  });
}
