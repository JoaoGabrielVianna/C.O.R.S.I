/**
 * Typed REST client for /finance/transactions.
 *
 * Backend models income/expense only. Transfers, installment plans,
 * shared expenses, and Person/Account entities stay 100% local on the
 * frontend — see `transactionMetadata.ts` for the sidecar of fields
 * that ride along with backend rows but aren't on the wire.
 *
 * Wire shape matches `backend/api/openapi/finance.yaml` 1:1.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

export type EntryType = "income" | "expense";
export type TransactionStatus = "paid" | "pending" | "scheduled";
export type PaymentMethodWire = "debit" | "credit" | "pix" | "cash" | "transfer";
export type TransactionSourceWire = "manual" | "whatsapp" | "import" | "ai";

export interface ApiTransaction {
  id: string;
  workspace_id: string;
  account_id: string | null;
  category_id: string;
  type: EntryType;
  person_id: string | null;
  amount_cents: number;
  description: string;
  occurred_at: string;        // RFC3339
  status: TransactionStatus;
  payment_method: PaymentMethodWire;
  source: TransactionSourceWire;
  notes: string;
  // Present iff the row is a leg of a transfer pair. Both legs share
  // the same pair id; treat any tx with this set as type="transfer"
  // for display + as excluded from realized/projected totals.
  transfer_pair_id?: string | null;
  plan_id?: string | null;
  installment_number?: number;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface CreateTransactionRequest {
  account_id?: string | null;
  category_id: string;
  person_id?: string | null;
  amount_cents: number;
  description?: string;
  occurred_at: string;
  status?: TransactionStatus;
  payment_method?: PaymentMethodWire;
  source?: TransactionSourceWire;
  notes?: string;
}

export interface UpdateTransactionRequest {
  account_id?: string | null;
  clear_account_id?: boolean;
  category_id?: string;
  person_id?: string | null;
  clear_person_id?: boolean;
  amount_cents?: number;
  description?: string;
  occurred_at?: string;
  status?: TransactionStatus;
  payment_method?: PaymentMethodWire;
  source?: TransactionSourceWire;
  notes?: string;
}

export interface ListTransactionsParams {
  type?: EntryType;
  category?: string;
  status?: TransactionStatus;
  from?: string;
  to?: string;
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

export async function listTransactions(p: ListTransactionsParams = {}): Promise<ApiTransaction[]> {
  // Bump the per-page cap since the dashboard sums an entire month and
  // we don't yet expose cursor pagination on the frontend.
  const env = await apiFetch<PageEnvelope<ApiTransaction>>(
    `/finance/transactions${qs({ limit: 100, ...p })}`,
  );
  return env.items;
}

export function getTransaction(id: string): Promise<ApiTransaction> {
  return apiFetch<ApiTransaction>(`/finance/transactions/${id}`);
}

export function createTransaction(body: CreateTransactionRequest): Promise<ApiTransaction> {
  return apiFetch<ApiTransaction>(`/finance/transactions`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateTransaction(id: string, body: UpdateTransactionRequest): Promise<ApiTransaction> {
  return apiFetch<ApiTransaction>(`/finance/transactions/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteTransaction(id: string): Promise<void> {
  return apiFetch<void>(`/finance/transactions/${id}`, { method: "DELETE" });
}

/* ── Totals (dashboard contract) ────────────────────────────────────────
 * Server-side aggregation per docs/totals-contract.md. The dashboard
 * MUST consume `by_realization` (realized / projected / excluded) and
 * MUST NOT recompute these from raw transactions — transfer-leg
 * exclusion and the wall-clock realized/projected split are server
 * concerns. Window bounds are required, RFC3339.
 */

export interface ApiTotalsBucket {
  income_cents: number;
  expense_cents: number;
  count: number;
}

export interface ApiCategoryTotal {
  category_id: string;
  type: EntryType;
  total_cents: number;
  count: number;
}

export interface ApiMethodTotal {
  payment_method: PaymentMethodWire;
  total_cents: number;
  count: number;
}

export interface ApiTransactionTotals {
  window: { from: string; to: string };
  by_status: {
    paid: ApiTotalsBucket;
    pending: ApiTotalsBucket;
    scheduled: ApiTotalsBucket;
  };
  by_realization: {
    realized: ApiTotalsBucket;
    projected: ApiTotalsBucket;
    excluded: ApiTotalsBucket;
  };
  by_category: ApiCategoryTotal[];
  by_method: ApiMethodTotal[];
}

export interface TransactionTotalsParams {
  from: string;  // RFC3339
  to: string;    // RFC3339
}

export function getTransactionTotals(p: TransactionTotalsParams): Promise<ApiTransactionTotals> {
  return apiFetch<ApiTransactionTotals>(
    `/finance/transactions/totals${qs(p)}`,
  );
}

/**
 * Backend transaction ids are UUIDs (server-generated by `gen_random_uuid()`).
 * Pre-Phase-3 local ids look like `tx_<base36ts>_<base36rand>`. Use this
 * predicate to route mutations: UUID → backend hooks; otherwise → store.
 */
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function isBackendTransactionId(id: string): boolean {
  return UUID_RE.test(id);
}

/**
 * Which writer handles an edit: the backend, or the in-memory store.
 *
 * ── Why this is a named predicate and not an inline condition ──────────
 * Because getting it wrong is silent. It decided, for a while, that a
 * purchase-plan installment should be edited locally — `planId != null` was
 * part of the test — and that was true when installments were synthesised
 * in the browser. Once plans moved to Postgres it stopped being true, and
 * nothing failed: `store.updateTransaction` walked an array that no longer
 * contained backend rows, matched nothing, and the modal closed reporting
 * success. The user saw a saved edit that was never written.
 *
 * The rule is one thing and one thing only: **does this row live in the
 * backend?** A server-issued id means yes, and everything about the row —
 * whether it belongs to a plan, whether it is a transfer leg — is the
 * backend's business after that. Anything else is a leftover that only ever
 * existed in this browser.
 */
export function editWritesToBackend(tx: { id: string }): boolean {
  return isBackendTransactionId(tx.id);
}
