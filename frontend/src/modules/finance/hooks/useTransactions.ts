/**
 * TanStack Query hooks for the Transactions aggregate.
 *
 * Backend-served slice: income/expense transactions WITHOUT planId.
 * Transfers, installment-plan children, splitAmong, personId, legacy
 * accountId are NOT on the wire — see `transactionMetadata.ts`.
 *
 * Returns the same `Transaction` shape the rest of the frontend
 * consumes; the hook merges backend fields with the local sidecar so
 * `Overview` / `Projection` / `People` aggregations stay correct
 * without component changes.
 *
 * Workspace isolation: same scoping pattern as Categories/Cards.
 * Mutations cancel/invalidate inside the workspace's root key only.
 *
 * TODO backend-v0.3: when persons + accounts land, drop the sidecar
 * and send `person_id` / `account_id` over the wire.
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
  createTransaction as apiCreate,
  deleteTransaction as apiDelete,
  getTransactionTotals as apiTotals,
  editWritesToBackend,
  isBackendTransactionId,
  listTransactions,
  updateTransaction as apiUpdate,
  type ApiTransaction,
  type ApiTransactionTotals,
  type CreateTransactionRequest,
  type ListTransactionsParams,
  type PaymentMethodWire,
  type TransactionSourceWire,
  type TransactionStatus,
  type TransactionTotalsParams,
  type UpdateTransactionRequest,
} from "@/modules/finance/api/transactions";
import {
  clearTransactionMetadata,
  getTransactionMetadata,
  setTransactionMetadata,
} from "@/modules/finance/api/transactionMetadata";
import type {
  PaymentMethod,
  Transaction,
  TransactionSource,
} from "@/pages/app/modules/finance/types";

/* ── codec ──────────────────────────────────────────────────────────── */

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
function isUuid(s: string | null | undefined): s is string {
  return !!s && UUID_RE.test(s);
}

function fromApi(api: ApiTransaction): Transaction {
  const md = getTransactionMetadata(api.id);
  // Transfer legs arrive as income/expense rows tagged with
  // `transfer_pair_id`. Surface them as `type="transfer"` so the
  // existing UI excludes them from income/expense aggregations and the
  // Transactions tab can badge them.
  const isTransferLeg = Boolean(api.transfer_pair_id);
  return {
    id: api.id,
    type: isTransferLeg ? "transfer" : api.type,
    // person_id rides the wire now that the Persons aggregate is on the
    // backend. The sidecar `personId` remains as a read fallback for
    // pre-v0.3 rows that don't carry a backend UUID yet.
    personId: api.person_id ?? md.personId ?? "",
    amount: api.amount_cents,
    description: api.description,
    categoryId: api.category_id,
    paymentMethod: api.payment_method as PaymentMethod,
    // accountId resolves to backend UUID if present, else the local fallback
    // captured at create/update time (legacy `cc_*` ids).
    accountId: api.account_id ?? md.accountId ?? null,
    date: Date.parse(api.occurred_at),
    status: api.status,
    source: api.source as TransactionSource,
    notes: api.notes,
    splitAmong: md.splitAmong,
    planId: api.plan_id ?? undefined,
    installmentNumber: api.installment_number ?? undefined,
    transferPairId: api.transfer_pair_id ?? undefined,
    createdAt: Date.parse(api.created_at),
    updatedAt: Date.parse(api.updated_at),
  };
}

/* ── keys ───────────────────────────────────────────────────────────── */

export const transactionsRootKey = (workspaceId: string) =>
  ["finance", "transactions", workspaceId] as const;

export const transactionsKey = (workspaceId: string, params?: ListTransactionsParams) =>
  params && Object.keys(params).length > 0
    ? ([...transactionsRootKey(workspaceId), params] as const)
    : transactionsRootKey(workspaceId);

/* ── query ──────────────────────────────────────────────────────────── */

export function useTransactions(
  params?: ListTransactionsParams,
): UseQueryResult<Transaction[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: transactionsKey(workspaceId, params),
    queryFn: async () => (await listTransactions(params)).map(fromApi),
    staleTime: 15_000,
  });
}

