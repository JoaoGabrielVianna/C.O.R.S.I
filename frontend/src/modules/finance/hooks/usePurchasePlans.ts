/**
 * TanStack Query hooks for the PurchasePlans aggregate.
 *
 * Plans are parent records — the backend materializes one Transaction per
 * installment under the plan's id. Future installments are `scheduled`,
 * paid ones are `paid`. Every total (dashboard projected, dashboard
 * realized, by-category, by-month) flows through the existing
 * transactions feed; there is no plan-level total. See
 * `backend/docs/totals-contract.md` §3.
 *
 * Wire-shape note: the backend's PurchasePlan record is minimal
 * (id, name, total_amount_cents, installments, remaining_installments,
 * created_at, deleted_at) — it has no `status`, no `category_id`, no
 * `account_id`, no `first_occurred_at`. Those facts live on the child
 * installment rows. The local `PurchasePlan` type carries the richer
 * shape the UI was built against; `fromApi` fills the API-backed fields
 * and leaves the derived ones empty so `PurchasePlansSection.buildRow`
 * can resolve them from the plan's children at render time.
 *
 * Workspace isolation: same scoping pattern as Cards/Categories/
 * Transactions — query keys include the current workspace id. Mutations
 * invalidate both the plans root AND the transactions root so the
 * dashboard updates automatically.
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
  cancelPurchasePlan as apiCancel,
  createPurchasePlan as apiCreate,
  listPurchasePlans,
  type ApiPurchasePlan,
  type CreatePurchasePlanRequest,
  type ListPurchasePlansParams,
} from "@/modules/finance/api/purchasePlans";
import { transactionsRootKey } from "@/modules/finance/hooks/useTransactions";
import type { PurchasePlan } from "@/pages/app/modules/finance/types";

/* ── codec ──────────────────────────────────────────────────────────── */

function fromApi(api: ApiPurchasePlan): PurchasePlan {
  const createdAt = Date.parse(api.created_at);
  const total = api.total_amount_cents;
  const n = Math.max(1, api.installments);
  // Even split with the last installment absorbing remainder — same rule
  // the backend uses (see service `CreatePurchasePlan`).
  const per = Math.floor(total / n);
  return {
    id: api.id,
    description: api.name,
    originalAmount: total,
    totalInstallments: api.installments,
    perInstallment: per,
    // accountId (card binding), categoryId, and purchasedAt are not on
    // the plan response — they live on the child installment rows. The
    // UI's plan-row component re-derives them from
    // `state.transactions` keyed by planId.
    accountId: "",
    personId: api.person_id ?? "",
    categoryId: "",
    purchasedAt: createdAt,
    // BE has no status field. Default to "active"; `PurchasePlansSection`
    // refines this from (paid, remaining) counts of child installments.
    status: "active",
    notes: "",
    createdAt,
    updatedAt: createdAt,
  };
}

/* ── keys ───────────────────────────────────────────────────────────── */

export const purchasePlansRootKey = (workspaceId: string) =>
  ["finance", "purchase-plans", workspaceId] as const;

export const purchasePlansKey = (workspaceId: string, params?: ListPurchasePlansParams) =>
  params && Object.keys(params).length > 0
    ? ([...purchasePlansRootKey(workspaceId), params] as const)
    : purchasePlansRootKey(workspaceId);

/* ── query ──────────────────────────────────────────────────────────── */

export function usePurchasePlans(
  params?: ListPurchasePlansParams,
): UseQueryResult<PurchasePlan[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: purchasePlansKey(workspaceId, params),
    queryFn: async () => (await listPurchasePlans(params)).map(fromApi),
    staleTime: 30_000,
  });
}

/* ── create ─────────────────────────────────────────────────────────── */

export interface CreatePurchasePlanInput {
  description: string;
  originalAmount: number;          // cents
  totalInstallments: number;
  cardId: string;                  // backend card UUID — sent as account_id
  personId?: string | null;
  categoryId: string;
  firstDueDate: number;            // epoch ms (local midnight)
  notes?: string;
}

