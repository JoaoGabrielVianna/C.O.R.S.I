/**
 * Typed REST client for the GitHub integration.
 *
 * ── What this file is allowed to know ──────────────────────────────────
 * The management surface, and nothing about tools. Which capabilities an
 * agent may use is a question `GET /chat/agents/{id}/tools` answers, and it
 * is answered by the backend's own registry; a second list here would be a
 * parallel catalogue that drifts on the first deploy.
 *
 * ── The credential ─────────────────────────────────────────────────────
 * `connect` is the only function that ever holds a token, and it holds it
 * for the duration of one request. Nothing returns one: the backend seals
 * it and answers with a four-character hint. There is deliberately no
 * `getToken`, and there must never be one — a token in frontend state is a
 * token in a devtools panel, a bug report and a session replay.
 */

import { apiFetch } from "@/lib/api/client";

/** How the stored credential was obtained. One value today. */
export type GitHubAuthKind = "pat";

export interface GitHubConnection {
  id: string;
  auth_kind: GitHubAuthKind;
  /** The display-safe remnant, e.g. "...a3f9". Never the token. */
  token_hint: string;
  account_login: string;
  account_id: number;
  account_type: "User" | "Organization";
  account_name?: string;
  account_avatar_url?: string;
  /** Absent until a call to GitHub has succeeded since it was stored. */
  last_verified_at?: string;
  created_at: string;
  updated_at: string;
}

export interface GitHubRepository {
  id: string;
  github_id: number;
  owner: string;
  name: string;
  full_name: string;
  private: boolean;
  default_branch: string;
  html_url?: string;
  description?: string;
  owner_type: "User" | "Organization";
  authorized_at: string;
}

export interface GitHubStatus {
  connected: boolean;
  connection?: GitHubConnection;
  /**
   * The authorized set — which is also exactly what the agent tools can
   * reach. Present even when empty, because "connected and nothing
   * authorized" is the state a person most needs explained.
   */
  repositories: GitHubRepository[];
  /**
   * Derived by the backend from the authorized repositories' owners, not
   * read from GitHub's org listing. See the note on app.Status.
   */
  organizations: string[];
}

/** One repository the credential can reach, and whether it is allowed. */
export interface GitHubAvailableRepository {
  id: number;
  owner: string;
  owner_type: "User" | "Organization";
  name: string;
  full_name: string;
  private: boolean;
  description?: string;
  default_branch: string;
  html_url?: string;
  pushed_at?: string;
  authorized: boolean;
}

export interface GitHubAvailableRepositories {
  items: GitHubAvailableRepository[];
  /** The ceiling the server applied. */
  limit: number;
  /** Whether it was reached — a picker that stops silently tells somebody
   *  their repository does not exist. */
  truncated: boolean;
}

export interface GitHubActivityEntry {
  repository: string;
  sha: string;
  message: string;
  author_name?: string;
  author_login?: string;
  date?: string;
  html_url?: string;
}

export interface GitHubActivity {
  items: GitHubActivityEntry[];
  repositories_read: number;
  per_repository: number;
}

/** The machine-readable reasons the UI branches on. */
export const GITHUB_NOT_CONNECTED = "github_not_connected";
export const GITHUB_UNAUTHORIZED = "github_unauthorized";
export const GITHUB_RATE_LIMITED = "github_rate_limited";

export function getGitHubStatus(): Promise<GitHubStatus> {
  return apiFetch<GitHubStatus>("/integrations/github/connection");
}

/**
 * Stores a credential, after the backend has proved it works.
 *
 * The token is the argument and never the return value. A caller that
 * wanted to keep it would have to keep its own copy, which is a decision
 * someone would have to write down rather than inherit.
 */
export function connectGitHub(token: string): Promise<GitHubConnection> {
  return apiFetch<GitHubConnection>("/integrations/github/connection", {
    method: "POST",
    body: JSON.stringify({ token }),
    // Verifying a token is a round trip to GitHub, on top of ours.
    timeoutMs: 30_000,
  });
}

export function disconnectGitHub(): Promise<void> {
  return apiFetch<void>("/integrations/github/connection", { method: "DELETE" });
}

/** What the credential can reach. This one talks to GitHub, so it is slow
 *  and is deliberately a separate call from the status read. */
export function listAvailableRepositories(): Promise<GitHubAvailableRepositories> {
  return apiFetch<GitHubAvailableRepositories>("/integrations/github/repositories", {
    timeoutMs: 30_000,
  });
}

/**
 * Replaces the authorized set with exactly `fullNames`.
 *
 * The whole set, not a delta. The operator edits a set, and sending an add
 * and a revoke as two requests creates a window in which the stored set is
 * neither the old one nor the new one — and a tool call landing in that
 * window would read an authorization nobody chose.
 */
export function setAuthorizedRepositories(fullNames: string[]): Promise<GitHubStatus> {
  return apiFetch<GitHubStatus>("/integrations/github/repositories", {
    method: "PUT",
    body: JSON.stringify({ repositories: fullNames }),
    timeoutMs: 30_000,
  });
}

export function getGitHubActivity(): Promise<GitHubActivity> {
  return apiFetch<GitHubActivity>("/integrations/github/activity", { timeoutMs: 30_000 });
}