/* ── totals (dashboard contract) ───────────────────────────────────────
 * Reads `GET /finance/transactions/totals` for the given RFC3339 window.
 * The dashboard tiles must consume `by_realization` (realized / projected
 * / excluded) from this hook — see docs/totals-contract.md §§2, 5.
 *
 * Cache key is scoped by workspace + window. Mutations on transactions
 * or purchase plans already invalidate `transactionsRootKey`, which is
 * a prefix of this key, so totals refresh automatically.
 */

export const transactionTotalsKey = (workspaceId: string, params: TransactionTotalsParams) =>
  [...transactionsRootKey(workspaceId), "totals", params.from, params.to] as const;

export function useTransactionTotals(
  params: TransactionTotalsParams,
): UseQueryResult<ApiTransactionTotals> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: transactionTotalsKey(workspaceId, params),
    queryFn: () => apiTotals(params),
    staleTime: 15_000,
    enabled: Boolean(params.from) && Boolean(params.to),
  });
}

/* ── create ─────────────────────────────────────────────────────────── */

export interface CreateTransactionInput {
  type: "income" | "expense"; // transfer is NOT routed here
  categoryId: string;
  amount: number;
  description: string;
  occurredAt: number;          // epoch ms
  status: TransactionStatus;
  paymentMethod: PaymentMethod;
  source: TransactionSource;
  notes: string;
  // Local sidecar fields:
  personId: string;
  splitAmong?: string[];
  accountId?: string | null;   // can be backend UUID or legacy id
}

