/**
 * Typed REST client for /finance/persons.
 *
 * Backend models name + notes only. Frontend extras (role, active,
 * whatsappNumber) ride a local sidecar — see `personMetadata.ts`.
 *
 * Wire shape matches `backend/api/openapi/finance.yaml` and the Go
 * struct in `backend/internal/finance/domain/person.go`.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

export interface ApiPerson {
  id: string;
  workspace_id: string;
  name: string;
  notes: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
}

export interface CreatePersonRequest {
  name: string;
  notes?: string;
}

export interface UpdatePersonRequest {
  name?: string;
  notes?: string;
}

export interface ListPersonsParams {
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

export async function listPersons(p: ListPersonsParams = {}): Promise<ApiPerson[]> {
  const env = await apiFetch<PageEnvelope<ApiPerson>>(
    `/finance/persons${qs({ limit: 100, ...p })}`,
  );
  return env.items;
}

export function getPerson(id: string): Promise<ApiPerson> {
  return apiFetch<ApiPerson>(`/finance/persons/${id}`);
}

export function createPerson(body: CreatePersonRequest): Promise<ApiPerson> {
  return apiFetch<ApiPerson>(`/finance/persons`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updatePerson(id: string, body: UpdatePersonRequest): Promise<ApiPerson> {
  return apiFetch<ApiPerson>(`/finance/persons/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deletePerson(id: string): Promise<void> {
  return apiFetch<void>(`/finance/persons/${id}`, { method: "DELETE" });
}
