/**
 * Typed REST client for /chat/agents/{id}/sources.
 *
 * A Source is reference material the agent can consult: a named piece of
 * text the user wrote or pasted on purpose. It is not Memory — see
 * `@/modules/agents/api/memories` for the other side of that boundary.
 *
 * ── The list does not carry the text ───────────────────────────────────
 * A source runs to twenty thousand characters, so `listSources` returns
 * titles, sizes and state with `content` empty, and `getSource` returns the
 * document when the editor opens it. Content can never legitimately be
 * empty (the database rejects it), so an empty string here unambiguously
 * means "not fetched".
 */

import { apiFetch } from "@/lib/api/client";

export interface ApiSource {
  id: string;
  workspace_id: string;
  agent_id: string;
  title: string;
  /** A note for the human. Never sent to the model. */
  description: string;
  /** Empty in list responses. See the note above. */
  content: string;
  enabled: boolean;
  /** Exact rune count of the content, measured server-side. */
  characters: number;
  /** `characters` through the module's one heuristic. An estimate. */
  estimated_tokens: number;
  /**
   * Whether this source would reach the model on the next turn. Computed by
   * the backend with the same function that composes the turn.
   */
  in_context: boolean;
  /**
   * This source cannot fit the block on its own, whatever else is switched
   * off. A different fact from `in_context: false`, and shown differently:
   * one is "not this time", the other is "not ever, until you shorten it".
   */
  oversized: boolean;
  created_at: string;
  updated_at: string;
}

export interface SourcePage {
  items: ApiSource[];
  /** The cap the server applied. `total` above it means the list was cut. */
  limit: number;
  total: number;
  /** What the selected sources contribute to a turn, header included. */
  used_characters: number;
  budget_characters: number;
}

export interface CreateSourceRequest {
  title: string;
  description?: string;
  content: string;
}

export type UpdateSourceRequest = Partial<CreateSourceRequest> & { enabled?: boolean };

export function listSources(agentId: string): Promise<SourcePage> {
  return apiFetch<SourcePage>(`/chat/agents/${agentId}/sources`);
}

export function getSource(agentId: string, id: string): Promise<ApiSource> {
  return apiFetch<ApiSource>(`/chat/agents/${agentId}/sources/${id}`);
}

export function createSource(agentId: string, body: CreateSourceRequest): Promise<ApiSource> {
  return apiFetch<ApiSource>(`/chat/agents/${agentId}/sources`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateSource(
  agentId: string,
  id: string,
  body: UpdateSourceRequest,
): Promise<ApiSource> {
  return apiFetch<ApiSource>(`/chat/agents/${agentId}/sources/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteSource(agentId: string, id: string): Promise<void> {
  return apiFetch<void>(`/chat/agents/${agentId}/sources/${id}`, { method: "DELETE" });
}

/** Mirrors the backend CHECK constraints, so the counters agree. */
export const MAX_SOURCE_TITLE = 120;
export const MAX_SOURCE_DESCRIPTION = 280;
export const MAX_SOURCE_CONTENT = 20000;

/**
 * The module's token heuristic, for text that has not been saved yet.
 *
 * This is the one place the rule is duplicated outside Go, and it is
 * duplicated for exactly one reason: the editor shows the size of a draft
 * while it is being typed, and a draft does not exist on the server to be
 * measured. **Every saved source reports `estimated_tokens` from the
 * backend** — never recompute it here for a record that came back from the
 * API.
 *
 * It must stay identical to `app.EstimateTokens`: ceil(characters / 4),
 * measured in characters and never in bytes. It is an estimate, it is named
 * one, and it must be presented as one.
 */
export function estimateTokens(characters: number): number {
  if (characters <= 0) return 0;
  return Math.ceil(characters / 4);
}

/** Rune count, matching how the backend and the database both measure. */
export function countCharacters(text: string): number {
  return [...text].length;
}
