/**
 * TanStack Query hooks for the Closet.
 *
 * ── Workspace in the key ───────────────────────────────────────────────
 * Same pattern as the other modules: every key starts with the current
 * workspace id, so switching workspace cannot serve another one's wardrobe
 * from cache.
 *
 * ── What invalidates what ──────────────────────────────────────────────
 * This module has real local writes, unlike Palace, so there is no
 * staleness window to accept: every mutation invalidates what it touched
 * and the screen re-reads. The one rule worth stating is that saving a
 * LOOK invalidates looks and not pieces — a composition references pieces
 * and never changes them — while archiving a PIECE invalidates both,
 * because a look's hydrated copy of that piece has just gone stale.
 *
 * ── The catalogue never goes stale ─────────────────────────────────────
 * It is the server's vocabulary, and it changes when the binary changes.
 * `staleTime: Infinity` means one request per session rather than one per
 * screen that needs to know what a category is.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";

import {
  archiveItem,
  archiveLook,
  createItem,
  createLook,
  getCatalog,
  getLook,
  listItems,
  listLooks,
  removeImage,
  restoreItem,
  restoreLook,
  setLookItems,
  updateItem,
  updateLook,
  uploadImage,
  type CreateItemBody,
  type CreateLookBody,
  type UpdateItemBody,
  type UpdateLookBody,
} from "../api/closet";
import type { Catalog, ClosetItem, Look, Page } from "../api/types";

/** The root key. Everything the Closet caches hangs off it. */
const rootKey = (workspaceId: string) => ["closet", workspaceId] as const;

export function useCatalog(): UseQueryResult<Catalog> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "catalog"] as const,
    queryFn: ({ signal }) => getCatalog(signal),
    // The vocabulary changes when the binary changes, not while somebody is
    // dressing.
    staleTime: Infinity,
  });
}

export interface ItemsParams {
  category?: string;
  search?: string;
  favorite?: boolean;
  includeArchived?: boolean;
}

export function useItems(params: ItemsParams = {}): UseQueryResult<Page<ClosetItem>> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "items", params] as const,
    queryFn: ({ signal }) =>
      listItems(
        {
          category: params.category,
          search: params.search,
          favorite: params.favorite,
          include_archived: params.includeArchived ? "true" : undefined,
        },
        signal,
      ),
  });
}

export interface LooksParams {
  occasion?: string;
  favorite?: boolean;
  includeArchived?: boolean;
  search?: string;
}

export function useLooks(params: LooksParams = {}): UseQueryResult<Page<Look>> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "looks", params] as const,
    queryFn: ({ signal }) =>
      listLooks(
        {
          occasion: params.occasion,
          favorite: params.favorite,
          search: params.search,
          include_archived: params.includeArchived ? "true" : undefined,
        },
        signal,
      ),
  });
}

export function useLook(id: string | undefined): UseQueryResult<Look> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "look", id] as const,
    queryFn: ({ signal }) => getLook(id!, signal),
    enabled: Boolean(id),
  });
}

/* ── mutations ───────────────────────────────────────────────────────── */

/**
 * The invalidation helpers.
 *
 * Written as two functions rather than one `invalidateQueries(["closet"])`
 * so the difference between "the wardrobe changed" and "a look changed" is
 * stated rather than papered over with a blanket refetch. A blanket
 * invalidation is also what makes a builder re-fetch forty images because
 * somebody renamed a look.
 */
function useInvalidators() {
  const qc = useQueryClient();
  const workspaceId = getApiWorkspaceId();
  const root = rootKey(workspaceId);
  return {
    items: () => qc.invalidateQueries({ queryKey: [...root, "items"] }),
    looks: () => {
      void qc.invalidateQueries({ queryKey: [...root, "looks"] });
      void qc.invalidateQueries({ queryKey: [...root, "look"] });
    },
  };
}

export function useCreateItem() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: (body: CreateItemBody) => createItem(body),
    onSuccess: () => invalidate.items(),
  });
}

export function useUpdateItem() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateItemBody }) => updateItem(id, body),
    onSuccess: () => {
      void invalidate.items();
      // A look carries a hydrated copy of the piece, so renaming a shirt
      // has to reach the gallery too.
      void invalidate.looks();
    },
  });
}

export function useArchiveItem() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: (id: string) => archiveItem(id),
    onSuccess: () => {
      void invalidate.items();
      void invalidate.looks();
    },
  });
}

export function useRestoreItem() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: (id: string) => restoreItem(id),
    onSuccess: () => {
      void invalidate.items();
      void invalidate.looks();
    },
  });
}

export function useUploadImage() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: ({ itemId, view, file }: { itemId: string; view: string; file: File }) =>
      uploadImage(itemId, view, file),
    onSuccess: () => {
      void invalidate.items();
      void invalidate.looks();
    },
  });
}

export function useRemoveImage() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: ({ itemId, view }: { itemId: string; view: string }) =>
      removeImage(itemId, view),
    onSuccess: () => {
      void invalidate.items();
      void invalidate.looks();
    },
  });
}

export function useCreateLook() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: (body: CreateLookBody) => createLook(body),
    onSuccess: () => invalidate.looks(),
  });
}

export function useUpdateLook() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateLookBody }) => updateLook(id, body),
    onSuccess: () => invalidate.looks(),
  });
}

export function useSetLookItems() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: ({ id, itemIDs }: { id: string; itemIDs: string[] }) =>
      setLookItems(id, itemIDs),
    onSuccess: () => invalidate.looks(),
  });
}

export function useArchiveLook() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: (id: string) => archiveLook(id),
    onSuccess: () => invalidate.looks(),
  });
}

export function useRestoreLook() {
  const invalidate = useInvalidators();
  return useMutation({
    mutationFn: (id: string) => restoreLook(id),
    onSuccess: () => invalidate.looks(),
  });
}
