/**
 * Typed REST client for /finance/cards.
 *
 * Wire shape matches `backend/api/openapi/finance.yaml` 1:1. All
 * domain↔wire adaptation (color/owner local metadata, brand→network
 * mapping, institution/variant derivation from the free-form name)
 * happens in the hooks layer, not here.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

export type CardNetwork = "visa" | "mastercard" | "elo" | "amex";

export interface ApiCard {
  id: string;
  workspace_id: string;
  name: string;
  institution: string;
  network: CardNetwork;
  variant: string;
  last4: string;       // ^[0-9]{4}$
  limit_cents: number;
  closing_day: number; // 1..28
  due_day: number;     // 1..28
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface CreateCardRequest {
  name: string;
  institution: string;
  network: CardNetwork;
  variant?: string;
  last4: string;
  limit_cents: number;
  closing_day: number;
  due_day: number;
}

export interface UpdateCardRequest {
  name?: string;
  institution?: string;
  network?: CardNetwork;
  variant?: string;
  last4?: string;
  limit_cents?: number;
  closing_day?: number;
  due_day?: number;
}

export interface ListCardsParams {
  limit?: number;
  offset?: number;
}

function qs(params: object): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined) sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export async function listCards(p: ListCardsParams = {}): Promise<ApiCard[]> {
  const env = await apiFetch<PageEnvelope<ApiCard>>(`/finance/cards${qs(p)}`);
  return env.items;
}

export function getCard(id: string): Promise<ApiCard> {
  return apiFetch<ApiCard>(`/finance/cards/${id}`);
}

export function createCard(body: CreateCardRequest): Promise<ApiCard> {
  return apiFetch<ApiCard>(`/finance/cards`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateCard(id: string, body: UpdateCardRequest): Promise<ApiCard> {
  return apiFetch<ApiCard>(`/finance/cards/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

/** Backend uses DELETE to archive (soft-delete). */
export function archiveCard(id: string): Promise<void> {
  return apiFetch<void>(`/finance/cards/${id}`, { method: "DELETE" });
}
