/**
 * TanStack Query hooks for the Cards aggregate.
 *
 * Returns the same `CreditCard` shape the rest of the frontend consumes
 * (from `pages/app/modules/finance/types.ts`) — ownerId + color come from
 * local metadata, the rest from the backend. The backend's
 * institution/network/variant are wire-only details: the frontend
 * re-derives them from `name`+`brand` via `cards.ts` detectors for
 * display, so we ignore them on read.
 *
 * Workspace isolation: same scoping pattern as `useCategories` — query
 * keys include the current workspace id. Mutations cancel/invalidate
 * inside the workspace's root key only.
 *
 * `useArchiveCard` is the "delete" mutation; the backend's DELETE is a
 * soft-delete that flips `deleted_at`. We surface it as `archive` to
 * match the frontend UX vocabulary.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  archiveCard as apiArchive,
  createCard as apiCreate,
  listCards,
  updateCard as apiUpdate,
  type ApiCard,
  type CardNetwork,
  type CreateCardRequest,
  type ListCardsParams,
  type UpdateCardRequest,
} from "@/modules/finance/api/cards";
import {
  clearCardMetadata,
  getCardMetadata,
  setCardMetadata,
} from "@/modules/finance/api/cardMetadata";
import { addArchivedCard, removeArchivedCard } from "@/modules/finance/api/cardArchive";
import { detectBank, detectNetwork, detectVariant } from "@/pages/app/modules/finance/cards";
import type { CreditCard } from "@/pages/app/modules/finance/types";

/* ── codec ──────────────────────────────────────────────────────────── */

const VALID_NETWORKS: ReadonlySet<CardNetwork> = new Set(["visa", "mastercard", "elo", "amex"]);

/**
 * Map the FE's free-form `brand` to the backend's enum. Anything outside
 * the enum (hipercard, diners, unknown) defaults to "visa" — the display
 * layer re-derives the real network from `name`+`brand` via detectNetwork,
 * so the wire-side default never leaks into the UI.
 */
function brandToNetwork(name: string, brand?: string): CardNetwork {
  const detected = detectNetwork(name, brand).key;
  return (VALID_NETWORKS.has(detected as CardNetwork) ? detected : "visa") as CardNetwork;
}

function clampDay(d: number): number {
  if (Number.isNaN(d) || d < 1) return 1;
  if (d > 28) return 28;
  return Math.floor(d);
}

function fromApi(api: ApiCard): CreditCard {
  const md = getCardMetadata(api.id);
  return {
    id: api.id,
    name: api.name,
    ownerId: md.ownerId ?? "",
    limit: api.limit_cents,
    closingDay: api.closing_day,
    dueDay: api.due_day,
    // detectNetwork on the name produces the right display token; we don't
    // round-trip the backend's `network` enum because it's been narrowed.
    brand: api.network,
    color: md.color,
    last4: api.last4,
    deletedAt: api.deleted_at ? Date.parse(api.deleted_at) : undefined,
  };
}

/* ── keys ───────────────────────────────────────────────────────────── */

export const cardsRootKey = (workspaceId: string) =>
  ["finance", "cards", workspaceId] as const;

export const cardsKey = (workspaceId: string, params?: ListCardsParams) =>
  params && Object.keys(params).length > 0
    ? ([...cardsRootKey(workspaceId), params] as const)
    : cardsRootKey(workspaceId);

/* ── query ──────────────────────────────────────────────────────────── */

export function useCards(params?: ListCardsParams): UseQueryResult<CreditCard[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: cardsKey(workspaceId, params),
    queryFn: async () => (await listCards(params)).map(fromApi),
    staleTime: 30_000,
  });
}

/* ── create ─────────────────────────────────────────────────────────── */

export interface CreateCardInput {
  name: string;
  ownerId: string;       // local
  limit: number;         // cents
  closingDay: number;
  dueDay: number;
  brand?: string;        // free-form, will be normalized to network
  last4: string;         // 4 digits — required by backend
  color?: string;        // Tailwind token (local)
}

