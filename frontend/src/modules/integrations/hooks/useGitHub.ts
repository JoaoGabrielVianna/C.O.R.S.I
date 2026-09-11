/**
 * TanStack Query hooks for the GitHub integration.
 *
 * ── Three queries, not one ─────────────────────────────────────────────
 * The status read touches two of our tables and talks to nobody, so the
 * page renders from it immediately. The repository picker and the activity
 * feed each cost a round trip to GitHub. Collapsing them would mean the
 * card cannot appear until GitHub answers — and would make a rate limit
 * look like a broken page rather than a slow list.
 *
 * ── Why mutations invalidate rather than patch ─────────────────────────
 * Authorizing repositories changes what the activity feed reads and what
 * the picker shows as selected, and the server recomputes the derived
 * organization list. A local patch would leave the page describing a state
 * the backend does not have.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { ApiError } from "@/lib/api/client";
import type { Translations } from "@/lib/i18n/pt";
import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  connectGitHub,
  disconnectGitHub,
  getGitHubActivity,
  getGitHubStatus,
  listAvailableRepositories,
  setAuthorizedRepositories,
  GITHUB_NOT_CONNECTED,
  type GitHubActivity,
  type GitHubAvailableRepositories,
  type GitHubConnection,
  type GitHubStatus,
} from "../api/github";

export const githubStatusKey = (workspaceId: string) =>
  ["integrations", "github", "status", workspaceId] as const;

export const githubReposKey = (workspaceId: string) =>
  ["integrations", "github", "repositories", workspaceId] as const;

export const githubActivityKey = (workspaceId: string) =>
  ["integrations", "github", "activity", workspaceId] as const;

export function useGitHubStatus(): UseQueryResult<GitHubStatus> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: githubStatusKey(workspaceId),
    queryFn: getGitHubStatus,
    staleTime: 10_000,
  });
}

/**
 * What the credential can reach.
 *
 * `enabled` gates it on being connected: without that, a disconnected
 * workspace would issue a request whose only possible answer is
 * `github_not_connected`, and the page would render an error for a state
 * that is not an error.
 *
 * Retries are off. The failures this call has — a revoked token, a rate
 * limit — are all ones that repeating makes worse rather than better.
 */
export function useGitHubRepositories(enabled: boolean): UseQueryResult<GitHubAvailableRepositories> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: githubReposKey(workspaceId),
    queryFn: listAvailableRepositories,
    enabled,
    retry: false,
    staleTime: 60_000,
  });
}

export function useGitHubActivity(enabled: boolean): UseQueryResult<GitHubActivity> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: githubActivityKey(workspaceId),
    queryFn: getGitHubActivity,
    enabled,
    retry: false,
    staleTime: 60_000,
  });
}

/** Every GitHub cache entry of this workspace. Used after connect and
 *  disconnect, both of which change the answer to all three queries. */
function invalidateAll(qc: ReturnType<typeof useQueryClient>, workspaceId: string) {
  void qc.invalidateQueries({ queryKey: ["integrations", "github"] });
  void qc.invalidateQueries({ queryKey: githubStatusKey(workspaceId) });
}

export function useConnectGitHub(): UseMutationResult<GitHubConnection, Error, string> {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  return useMutation({
    mutationFn: (token: string) => connectGitHub(token),
    onSuccess: () => invalidateAll(qc, workspaceId),
  });
}

export function useDisconnectGitHub(): UseMutationResult<void, Error, void> {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  return useMutation({
    mutationFn: () => disconnectGitHub(),
    onSuccess: () => invalidateAll(qc, workspaceId),
  });
}

export function useSetAuthorizedRepositories(): UseMutationResult<GitHubStatus, Error, string[]> {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  return useMutation({
    mutationFn: (fullNames: string[]) => setAuthorizedRepositories(fullNames),
    onSuccess: (status) => {
      // The response IS the new status, so the read the page renders from
      // is correct without a second round trip. The other two are
      // invalidated because what they show depends on this set.
      qc.setQueryData(githubStatusKey(workspaceId), status);
      void qc.invalidateQueries({ queryKey: githubReposKey(workspaceId) });
      void qc.invalidateQueries({ queryKey: githubActivityKey(workspaceId) });
    },
  });
}

/**
 * Turns a failure into something a person can act on, in their language,
 * without losing what an engineer would need.
 *
 * ── Why the code and not the message ───────────────────────────────────
 * The backend writes one actionable sentence per failure and a
 * machine-readable code beside it. The sentence is not translated — it is
 * written once, on the server, in one language — so leaning on it made the
 * card speak that language regardless of the reader. The CODE is the part
 * that is stable and language-free, so a known code becomes translated copy
 * here, and the server's sentence is carried alongside as `technical`.
 *
 * An unknown code keeps the server's sentence as the visible text: a
 * generic "something went wrong" would be a downgrade, and inventing a
 * translation for a code the backend never declared would be worse.
 *
 * `github_not_connected` is deliberately not an error at all — it is a
 * state the page has a card for.
 */
export type GitHubFailure = {
  /** What the reader sees, in their language when the code is known. */
  text: string;
  /** The server's own wording. Never translated, never invented. */
  technical: string | null;
  code: string | null;
};

export function githubErrorMessage(
  error: unknown,
  t: Translations,
): GitHubFailure | null {
  if (!(error instanceof ApiError)) {
    return error instanceof Error
      ? { text: error.message, technical: null, code: null }
      : null;
  }
  if (error.code === GITHUB_NOT_CONNECTED) return null;

  const known = t.app.settings.github.errors;
  const byCode: Record<string, string> = {
    github_unauthorized: known.unauthorized,
    github_rate_limited: known.rateLimited,
    github_repository_not_authorized: known.repoNotAuthorized,
  };

  const translated = byCode[error.code];
  return {
    text: translated ?? error.message,
    // Only worth repeating when it is not already the visible text.
    technical: translated ? error.message : null,
    code: error.code,
  };
}
