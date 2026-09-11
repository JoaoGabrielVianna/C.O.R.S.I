/**
 * TanStack Query hooks for the Transfers aggregate.
 *
 * Transfers don't have their own list endpoint — the legs ride the
 * existing `/finance/transactions` feed tagged with `transfer_pair_id`,
 * surfaced as `type === "transfer"` by the transactions codec. Reads
 * therefore continue through `useTransactions`; this file only exposes
 * mutations (create pair, delete pair). Both mutations invalidate the
 * transactions root so the dashboard + lists refresh together.
 *
 * Backend semantics (see `backend/internal/finance/app/transfers.go`):
 *   • CREATE writes both legs atomically; from-leg is type=expense, to
 *     leg is type=income, both with the same `transfer_pair_id`.
 *   • DELETE soft-deletes both legs via the pair id.
 */

import {
  useMutation,
  useQueryClient,
  type UseMutationResult,
} from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  createTransfer as apiCreate,
  deleteTransfer as apiDelete,
  type ApiTransferPair,
  type CreateTransferRequest,
} from "@/modules/finance/api/transfers";
import { transactionsRootKey } from "@/modules/finance/hooks/useTransactions";

export interface CreateTransferInput {
  fromCategoryId: string;
  toCategoryId: string;
  fromAccountId?: string | null;
  toAccountId?: string | null;
  amount: number;                 // cents
  occurredAt: number;             // epoch ms
  description?: string;
  notes?: string;
}

export function useCreateTransfer(): UseMutationResult<
  ApiTransferPair,
  Error,
  CreateTransferInput
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const body: CreateTransferRequest = {
        from_category_id: input.fromCategoryId,
        to_category_id: input.toCategoryId,
        from_account_id: input.fromAccountId ?? null,
        to_account_id: input.toAccountId ?? null,
        amount_cents: Math.max(1, Math.floor(input.amount)),
        occurred_at: new Date(input.occurredAt).toISOString(),
        description: input.description,
        notes: input.notes,
      };
      return apiCreate(body);
    },
    onSettled: () => {
      // Both legs land in the transactions feed; totals must refetch.
      qc.invalidateQueries({ queryKey: transactionsRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useDeleteTransfer(): UseMutationResult<void, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (pairId) => apiDelete(pairId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: transactionsRootKey(getApiWorkspaceId()) });
    },
  });
}
