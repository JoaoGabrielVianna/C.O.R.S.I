/**
 * TanStack Query hooks for the release history.
 *
 * ── Why the query keys carry no workspace id ───────────────────────────
 * Every other module in this frontend keys its cache by workspace, because
 * every other module reads tenant data. Releases describe the deployed
 * build: the same rows are true for every workspace, and adding the id to
 * the key would create one cache entry per workspace holding identical
 * content. The backend has a test that pins this — two workspace headers
 * must receive byte-identical history.
 *
 * ── Why the data is treated as cold ────────────────────────────────────
 * A published release never changes. Refetching it on every window focus
 * would be requests spent to confirm that history is still history.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import {
  getModule,
  getRelease,
  listModules,
  publishRelease,
  type ApiModuleCard,
  type ApiModuleDetail,
  type ApiRelease,
} from "@/modules/releases/api/releases";

export const modulesKey = () => ["releases", "modules"] as const;
export const moduleKey = (key: string) => ["releases", "module", key] as const;
export const releaseKey = (key: string, version: string) =>
  ["releases", "release", key, version] as const;

/** Published history does not change; an hour of staleness costs nothing. */
const COLD = 60 * 60 * 1000;

export function useReleaseModules(): UseQueryResult<ApiModuleCard[]> {
  return useQuery({
    queryKey: modulesKey(),
    queryFn: ({ signal }) => listModules(signal),
    staleTime: COLD,
  });
}

export function useReleaseModule(key: string): UseQueryResult<ApiModuleDetail> {
  return useQuery({
    queryKey: moduleKey(key),
    queryFn: ({ signal }) => getModule(key, signal),
    staleTime: COLD,
    enabled: key.length > 0,
  });
}

export function useRelease(key: string, version: string): UseQueryResult<ApiRelease> {
  return useQuery({
    queryKey: releaseKey(key, version),
    queryFn: ({ signal }) => getRelease(key, version, signal),
    staleTime: COLD,
    enabled: key.length > 0 && version.length > 0,
  });
}

/**
 * Publishing changes which release is current, so the module list and the
 * module detail are both invalidated rather than patched locally: `current`
 * and `release_count` are derived server-side over the whole set, and a
 * local guess would leave the page describing a state the backend does not
 * agree with.
 */
export function usePublishRelease(
  key: string,
): UseMutationResult<ApiRelease, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (version: string) => publishRelease(key, version),
    onSuccess: (rel) => {
      qc.setQueryData(releaseKey(key, rel.version), rel);
      void qc.invalidateQueries({ queryKey: modulesKey() });
      void qc.invalidateQueries({ queryKey: moduleKey(key) });
    },
  });
}
