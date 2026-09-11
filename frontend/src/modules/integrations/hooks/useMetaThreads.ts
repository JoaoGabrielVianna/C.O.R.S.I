/**
 * TanStack Query hooks for the Meta Threads integration.
 *
 * ── One query, three mutations ─────────────────────────────────────────
 * The status read touches one row and talks to nobody, so the card renders
 * from it immediately. Everything that costs a round trip to Meta —
 * connecting, refreshing — is a mutation the operator triggers, never
 * something a page load performs.
 *
 * There is deliberately no hook that READS Meta content. Posts and metrics
 * are for agents, through tools, inside a conversation. A settings page
 * that listed the operator's posts would be a second reader of the same
 * data with its own idea of what a page of history is.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { ApiError } from "@/lib/api/client";
import { getApiWorkspaceId } from "@/lib/api/workspace";
import type { Translations } from "@/lib/i18n/pt";
import {
  connectMetaThreads,
  disconnectMetaThreads,
  getMetaThreadsStatus,
  refreshMetaThreads,
  META_THREADS_NOT_CONFIGURED,
  META_THREADS_NOT_CONNECTED,
  META_THREADS_SCOPE_MISSING,
  META_THREADS_TOKEN_EXPIRED,
  META_THREADS_UPSTREAM,
  type MetaThreadsConnection,
  type MetaThreadsStatus,
} from "../api/metaThreads";

export const metaThreadsStatusKey = (workspaceId: string) =>
  ["integrations", "meta-threads", "status", workspaceId] as const;

export function useMetaThreadsStatus(): UseQueryResult<MetaThreadsStatus> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: metaThreadsStatusKey(workspaceId),
    queryFn: getMetaThreadsStatus,
    staleTime: 10_000,
  });
}

function invalidate(qc: ReturnType<typeof useQueryClient>, workspaceId: string) {
  void qc.invalidateQueries({ queryKey: ["integrations", "meta-threads"] });
  void qc.invalidateQueries({ queryKey: metaThreadsStatusKey(workspaceId) });
}

export interface ConnectArgs {
  code: string;
  redirectUri: string;
}

export function useConnectMetaThreads(): UseMutationResult<
  { connection: MetaThreadsConnection },
  Error,
  ConnectArgs
> {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  return useMutation({
    mutationFn: ({ code, redirectUri }: ConnectArgs) => connectMetaThreads(code, redirectUri),
    onSuccess: () => invalidate(qc, workspaceId),
  });
}

export function useRefreshMetaThreads(): UseMutationResult<
  { connection: MetaThreadsConnection },
  Error,
  void
> {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  return useMutation({
    mutationFn: () => refreshMetaThreads(),
    onSuccess: () => invalidate(qc, workspaceId),
  });
}

export function useDisconnectMetaThreads(): UseMutationResult<void, Error, void> {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  return useMutation({
    mutationFn: () => disconnectMetaThreads(),
    onSuccess: () => invalidate(qc, workspaceId),
  });
}

export interface MetaThreadsFailure {
  text: string;
  technical: string | null;
  code: string | null;
}

/**
 * A failure, in the reader's language, with the server's own wording kept
 * beneath it whenever the two differ.
 *
 * ── Why the server's sentence survives translation ─────────────────────
 * Because the backend's refusals carry facts the UI cannot restate: which
 * scope is missing, the exact moment a refresh becomes possible, what Meta
 * itself said. Replacing those with friendlier text would cost the ability
 * to act on them.
 *
 * `not_connected` returns null: it is a STATE the card renders as its own
 * layout, not an error to put in a red box.
 */
export function metaThreadsErrorMessage(
  error: unknown,
  t: Translations,
): MetaThreadsFailure | null {
  if (!(error instanceof ApiError)) {
    return error instanceof Error ? { text: error.message, technical: null, code: null } : null;
  }
  if (error.code === META_THREADS_NOT_CONNECTED) return null;

  const known = t.app.settings.metaThreads.errors;
  const byCode: Record<string, string> = {
    [META_THREADS_NOT_CONFIGURED]: known.notConfigured,
    [META_THREADS_TOKEN_EXPIRED]: known.tokenExpired,
    [META_THREADS_SCOPE_MISSING]: known.scopeMissing,
    [META_THREADS_UPSTREAM]: known.upstream,
  };

  const translated = byCode[error.code];
  return {
    text: translated ?? error.message,
    technical: translated ? error.message : null,
    code: error.code,
  };
}
