/**
 * The monthly commitment, as server state.
 *
 * ── Why this is a query and not part of the Finance store ──────────────
 * Because it is not the browser's state to hold. `store.ts` exists to merge
 * a legacy localStorage blob with backend slices, and the one lesson that
 * file records in full is what happens when a screen keeps its own copy of
 * something Postgres owns: the KPI tiles and the charts answered the same
 * question differently, and nothing said which to believe.
 *
 * A month's totals, its paid state and its authoritative `today` are read,
 * rendered, and thrown away. Putting them in a mutable global store would
 * create a second place they could be wrong. The selected month STAYS in
 * the store, because that is a UI choice the user made and it drives more
 * than this screen.
 *
 * ── Why the mutations are not optimistic ───────────────────────────────
 * Deliberately, and it is not laziness. An optimistic tick would have to
 * predict four server-computed totals, five counts and a derived `overdue`,
 * from a row it is about to change — which is recomputing financial truth
 * in the client, the one thing this surface must not do. Half-optimism
 * (flip the row, leave the totals) is worse: the list and the summary
 * disagree for as long as the request is in flight, and on a slow
 * connection that is exactly when somebody looks.
 *
 * So the control disables itself while the request is in flight, the answer
 * comes from the server, and the row and the totals move together.
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
  getMonthlyCommitment,
  markOccurrencePaid,
  markOccurrencePending,
  setOccurrenceAmount,
  type ApiMonthlyCommitment,
  type PeriodKey,
} from "../api/monthlyCommitment";
import { recurringEntriesRootKey, recurringSummaryKey } from "./useRecurringEntries";

export const monthlyCommitmentRootKey = (workspaceId: string) =>
  ["finance", "monthly-commitment", workspaceId] as const;

export const monthlyCommitmentKey = (workspaceId: string, period: PeriodKey) =>
  [...monthlyCommitmentRootKey(workspaceId), period] as const;

/**
 * One month.
 *
 * ── staleTime is zero, on purpose ──────────────────────────────────────
 * Every other Finance read here caches for thirty seconds. This one does
 * not, because `overdue` is derived against the server's today and a bill
 * can be settled from a conversation with Ledger while the screen is open.
 * A stale month is a screen telling somebody a bill is outstanding when it
 * is not, and that is the exact question this surface exists to answer.
 */
export function useMonthlyCommitment(period: PeriodKey): UseQueryResult<ApiMonthlyCommitment> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: monthlyCommitmentKey(workspaceId, period),
    queryFn: () => getMonthlyCommitment(period),
    staleTime: 0,
  });
}

/**
 * Invalidates everything an occurrence write can move.
 *
 * The month, obviously. But also the recurring reads: an edit reached
 * through `apply_to_period` changes a definition AND a month in one
 * transaction, and a screen showing both must not keep half of the old
 * answer. Invalidating the whole commitment root rather than one period
 * because a definition edit can affect a month other than the one on
 * screen.
 */
function useInvalidateMonth() {
  const qc = useQueryClient();
  return () => {
    const ws = getApiWorkspaceId();
    void qc.invalidateQueries({ queryKey: monthlyCommitmentRootKey(ws) });
    void qc.invalidateQueries({ queryKey: recurringEntriesRootKey(ws) });
    void qc.invalidateQueries({ queryKey: recurringSummaryKey(ws) });
  };
}

export interface OccurrenceTarget {
  entryID: string;
  period: PeriodKey;
}

/**
 * Settles one month. POST, never a toggle.
 *
 * The verb says what it does, so a retry after a timeout means the same
 * thing the first attempt did. The backend makes a repeat a no-op that
 * succeeds and does not move the recorded payment date.
 */
export function useMarkOccurrencePaid(): UseMutationResult<void, Error, OccurrenceTarget> {
  const invalidate = useInvalidateMonth();
  return useMutation({
    mutationFn: ({ entryID, period }: OccurrenceTarget) => markOccurrencePaid(entryID, period),
    onSuccess: invalidate,
  });
}

/** Unsettles one month. DELETE, never a toggle. */
export function useMarkOccurrencePending(): UseMutationResult<void, Error, OccurrenceTarget> {
  const invalidate = useInvalidateMonth();
  return useMutation({
    mutationFn: ({ entryID, period }: OccurrenceTarget) => markOccurrencePending(entryID, period),
    onSuccess: invalidate,
  });
}

export interface SetOccurrenceAmountInput extends OccurrenceTarget {
  /** Integer cents, positive. */
  amountCents: number;
}

/**
 * Records what this month cost. Clears the estimate; settles nothing.
 *
 * `Math.floor` and a floor of 1 mirror `useRecurringEntries`: the field
 * this comes from is text a person typed, and a fractional cent reaching
 * the wire would be rejected by the schema rather than rounded.
 */
export function useSetOccurrenceAmount(): UseMutationResult<void, Error, SetOccurrenceAmountInput> {
  const invalidate = useInvalidateMonth();
  return useMutation({
    mutationFn: ({ entryID, period, amountCents }: SetOccurrenceAmountInput) =>
      setOccurrenceAmount(entryID, period, Math.max(1, Math.floor(amountCents))),
    onSuccess: invalidate,
  });
}
