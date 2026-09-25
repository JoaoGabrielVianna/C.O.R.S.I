/**
 * Typed REST client for the Closet.
 *
 * ── Two transports, and why ────────────────────────────────────────────
 * Everything that carries JSON goes through `apiFetch`, like every other
 * module. Two things cannot:
 *
 *   - `uploadImage` sends multipart, so it must not have a JSON
 *     `Content-Type` set for it. It drives `fetch` directly, the same way
 *     the chat stream reader does, and resolves the base URL from the same
 *     constant rather than keeping a second copy of the rule.
 *   - `fetchAssetBlob` reads image BYTES. See `useAssetObjectURL` for the
 *     whole argument about why a browser cannot simply put the URL in an
 *     `<img src>`.
 */

import { API_BASE, ApiError, apiFetch, type ApiErrorBody } from "@/lib/api/client";
import { getApiWorkspaceId } from "@/lib/api/workspace";

import type {
  ArchiveItemResult,
  Catalog,
  ClosetItem,
  ItemImage,
  Look,
  Page,
} from "./types";

/* ── the catalogue ───────────────────────────────────────────────────── */

export function getCatalog(signal?: AbortSignal): Promise<Catalog> {
  return apiFetch<Catalog>("/closet/catalog", { signal });
}

/* ── pieces ──────────────────────────────────────────────────────────── */

/**
 * The closed set of query keys this client will serialise.
 *
 * A permissive `Record<string, unknown>` is how a parameter nobody designed
 * eventually reaches the server: somebody spreads an object that came from
 * a URL, and the URL is whatever a bookmark happened to carry.
 */
const ITEM_QUERY_KEYS = [
  "category",
  "status",
  "include_archived",
  "favorite",
  "search",
  "limit",
  "offset",
] as const;

type ItemQuery = Partial<
  Record<(typeof ITEM_QUERY_KEYS)[number], string | number | boolean | undefined>
>;

const LOOK_QUERY_KEYS = [
  "occasion",
  "favorite",
  "include_archived",
  "search",
  "limit",
  "offset",
] as const;

type LookQuery = Partial<
  Record<(typeof LOOK_QUERY_KEYS)[number], string | number | boolean | undefined>
>;

/**
 * Serialises ONLY the listed keys, read out one at a time rather than by
 * iterating the object — the type is a compile-time promise and the object
 * is a runtime value, and only reading the keys by name makes the two the
 * same thing.
 */
function queryString(keys: readonly string[], q: Record<string, unknown>): string {
  const params = new URLSearchParams();
  for (const key of keys) {
    const value = q[key];
    if (value === undefined || value === null || value === "") continue;
    params.set(key, String(value));
  }
  const s = params.toString();
  return s ? `?${s}` : "";
}

export function listItems(q: ItemQuery = {}, signal?: AbortSignal): Promise<Page<ClosetItem>> {
  return apiFetch<Page<ClosetItem>>(
    `/closet/items${queryString(ITEM_QUERY_KEYS, q)}`,
    { signal },
  );
}

export function getItem(id: string, signal?: AbortSignal): Promise<ClosetItem> {
  return apiFetch<ClosetItem>(`/closet/items/${id}`, { signal });
}

export interface CreateItemBody {
  name: string;
  category: string;
  subtype?: string;
  primary_color: string;
  secondary_color?: string;
  brand?: string;
  notes?: string;
  favorite?: boolean;
}

