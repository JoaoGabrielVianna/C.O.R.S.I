/**
 * Typed REST client for /finance/purchase-plans.
 *
 * A PurchasePlan represents a single purchase materialized across N
 * installment transactions. The aggregate itself is not a ledger: the
 * backend spawns one Transaction per installment under the plan's id.
 * Future installments arrive as `scheduled`, paid ones as `paid` —
 * which means every total flows through the existing transactions
 * pipeline without a plan-specific aggregation. See
 * `backend/docs/totals-contract.md` §3.
 *
 * Wire shape mirrors `backend/api/openapi/finance.yaml` and the Go
 * structs in `backend/internal/finance/adapters/httpapi/purchaseplans.go`
 * + `backend/internal/finance/domain/purchaseplan.go`:
 *   • snake_case fields, integer cents, RFC3339 timestamps
 *   • create body keys: name / total_amount_cents / installments /
 *     first_occurred_at / category_id / person_id / account_id /
 *     payment_method / source / notes
 *   • response keys: id / workspace_id / person_id / name /
 *     total_amount_cents / installments / remaining_installments /
 *     created_at / deleted_at
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";
import type {
  PaymentMethodWire,
  TransactionSourceWire,
} from "./transactions";

export interface ApiPurchasePlan {
  id: string;
  workspace_id: string;
  person_id: string | null;
  name: string;
  total_amount_cents: number;
  installments: number;
  remaining_installments: number;
  created_at: string;            // RFC3339
  deleted_at?: string | null;
}

export interface CreatePurchasePlanRequest {
  category_id: string;
  person_id?: string | null;
  // FK deferred on the backend side (migration 0004 comment); the FE
  // passes the chosen credit-card UUID here so each installment row
  // carries it back via `transaction.account_id`.
  account_id?: string | null;
  name: string;
  total_amount_cents: number;
  installments: number;
  first_occurred_at: string;     // RFC3339
  payment_method?: PaymentMethodWire;
  source?: TransactionSourceWire;
  notes?: string;
}

export interface ListPurchasePlansParams {
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

export async function listPurchasePlans(
  p: ListPurchasePlansParams = {},
): Promise<ApiPurchasePlan[]> {
  const env = await apiFetch<PageEnvelope<ApiPurchasePlan>>(
    `/finance/purchase-plans${qs({ limit: 100, ...p })}`,
  );
  return env.items;
}

export function getPurchasePlan(id: string): Promise<ApiPurchasePlan> {
  return apiFetch<ApiPurchasePlan>(`/finance/purchase-plans/${id}`);
}

export function createPurchasePlan(
  body: CreatePurchasePlanRequest,
): Promise<ApiPurchasePlan> {
  return apiFetch<ApiPurchasePlan>(`/finance/purchase-plans`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// Backend cancel is body-less: it soft-deletes every still-scheduled
// installment of the plan. Paid installments stay as history.
export function cancelPurchasePlan(id: string): Promise<ApiPurchasePlan> {
  return apiFetch<ApiPurchasePlan>(`/finance/purchase-plans/${id}/cancel`, {
    method: "POST",
  });
}