// Backend `first_occurred_at` is RFC3339 (Go time.Time). The picker
// gives us a local-midnight epoch; serialize as an ISO instant.
function toRFC3339(ms: number): string {
  return new Date(ms).toISOString();
}

export function useCreatePurchasePlan(): UseMutationResult<
  PurchasePlan,
  Error,
  CreatePurchasePlanInput,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const body: CreatePurchasePlanRequest = {
        name: input.description,
        total_amount_cents: Math.max(1, Math.floor(input.originalAmount)),
        installments: Math.max(2, Math.floor(input.totalInstallments)),
        category_id: input.categoryId,
        person_id: input.personId ?? null,
        // Send the chosen card UUID as account_id; the FK is deferred
        // on the backend (migration 0004) so any workspace-scoped UUID
        // round-trips. Children carry it back on `transaction.account_id`.
        account_id: input.cardId || null,
        first_occurred_at: toRFC3339(input.firstDueDate),
        payment_method: "credit",
        source: "manual",
        notes: input.notes,
      };
      const created = await apiCreate(body);
      return fromApi(created);
    },
    onMutate: async (input) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = purchasePlansRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<PurchasePlan[]>({ queryKey: rootKey });
      const per = Math.floor(input.originalAmount / Math.max(1, input.totalInstallments));
      const now = Date.now();
      const optimistic: PurchasePlan = {
        id: `optimistic_${now}_${Math.random().toString(36).slice(2, 7)}`,
        description: input.description,
        originalAmount: input.originalAmount,
        totalInstallments: input.totalInstallments,
        perInstallment: per,
        accountId: input.cardId,
        personId: input.personId ?? "",
        categoryId: input.categoryId,
        purchasedAt: input.firstDueDate,
        status: "active",
        notes: input.notes ?? "",
        createdAt: now,
        updatedAt: now,
      };
      qc.setQueriesData<PurchasePlan[]>({ queryKey: rootKey }, (old) =>
        old ? [optimistic, ...old] : [optimistic],
      );
      return { previous, workspaceId };
    },
    onError: (_err, _input, ctx) => {
      if (!ctx) return;
      for (const [key, data] of ctx.previous) qc.setQueryData(key, data);
    },
    onSettled: (_data, _err, _input, ctx) => {
      const workspaceId = ctx?.workspaceId ?? getApiWorkspaceId();
      // Plans + transactions: creating a plan spawns N child rows, so the
      // transactions cache must refresh for dashboard totals to pick up
      // the new projected installments.
      qc.invalidateQueries({ queryKey: purchasePlansRootKey(workspaceId) });
      qc.invalidateQueries({ queryKey: transactionsRootKey(workspaceId) });
    },
  });
}

/* ── cancel ─────────────────────────────────────────────────────────── */

export interface CancelPurchasePlanInput {
  id: string;
}

export function useCancelPurchasePlan(): UseMutationResult<
  PurchasePlan,
  Error,
  CancelPurchasePlanInput,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id }) => {
      const updated = await apiCancel(id);
      return fromApi(updated);
    },
    onMutate: async ({ id }) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = purchasePlansRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<PurchasePlan[]>({ queryKey: rootKey });
      const now = Date.now();
      qc.setQueriesData<PurchasePlan[]>({ queryKey: rootKey }, (old) =>
        old
          ? old.map((p) =>
              p.id === id
                ? {
                    ...p,
                    status: "cancelled" as const,
                    cancelledAt: now,
                    updatedAt: now,
                  }
                : p,
            )
          : old,
      );
      return { previous, workspaceId };
    },
    onError: (_err, _input, ctx) => {
      if (!ctx) return;
      for (const [key, data] of ctx.previous) qc.setQueryData(key, data);
    },
    onSettled: (_data, _err, _input, ctx) => {
      const workspaceId = ctx?.workspaceId ?? getApiWorkspaceId();
      // Cancelling removes scheduled child rows server-side. Refresh
      // both caches so the dashboard's PROJECTED total drops.
      qc.invalidateQueries({ queryKey: purchasePlansRootKey(workspaceId) });
      qc.invalidateQueries({ queryKey: transactionsRootKey(workspaceId) });
    },
  });
}

/* Re-export for convenience. */
export type { PurchasePlan };