export function createItem(body: CreateItemBody): Promise<ClosetItem> {
  return apiFetch<ClosetItem>("/closet/items", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

/**
 * A partial update. An omitted key means "leave it alone" — which is what
 * lets the detail panel save one edited field without echoing back the five
 * it did not touch.
 */
export type UpdateItemBody = Partial<CreateItemBody>;

export function updateItem(id: string, body: UpdateItemBody): Promise<ClosetItem> {
  return apiFetch<ClosetItem>(`/closet/items/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function archiveItem(id: string): Promise<ArchiveItemResult> {
  return apiFetch<ArchiveItemResult>(`/closet/items/${id}/archive`, { method: "POST" });
}

export function restoreItem(id: string): Promise<ClosetItem> {
  return apiFetch<ClosetItem>(`/closet/items/${id}/restore`, { method: "POST" });
}

/* ── images ──────────────────────────────────────────────────────────── */

/**
 * Attaches a PNG to one view of one piece, replacing whatever that view
 * held.
 *
 * ── Why this one drives `fetch` itself ─────────────────────────────────
 * `apiFetch` sets `Content-Type: application/json` whenever a body is
 * present. A multipart request must carry the boundary the browser
 * generates, so the header has to be left alone — setting it by hand is the
 * classic way to produce an upload the server cannot parse.
 *
 * The filename passed to `FormData.append` is the one the browser took from
 * the file picker. The server never reads it; it is sent because a
 * multipart file part without one is not treated as a file at all.
 */
export async function uploadImage(
  itemId: string,
  view: string,
  file: File,
  signal?: AbortSignal,
): Promise<ItemImage> {
  const form = new FormData();
  form.append("file", file, file.name || "image.png");

  const res = await fetch(`${API_BASE}/closet/items/${itemId}/images/${view}`, {
    method: "PUT",
    body: form,
    headers: { "X-Workspace-Id": getApiWorkspaceId() },
    credentials: "include",
    signal,
  });

  if (!res.ok) {
    // Same envelope as every other error in the product, decoded the same
    // way, so a screen does not need a second error shape for uploads. A
    // body this layer cannot read is not a reason to lose the status.
    throw new ApiError(res.status, await readErrorBody(res), res.statusText);
  }
  return (await res.json()) as ItemImage;
}

/**
 * Reads the platform's error envelope, or returns null when the response
 * carried something else — an HTML page from a proxy, an empty body.
 */
async function readErrorBody(res: Response): Promise<ApiErrorBody | null> {
  try {
    return (await res.json()) as ApiErrorBody;
  } catch {
    return null;
  }
}

export function removeImage(itemId: string, view: string): Promise<void> {
  return apiFetch<void>(`/closet/items/${itemId}/images/${view}`, { method: "DELETE" });
}

/**
 * Fetches the bytes of one image.
 *
 * Returns a Blob rather than a URL because the caller has to decide the
 * lifetime of the object URL — see `useAssetObjectURL`, which owns that.
 */
export async function fetchAssetBlob(assetId: string, signal?: AbortSignal): Promise<Blob> {
  const res = await fetch(`${API_BASE}/closet/assets/${assetId}`, {
    headers: { "X-Workspace-Id": getApiWorkspaceId() },
    credentials: "include",
    signal,
  });
  if (!res.ok) {
    throw new ApiError(res.status, null, res.statusText);
  }
  return res.blob();
}

/* ── looks ───────────────────────────────────────────────────────────── */

export function listLooks(q: LookQuery = {}, signal?: AbortSignal): Promise<Page<Look>> {
  return apiFetch<Page<Look>>(`/closet/looks${queryString(LOOK_QUERY_KEYS, q)}`, { signal });
}

export function getLook(id: string, signal?: AbortSignal): Promise<Look> {
  return apiFetch<Look>(`/closet/looks/${id}`, { signal });
}

export interface CreateLookBody {
  name: string;
  occasion?: string;
  favorite?: boolean;
  notes?: string;
  /** In the order they were chosen. The server derives the slots. */
  item_ids?: string[];
}

export function createLook(body: CreateLookBody): Promise<Look> {
  return apiFetch<Look>("/closet/looks", { method: "POST", body: JSON.stringify(body) });
}

export type UpdateLookBody = Partial<Omit<CreateLookBody, "item_ids">>;

export function updateLook(id: string, body: UpdateLookBody): Promise<Look> {
  return apiFetch<Look>(`/closet/looks/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

/**
 * Replaces a look's whole composition.
 *
 * The ids go up in the order they were chosen and the SERVER re-runs every
 * rule — capacity, eviction, de-duplication, position. The builder's local
 * composition is a preview of that answer, never the record of it.
 */
export function setLookItems(id: string, itemIds: string[]): Promise<Look> {
  return apiFetch<Look>(`/closet/looks/${id}/items`, {
    method: "PUT",
    body: JSON.stringify({ item_ids: itemIds }),
  });
}

export function archiveLook(id: string): Promise<Look> {
  return apiFetch<Look>(`/closet/looks/${id}/archive`, { method: "POST" });
}

export function restoreLook(id: string): Promise<Look> {
  return apiFetch<Look>(`/closet/looks/${id}/restore`, { method: "POST" });
}