export function useCreateCard(): UseMutationResult<
  CreditCard,
  Error,
  CreateCardInput,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const body: CreateCardRequest = {
        name: input.name,
        institution: detectBank(input.name).key,
        network: brandToNetwork(input.name, input.brand),
        variant: detectVariant(input.name).key,
        last4: input.last4,
        limit_cents: Math.max(0, Math.floor(input.limit)),
        closing_day: clampDay(input.closingDay),
        due_day: clampDay(input.dueDay),
      };
      const created = await apiCreate(body);
      setCardMetadata(created.id, { ownerId: input.ownerId, color: input.color });
      return fromApi(created);
    },
    onMutate: async (input) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = cardsRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<CreditCard[]>({ queryKey: rootKey });
      const optimistic: CreditCard = {
        id: `optimistic_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
        name: input.name,
        ownerId: input.ownerId,
        limit: input.limit,
        closingDay: clampDay(input.closingDay),
        dueDay: clampDay(input.dueDay),
        brand: brandToNetwork(input.name, input.brand),
        last4: input.last4,
        color: input.color,
      };
      qc.setQueriesData<CreditCard[]>({ queryKey: rootKey }, (old) =>
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
      qc.invalidateQueries({ queryKey: cardsRootKey(workspaceId) });
    },
  });
}

/* ── update ─────────────────────────────────────────────────────────── */

export interface UpdateCardInput {
  id: string;
  name?: string;
  ownerId?: string;
  limit?: number;
  closingDay?: number;
  dueDay?: number;
  brand?: string;
  last4?: string;
  color?: string;
}

export function useUpdateCard(): UseMutationResult<
  CreditCard | null,
  Error,
  UpdateCardInput
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, ...input }) => {
      const patch: UpdateCardRequest = {};
      if (input.name !== undefined) {
        patch.name = input.name;
        // institution + variant are derived from name; keep them in sync.
        patch.institution = detectBank(input.name).key;
        patch.variant = detectVariant(input.name).key;
      }
      if (input.brand !== undefined || input.name !== undefined) {
        // network only stored on the wire — re-derive on any change that
        // would affect detection.
        patch.network = brandToNetwork(input.name ?? "", input.brand);
      }
      if (input.last4 !== undefined) patch.last4 = input.last4;
      if (input.limit !== undefined) patch.limit_cents = Math.max(0, Math.floor(input.limit));
      if (input.closingDay !== undefined) patch.closing_day = clampDay(input.closingDay);
      if (input.dueDay !== undefined) patch.due_day = clampDay(input.dueDay);

      let updated: CreditCard | null = null;
      if (Object.keys(patch).length > 0) {
        updated = fromApi(await apiUpdate(id, patch));
      }
      // Local-only metadata after the network call so a failure doesn't
      // leave local ahead of server.
      if (input.ownerId !== undefined || input.color !== undefined) {
        const m: { ownerId?: string; color?: string } = {};
        if (input.ownerId !== undefined) m.ownerId = input.ownerId;
        if (input.color !== undefined) m.color = input.color;
        setCardMetadata(id, m);
      }
      return updated;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: cardsRootKey(getApiWorkspaceId()) });
    },
  });
}

/* ── archive (soft-delete) ──────────────────────────────────────────── */

export function useArchiveCard(): UseMutationResult<
  string,
  Error,
  string,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id) => {
      await apiArchive(id);
      // Local metadata (ownerId, color) stays so the archive snapshot can
      // be re-resolved on reload via the legacy/archive fallback.
      return id;
    },
    onMutate: async (id) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = cardsRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<CreditCard[]>({ queryKey: rootKey });

      // Persist a snapshot of the card BEFORE the backend forgets it (the
      // list endpoint filters archived rows). The store merges these
      // snapshots into state.creditCards so "Show archived" + historical
      // transaction labels keep working after reload.
      const liveList = qc.getQueryData<CreditCard[]>(rootKey);
      const snapshot = liveList?.find((c) => c.id === id);
      if (snapshot) addArchivedCard({ ...snapshot, deletedAt: Date.now() });

      // Mark archived in-cache so any consumer that filters `!c.deletedAt`
      // hides it instantly — `activeCreditCards` in the store does this.
      qc.setQueriesData<CreditCard[]>({ queryKey: rootKey }, (old) =>
        old ? old.map((c) => (c.id === id ? { ...c, deletedAt: Date.now() } : c)) : old,
      );
      return { previous, workspaceId };
    },
    onError: (_err, id, ctx) => {
      if (!ctx) return;
      for (const [key, data] of ctx.previous) qc.setQueryData(key, data);
      // The archive snapshot was written before the backend call; the
      // failure means the card is still live, so clear the orphan
      // snapshot to keep the de-dup filter unambiguous.
      removeArchivedCard(id);
    },
    onSettled: (_data, _err, _id, ctx) => {
      const workspaceId = ctx?.workspaceId ?? getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: cardsRootKey(workspaceId) });
    },
  });
}

/* Re-export for convenience. */
export type { CreditCard };
export { clearCardMetadata };
