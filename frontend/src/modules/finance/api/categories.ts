/**
 * Typed REST client for /finance/categories.
 *
 * Wire shape matches `backend/api/openapi/finance.yaml` 1:1. All
 * domain↔wire adaptation (color codec, local-only `budget` metadata)
 * happens in the hooks layer, not here.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

export type EntryType = "income" | "expense";

export interface ApiCategory {
  id: string;
  workspace_id: string;
  name: string;
  type: EntryType;
  color: string;       // #rrggbb
  icon: string;
  created_at: string;  // RFC3339
  updated_at: string;
  deleted_at?: string;
}

export interface CreateCategoryRequest {
  name: string;
  type: EntryType;
  color: string;
  icon: string;
}

export interface UpdateCategoryRequest {
  name?: string;
  color?: string;
  icon?: string;
}

export interface ListCategoriesParams {
  type?: EntryType;
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

export async function listCategories(p: ListCategoriesParams = {}): Promise<ApiCategory[]> {
  const env = await apiFetch<PageEnvelope<ApiCategory>>(`/finance/categories${qs(p)}`);
  return env.items;
}

export function getCategory(id: string): Promise<ApiCategory> {
  return apiFetch<ApiCategory>(`/finance/categories/${id}`);
}

export function createCategory(body: CreateCategoryRequest): Promise<ApiCategory> {
  return apiFetch<ApiCategory>(`/finance/categories`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateCategory(id: string, body: UpdateCategoryRequest): Promise<ApiCategory> {
  return apiFetch<ApiCategory>(`/finance/categories/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteCategory(id: string): Promise<void> {
  return apiFetch<void>(`/finance/categories/${id}`, { method: "DELETE" });
}
