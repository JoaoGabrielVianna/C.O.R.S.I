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
 *
 * ── Why a PUBLISHED release is cached forever, and a draft is not ──────
 * The strongest statement this frontend can make about freshness, and it
 * is not a guess about how often anybody publishes: a published snapshot is
 * IMMUTABLE BY DATABASE TRIGGER. Re-reading one cannot return a different
 * answer, so `staleTime: Infinity` is not a tradeoff — it is the invariant,
 * written down where the cache can act on it.
 *
 * A DRAFT is not immutable, so it does not get that treatment. The single
 * release read therefore decides per record, from the status the server
 * returned, rather than assuming every row on this route is history.
 *
 * `Infinity` and not `'static'`: static queries opt out of invalidation
 * too, and publishing must still be able to invalidate the two indexes
 * below — otherwise the page would keep describing the release it just
 * superseded.
 *
 * ── Why the module index is Infinity as well ───────────────────────────
 * `current` and `release_count` are derived over the whole set, so they
 * change exactly when something is published. Every publish in this UI goes
 * through `usePublishRelease`, which invalidates both. A release published
 * from outside the browser (a migration, as Palace v0.0.1 was) will not
 * appear until the page is reloaded — the same limit the previous one-hour
 * value had for the first hour, stated rather than implied.
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

/** A draft can still change, so an hour is the ceiling for one. */
const COLD = 60 * 60 * 1000;

/**
 * History is not re-read within a session.
 *
 * `gcTime` matches `staleTime` on purpose: with the default five minutes,
 * walking away from the release history and coming back discards the cache
 * entry and fetches it again — a request to re-learn something that cannot
 * have changed. The set is bounded by the number of published releases,
 * which is twelve.
 */
const FOREVER = Infinity;

export function useReleaseModules(): UseQueryResult<ApiModuleCard[]> {
  return useQuery({
    queryKey: modulesKey(),
    queryFn: ({ signal }) => listModules(signal),
    staleTime: FOREVER,
    gcTime: FOREVER,
  });
}

export function useReleaseModule(key: string): UseQueryResult<ApiModuleDetail> {
  return useQuery({
    queryKey: moduleKey(key),
    queryFn: ({ signal }) => getModule(key, signal),
    staleTime: FOREVER,
    gcTime: FOREVER,
    enabled: key.length > 0,
  });
}

export function useRelease(key: string, version: string): UseQueryResult<ApiRelease> {
  return useQuery({
    queryKey: releaseKey(key, version),
    queryFn: ({ signal }) => getRelease(key, version, signal),
    // Published snapshots are immutable by trigger. Drafts are not, and the
    // status the server returned is what decides which of the two this is.
    staleTime: (query) => (query.state.data?.status === "published" ? FOREVER : COLD),
    gcTime: FOREVER,
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
