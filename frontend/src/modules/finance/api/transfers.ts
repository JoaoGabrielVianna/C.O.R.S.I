/**
 * Typed REST client for /finance/transfers.
 *
 * A Transfer is an atomic pair of transactions (expense + income legs)
 * sharing a `transfer_pair_id`. Both legs are server-classified as
 * EXCLUDED in the totals contract (see docs/totals-contract.md §2.1)
 * so they never inflate income or expense aggregates.
 *
 * Wire shape mirrors `backend/api/openapi/finance.yaml` 1:1.
 */

import { apiFetch } from "@/lib/api/client";
import type { ApiTransaction } from "./transactions";

export interface CreateTransferRequest {
  from_category_id: string;       // must reference an expense category
  to_category_id: string;         // must reference an income category
  from_account_id?: string | null;
  to_account_id?: string | null;
  amount_cents: number;
  occurred_at: string;            // RFC3339
  description?: string;
  notes?: string;
}

export interface ApiTransferPair {
  from: ApiTransaction;
  to: ApiTransaction;
}

export function createTransfer(body: CreateTransferRequest): Promise<ApiTransferPair> {
  return apiFetch<ApiTransferPair>(`/finance/transfers`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// Deletes BOTH legs of a transfer. The argument is the `transfer_pair_id`
// shared by the two legs (`Transaction.transferPairId` on the FE).
export function deleteTransfer(pairId: string): Promise<void> {
  return apiFetch<void>(`/finance/transfers/${pairId}`, { method: "DELETE" });
}