export function useCreateTransaction(): UseMutationResult<
  Transaction,
  Error,
  CreateTransactionInput,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const body: CreateTransactionRequest = {
        category_id: input.categoryId,
        amount_cents: Math.max(1, Math.floor(input.amount)),
        description: input.description,
        occurred_at: new Date(input.occurredAt).toISOString(),
        status: input.status,
        payment_method: input.paymentMethod as PaymentMethodWire,
        source: input.source as TransactionSourceWire,
        notes: input.notes,
      };
      // Only send accountId / personId over the wire when they are real
      // backend UUIDs. Legacy ids stay in the sidecar.
      if (isUuid(input.accountId ?? undefined)) {
        body.account_id = input.accountId;
      }
      if (isUuid(input.personId)) {
        body.person_id = input.personId;
      }
      const created = await apiCreate(body);
      const sidecar: { personId?: string; splitAmong?: string[]; accountId?: string } = {};
      if (input.personId && !isUuid(input.personId)) sidecar.personId = input.personId;
      if (input.splitAmong && input.splitAmong.length >= 2) sidecar.splitAmong = input.splitAmong;
      if (input.accountId && !isUuid(input.accountId)) sidecar.accountId = input.accountId;
      if (Object.keys(sidecar).length > 0) setTransactionMetadata(created.id, sidecar);
      return fromApi(created);
    },
    onMutate: async (input) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = transactionsRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<Transaction[]>({ queryKey: rootKey });
      const optimistic: Transaction = {
        id: `optimistic_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
        type: input.type,
        personId: input.personId,
        amount: input.amount,
        description: input.description,
        categoryId: input.categoryId,
        paymentMethod: input.paymentMethod,
        accountId: input.accountId ?? null,
        date: input.occurredAt,
        status: input.status,
        source: input.source,
        notes: input.notes,
        splitAmong: input.splitAmong,
        createdAt: Date.now(),
        updatedAt: Date.now(),
      };
      qc.setQueriesData<Transaction[]>({ queryKey: rootKey }, (old) =>
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
      qc.invalidateQueries({ queryKey: transactionsRootKey(workspaceId) });
    },
  });
}

/* ── update ─────────────────────────────────────────────────────────── */

export interface UpdateTransactionInput {
  id: string;                  // must be a backend UUID; caller is responsible for routing
  type?: "income" | "expense";
  categoryId?: string;
  amount?: number;
  description?: string;
  occurredAt?: number;
  status?: TransactionStatus;
  paymentMethod?: PaymentMethod;
  source?: TransactionSource;
  notes?: string;
  // Sidecar:
  personId?: string;
  splitAmong?: string[];
  accountId?: string | null;   // null = clear; UUID = wire; non-UUID = sidecar
  clearAccountId?: boolean;
}

export function useUpdateTransaction(): UseMutationResult<
  Transaction | null,
  Error,
  UpdateTransactionInput
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, ...input }) => {
      const patch: UpdateTransactionRequest = {};
      if (input.categoryId !== undefined) patch.category_id = input.categoryId;
      if (input.amount !== undefined) patch.amount_cents = Math.max(1, Math.floor(input.amount));
      if (input.description !== undefined) patch.description = input.description;
      if (input.occurredAt !== undefined) patch.occurred_at = new Date(input.occurredAt).toISOString();
      if (input.status !== undefined) patch.status = input.status;
      if (input.paymentMethod !== undefined) patch.payment_method = input.paymentMethod as PaymentMethodWire;
      if (input.source !== undefined) patch.source = input.source as TransactionSourceWire;
      if (input.notes !== undefined) patch.notes = input.notes;
      if (input.clearAccountId) {
        patch.clear_account_id = true;
      } else if (input.accountId !== undefined && isUuid(input.accountId)) {
        patch.account_id = input.accountId;
      }
      // person_id rides the wire when it's a backend UUID; legacy
      // `p_*` ids only update the local sidecar below.
      if (input.personId !== undefined) {
        if (input.personId === "") {
          patch.clear_person_id = true;
        } else if (isUuid(input.personId)) {
          patch.person_id = input.personId;
        }
      }

      let updated: Transaction | null = null;
      if (Object.keys(patch).length > 0) {
        updated = fromApi(await apiUpdate(id, patch));
      }
      // Sidecar (after backend call so a network failure doesn't leave
      // metadata ahead of server state). Only legacy non-UUID ids land
      // here now that persons + accounts are on the wire.
      const md: { personId?: string; splitAmong?: string[]; accountId?: string } = {};
      if (input.personId !== undefined && input.personId !== "" && !isUuid(input.personId)) {
        md.personId = input.personId;
      }
      if (input.splitAmong !== undefined) md.splitAmong = input.splitAmong;
      if (input.accountId !== undefined && !isUuid(input.accountId)) {
        if (input.accountId === null) {
          /* cleared above; nothing to write */
        } else {
          md.accountId = input.accountId ?? undefined;
        }
      }
      if (Object.keys(md).length > 0) setTransactionMetadata(id, md);
      return updated;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: transactionsRootKey(getApiWorkspaceId()) });
    },
  });
}

/* ── delete ─────────────────────────────────────────────────────────── */

export function useDeleteTransaction(): UseMutationResult<
  string,
  Error,
  string,
  { previous: Array<[readonly unknown[], unknown]>; workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id) => {
      if (!isBackendTransactionId(id)) {
        throw new Error("useDeleteTransaction only handles backend (UUID) ids");
      }
      await apiDelete(id);
      clearTransactionMetadata(id);
      return id;
    },
    onMutate: async (id) => {
      const workspaceId = getApiWorkspaceId();
      const rootKey = transactionsRootKey(workspaceId);
      await qc.cancelQueries({ queryKey: rootKey });
      const previous = qc.getQueriesData<Transaction[]>({ queryKey: rootKey });
      qc.setQueriesData<Transaction[]>({ queryKey: rootKey }, (old) =>
        old ? old.filter((t) => t.id !== id) : old,
      );
      return { previous, workspaceId };
    },
    onError: (_err, _id, ctx) => {
      if (!ctx) return;
      for (const [key, data] of ctx.previous) qc.setQueryData(key, data);
    },
    onSettled: (_data, _err, _id, ctx) => {
      const workspaceId = ctx?.workspaceId ?? getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: transactionsRootKey(workspaceId) });
    },
  });
}

/* Re-export so the routing predicate has a single import path. */
// Re-exported so the modal has one import for routing decisions and the
// mutations they route to. `editWritesToBackend` is the predicate that
// decides which writer handles an edit — see api/transactions.ts.
export { editWritesToBackend, isBackendTransactionId };
