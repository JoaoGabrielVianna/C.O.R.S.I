/**
 * Recurring entries, served by the backend.
 *
 * ── Why this hook exists ───────────────────────────────────────────────
 * The entity lived only in `localStorage`, which meant the screens could
 * project a monthly figure Ledger had no way to see — the last place where
 * the UI and the agent could hold different beliefs about the user's money.
 * It moved to Postgres; this is the read and write path the screens use,
 * and it is the same application service the `finance.recurring_entry.*`
 * capabilities call.
 *
 * Amounts are integer cents end to end. The local shape uses `amount` for
 * cents, matching the rest of the module's `Cents` convention.
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
  createRecurringEntry as apiCreate,
  deleteRecurringEntry as apiDelete,
  getRecurringSummary as apiSummary,
  listRecurringEntries as apiList,
  updateRecurringEntry as apiUpdate,
  type ApiRecurringEntry,
  type ApiRecurringSummary,
  type UpdateRecurringEntryRequest,
} from "../api/recurringEntries";
import type { RecurringEntry } from "@/pages/app/modules/finance/types";

export const recurringEntriesRootKey = (workspaceId: string) =>
  ["finance", "recurring-entries", workspaceId] as const;

export const recurringSummaryKey = (workspaceId: string) =>
  ["finance", "recurring-summary", workspaceId] as const;

/** Wire → the shape the screens already render. */
function fromApi(a: ApiRecurringEntry): RecurringEntry {
  return {
    id: a.id,
    description: a.description,
    amount: a.amount_cents,
    categoryId: a.category_id,
    personId: a.person_id ?? "",
    dueDay: a.due_day,
    recurrence: a.recurrence,
    dueMonth: a.due_month ?? undefined,
    amountVaries: a.amount_varies,
    status: a.status,
    startsAt: Date.parse(a.starts_at),
    endsAt: a.ends_at ? Date.parse(a.ends_at) : undefined,
    notes: a.notes,
  };
}

export function useRecurringEntries(): UseQueryResult<RecurringEntry[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: recurringEntriesRootKey(workspaceId),
    queryFn: async () => (await apiList()).map(fromApi),
    staleTime: 30_000,
  });
}

export function useRecurringSummary(): UseQueryResult<ApiRecurringSummary> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: recurringSummaryKey(workspaceId),
    queryFn: apiSummary,
    staleTime: 30_000,
  });
}

export interface CreateRecurringEntryInput {
  description: string;
  amount: number;          // cents
  categoryId: string;
  personId?: string | null;
  dueDay: number;
  recurrence?: "monthly" | "annual";
  /** Required when `recurrence` is annual; refused when it is monthly. */
  dueMonth?: number;
  amountVaries?: boolean;
  notes?: string;
}

/**
 * Invalidates every read a definition write can move.
 *
 * The list and the normalised monthly total, as before. And the MONTHLY
 * COMMITMENT: an edit carrying `apply_to_period` changes a definition and
 * a month in one transaction, and a screen showing both must not keep half
 * of the old answer. The key is imported rather than re-spelled so a rename
 * cannot leave this stale.
 */
function useInvalidate() {
  const qc = useQueryClient();
  return () => {
    const ws = getApiWorkspaceId();
    void qc.invalidateQueries({ queryKey: recurringEntriesRootKey(ws) });
    void qc.invalidateQueries({ queryKey: recurringSummaryKey(ws) });
    void qc.invalidateQueries({ queryKey: ["finance", "monthly-commitment", ws] });
  };
}

export function useCreateRecurringEntry(): UseMutationResult<
  RecurringEntry, Error, CreateRecurringEntryInput
> {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input) =>
      fromApi(
        await apiCreate({
          description: input.description,
          amount_cents: Math.max(1, Math.floor(input.amount)),
          category_id: input.categoryId,
          person_id: input.personId && input.personId !== "" ? input.personId : null,
          due_day: input.dueDay,
          recurrence: input.recurrence,
          // Sent only for an annual recurrence: the backend refuses a
          // due_month on a monthly one, and the form never offers it there.
          due_month: input.recurrence === "annual" ? input.dueMonth : undefined,
          amount_varies: input.amountVaries,
          notes: input.notes,
        }),
      ),
    onSuccess: invalidate,
  });
}

export function useUpdateRecurringEntry(): UseMutationResult<
  RecurringEntry, Error, { id: string; patch: UpdateRecurringEntryRequest }
> {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async ({ id, patch }) => fromApi(await apiUpdate(id, patch)),
    onSuccess: invalidate,
  });
}

export function useDeleteRecurringEntry(): UseMutationResult<void, Error, string> {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: apiDelete,
    onSuccess: invalidate,
  });
}
