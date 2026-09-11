/**
 * Typed REST client for the Meta Threads integration.
 *
 * ══════════════════════════════════════════════════════════════════════
 *  Meta Threads is NOT the Threads module
 * ══════════════════════════════════════════════════════════════════════
 *
 *   Meta Threads   the EXTERNAL social network. This file. A credential
 *                  and the account it belongs to. Read-only.
 *   Threads        the operator's INTERNAL content pipeline — ideas and
 *                  drafts. It has no frontend at all, by decision.
 *
 * Nothing here touches the second one.
 *
 * ── What this file is allowed to know ──────────────────────────────────
 * The management surface, and nothing about tools. Which capabilities an
 * agent may use is a question `GET /chat/agents/{id}/tools` answers from
 * the backend's own registry; a second list here would be a parallel
 * catalogue that drifts on the first deploy.
 *
 * ── The credential ─────────────────────────────────────────────────────
 * No function here ever receives or returns a token. The OAuth code is the
 * only secret that passes through, it lives for the duration of one
 * request, and what comes back is a four-character hint. There is
 * deliberately no `getToken`, and there must never be one.
 *
 * The exchange itself — code for token, token for long-lived token — is
 * the backend's, entirely. This file cannot perform it: it has no app
 * secret and no route to one.
 */

import { apiFetch } from "@/lib/api/client";

/**
 * One linked Meta Threads account.
 *
 * Every field here is already on the backend's response. Nothing is
 * computed client-side, and the cipher is absent because the Go struct
 * marks it `json:"-"` — it cannot reach this type even by mistake.
 */
export interface MetaThreadsConnection {
  id: string;
  /** The display-safe remnant, e.g. "…a3f9". Never the token. */
  token_hint: string;
  /** When the stored long-lived token dies, as Meta reported it. */
  token_expires_at: string;
  /** What the grant actually carries. Says what the CREDENTIAL can do,
   *  which is a different question from what an agent may ask for. */
  scopes: string[];
  account_id: string;
  username: string;
  display_name?: string;
  profile_picture_url?: string;
  /** Absent until a call to Meta has succeeded since it was stored. */
  last_verified_at?: string;
  created_at: string;
  updated_at: string;
}

export interface MetaThreadsStatus {
  connected: boolean;
  /**
   * Whether this DEPLOYMENT has a Meta app at all.
   *
   * Distinct from `connected` on purpose, and the distinction is the whole
   * reason the card can be honest: "you have not linked an account" and
   * "this installation cannot link one" are different problems with
   * different owners, and showing a Connect button for the second would
   * send the operator round a loop that cannot terminate.
   */
  configured: boolean;
  connection?: MetaThreadsConnection;
}

export interface MetaThreadsAuthorizeURL {
  authorization_url: string;
}

/** The machine-readable reasons the UI branches on. */
export const META_THREADS_NOT_CONFIGURED = "meta_threads_not_configured";
export const META_THREADS_NOT_CONNECTED = "meta_threads_not_connected";
export const META_THREADS_TOKEN_EXPIRED = "meta_threads_token_expired";
export const META_THREADS_SCOPE_MISSING = "meta_threads_scope_missing";
export const META_THREADS_INVALID = "meta_threads_invalid";
export const META_THREADS_UPSTREAM = "meta_threads_upstream";

export function getMetaThreadsStatus(): Promise<MetaThreadsStatus> {
  return apiFetch<MetaThreadsStatus>("/integrations/meta-threads/connection");
}

/**
 * Where the operator is sent to approve the connection.
 *
 * The URL is BUILT BY THE BACKEND, including the scope list. That is not an
 * arbitrary split: the scopes decide what the credential will be able to do
 * for the rest of its life, and a frontend that could name them could ask
 * for `threads_content_publish`. Here it cannot — it sends a `state` and
 * receives a finished URL.
 */
export function getMetaThreadsAuthorizeURL(state: string): Promise<MetaThreadsAuthorizeURL> {
  const query = state ? `?state=${encodeURIComponent(state)}` : "";
  return apiFetch<MetaThreadsAuthorizeURL>(`/integrations/meta-threads/authorize-url${query}`);
}

/**
 * Redeems the authorization code Meta handed back.
 *
 * Three round trips happen behind this one call — code for a short-lived
 * token, short-lived for long-lived, then a profile read that proves the
 * credential works before anything is stored — so the timeout is generous.
 */
export function connectMetaThreads(
  code: string,
  redirectUri: string,
): Promise<{ connection: MetaThreadsConnection }> {
  return apiFetch<{ connection: MetaThreadsConnection }>("/integrations/meta-threads/connection", {
    method: "POST",
    body: JSON.stringify({ code, redirect_uri: redirectUri }),
    timeoutMs: 30_000,
  });
}

/**
 * Extends the stored long-lived token.
 *
 * Meta refuses a token younger than 24 hours, and the backend refuses it
 * first with a sentence naming the moment it becomes possible. The UI does
 * NOT re-implement that rule: duplicating it here would be a second copy
 * free to disagree, and the server's refusal is already the better message.
 */
export function refreshMetaThreads(): Promise<{ connection: MetaThreadsConnection }> {
  return apiFetch<{ connection: MetaThreadsConnection }>(
    "/integrations/meta-threads/connection/refresh",
    { method: "POST", timeoutMs: 30_000 },
  );
}

export function disconnectMetaThreads(): Promise<void> {
  return apiFetch<void>("/integrations/meta-threads/connection", { method: "DELETE" });
}
