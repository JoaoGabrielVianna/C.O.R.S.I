/**
 * TanStack Query hooks for the Categories aggregate.
 *
 * Exposes the same domain shape the rest of the frontend already uses
 * (`Category` from `pages/app/modules/finance/types.ts`) — color in
 * Tailwind-token form, optional local `budget`. The codec lives in
 * `@/lib/api/colorCodec`; the `budget` field lives in
 * `@/modules/finance/api/categoryMetadata` until backend v0.2 adopts it.
 *
 * Workspace isolation: every query key is scoped by the current workspace
 * id (`getApiWorkspaceId()`). When external auth lands and the getter
 * starts returning per-user ids, switching workspace allocates a separate
 * cache slot automatically — no leak. Optimistic mutations also scope
 * their setQueriesData / cancelQueries / invalidateQueries calls to the
 * same workspace key.
 *
 * Cache invalidation: writes invalidate the workspace's root key tree.
 * Create and Delete are optimistic; Update is straight-through.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { hexToToken, tokenToHex } from "@/lib/api/colorCodec";
import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  createCategory as apiCreate,
  deleteCategory as apiDelete,
  listCategories,
  updateCategory as apiUpdate,
  type ApiCategory,
  type EntryType,
  type ListCategoriesParams,
  type UpdateCategoryRequest,
} from "@/modules/finance/api/categories";
import {
  clearCategoryMetadata,
  getCategoryMetadata,
  setCategoryMetadata,
} from "@/modules/finance/api/categoryMetadata";

export type CategoryType = EntryType;

export interface Category {
  id: string;
  name: string;
  type: CategoryType;
  icon: string;
  color: string;          // Tailwind token
  budget?: number | null; // Cents — local until backend v0.2
}

function fromApi(api: ApiCategory): Category {
  return {
    id: api.id,
    name: api.name,
    type: api.type,
    icon: api.icon,
    color: hexToToken(api.color),
    budget: getCategoryMetadata(api.id).budget ?? null,
  };
}

/**
 * Workspace-scoped query keys. Shape:
 *   ["finance","categories", <workspaceId>]                — root list
 *   ["finance","categories", <workspaceId>, <params>]      — filtered list
 *
 * Mutations target the workspace's root key (prefix-match) so unfiltered
 * and filtered queries stay in sync, but never bleed across workspaces.
 */
export const categoriesRootKey = (workspaceId: string) =>
  ["finance", "categories", workspaceId] as const;

export const categoriesKey = (
  workspaceId: string,
  params?: ListCategoriesParams,
) =>
  params && Object.keys(params).length > 0
    ? ([...categoriesRootKey(workspaceId), params] as const)
    : categoriesRootKey(workspaceId);

export function useCategories(
  params?: ListCategoriesParams,
): UseQueryResult<Category[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: categoriesKey(workspaceId, params),
    queryFn: async () => (await listCategories(params)).map(fromApi),
    staleTime: 30_000,
  });
}

export interface CreateCategoryInput {
  name: string;
  type: CategoryType;
  icon: string;
  color: string;          // Tailwind token
  budget?: number | null;
}

export function useCreateCategory(): UseMutationResult<
  Category,
  Error,
  CreateCategoryInput,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const created = await apiCreate({
        name: input.name,
        type: input.type,
        icon: input.icon,
        color: tokenToHex(input.color),
      });
      if (input.budget !== undefined && input.budget !== null) {
        setCategoryMetadata(created.id, { budget: input.budget });
      }
      return fromApi(created);
    },
    onMutate: async (input) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = categoriesRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<Category[]>({ queryKey: rootKey });
      const optimistic: Category = {
        id: `optimistic_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
        name: input.name,
        type: input.type,
        icon: input.icon,
        color: input.color,
        budget: input.budget ?? null,
      };
      qc.setQueriesData<Category[]>({ queryKey: rootKey }, (old) =>
        old ? [...old, optimistic] : [optimistic],
      );
      return { previous, workspaceId };
    },
    onError: (_err, _input, ctx) => {
      if (!ctx) return;
      for (const [key, data] of ctx.previous) qc.setQueryData(key, data);
    },
    onSettled: (_data, _err, _input, ctx) => {
      const workspaceId = ctx?.workspaceId ?? getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: categoriesRootKey(workspaceId) });
    },
  });
}

export interface UpdateCategoryInput {
  id: string;
  name?: string;
  icon?: string;
  color?: string;          // Tailwind token
  budget?: number | null;
}

export function useUpdateCategory(): UseMutationResult<
  Category | null,
  Error,
  UpdateCategoryInput
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, ...input }) => {
      const patch: UpdateCategoryRequest = {};
      if (input.name !== undefined) patch.name = input.name;
      if (input.icon !== undefined) patch.icon = input.icon;
      if (input.color !== undefined) patch.color = tokenToHex(input.color);

      let updated: Category | null = null;
      if (Object.keys(patch).length > 0) {
        // Backend call first so a network/validation failure doesn't leave
        // local metadata ahead of the server state.
        updated = fromApi(await apiUpdate(id, patch));
      }
      if (input.budget !== undefined) {
        setCategoryMetadata(id, { budget: input.budget });
      }
      return updated;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: categoriesRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useDeleteCategory(): UseMutationResult<
  string,
  Error,
  string,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id) => {
      await apiDelete(id);
      clearCategoryMetadata(id);
      return id;
    },
    onMutate: async (id) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = categoriesRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<Category[]>({ queryKey: rootKey });
      qc.setQueriesData<Category[]>({ queryKey: rootKey }, (old) =>
        old ? old.filter((c) => c.id !== id) : old,
      );
      return { previous, workspaceId };
    },
    onError: (_err, _id, ctx) => {
      if (!ctx) return;
      for (const [key, data] of ctx.previous) qc.setQueryData(key, data);
    },
    onSettled: (_data, _err, _id, ctx) => {
      const workspaceId = ctx?.workspaceId ?? getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: categoriesRootKey(workspaceId) });
    },
  });
}
