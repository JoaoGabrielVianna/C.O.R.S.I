/**
 * Typed REST client for /chat/providers.
 *
 * A provider is an OpenAI-compatible endpoint — LiteLLM in practice. The
 * API key travels inbound only: the backend seals it and reads never
 * return it. `api_key_hint` is the last few characters, enough to tell two
 * keys apart and useless on its own.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

export interface ApiProvider {
  id: string;
  workspace_id: string;
  name: string;
  base_url: string;
  api_key_hint: string;
  default_model: string;
  created_at: string; // RFC3339
  updated_at: string;
  deleted_at?: string;
}

export interface CreateProviderRequest {
  name: string;
  base_url: string;
  api_key: string;
  default_model: string;
}

/** Omit `api_key` to keep the stored credential untouched. */
export interface UpdateProviderRequest {
  name?: string;
  base_url?: string;
  api_key?: string;
  default_model?: string;
}

export interface ApiModel {
  id: string;
  owned_by?: string;
}

export async function listProviders(): Promise<ApiProvider[]> {
  const env = await apiFetch<PageEnvelope<ApiProvider>>(`/chat/providers`);
  return env.items;
}

export function createProvider(body: CreateProviderRequest): Promise<ApiProvider> {
  return apiFetch<ApiProvider>(`/chat/providers`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateProvider(id: string, body: UpdateProviderRequest): Promise<ApiProvider> {
  return apiFetch<ApiProvider>(`/chat/providers/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteProvider(id: string): Promise<void> {
  return apiFetch<void>(`/chat/providers/${id}`, { method: "DELETE" });
}

/**
 * Lists the endpoint's catalog. Doubles as the connection test — it is the
 * cheapest call that exercises the base URL and the key together, so a
 * successful response means a real send will reach someone.
 *
 * Allowed a longer timeout than the default: a cold LiteLLM instance can
 * take a few seconds to answer its first request.
 */
export async function listProviderModels(id: string): Promise<ApiModel[]> {
  const res = await apiFetch<{ items: ApiModel[] }>(`/chat/providers/${id}/models`, {
    timeoutMs: 20_000,
  });
  return res.items;
}
