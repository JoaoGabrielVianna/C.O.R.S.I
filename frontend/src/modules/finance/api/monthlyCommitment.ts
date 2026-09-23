/**
 * Typed REST client for the monthly commitment.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE BACKEND COMPUTES THE MONTH; THIS FILE ONLY CARRIES IT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── Why every total is on the wire and none is derived here ────────────
 * Because an annual obligation counted at face value in the wrong month,
 * or a truncated list summed as if it were complete, are mistakes this
 * module has already made once in the browser: the recurring screen used
 * to add `amount` client-side and overstated an annual premium by twelve
 * times its weight. `committed_cents`, `paid_cents`, `remaining_cents` and
 * the five counts arrive already correct, and nothing in the frontend may
 * recompute them from `items`.
 *
 * ── Paid state comes from HERE and from nowhere else ───────────────────
 * Not from transactions. A transaction is money that moved; an occurrence
 * being settled is a separate record the operator asserts. A month with no
 * matching transaction is not an unpaid month, and a payment in the ledger
 * is not a ticked bill.
 *
 * ── Why the writes are two verbs and never a toggle ────────────────────
 * A toggle's meaning depends on a state the caller cannot see, so a retried
 * request undoes the first one and nobody can tell. POST settles, DELETE
 * unsettles, and both are idempotent on the server.
 *
 * Amounts are integer cents on the wire, like every other amount in this
 * module. Dates that are a DAY (`due_on`, `today`, `paid_on`) arrive as
 * `YYYY-MM-DD` and must be rendered with `utcDate`, never with `date`:
 * they are civil dates, not instants, and reading them in the browser's
 * zone moves half of them to the previous day.
 */

import { apiFetch } from "@/lib/api/client";

/** `YYYY-MM`. The canonical month identity, produced by the backend. */
export type PeriodKey = string;

export type OccurrenceStatusWire = "pending" | "paid";

export interface ApiOccurrence {
  /** The definition this month belongs to. Half of a write's target. */
  recurring_entry_id: string;
  /**
   * Absent on a PROJECTED month: those rows do not exist, and an id that
   * resolves to nothing is worse than none. Its absence is how a surface
   * knows the month is arithmetic rather than record.
   */
  occurrence_id?: string;
  /** The other half of a write's target. */
  period: PeriodKey;
  description: string;
  category_id?: string;
  category?: string;
  direction?: "income" | "expense";
  /** Civil date, `YYYY-MM-DD`, already clamped to a day the month has. */
  due_on: string;
  amount_cents: number;
  /** True when this figure is not a confirmed fact about this month. */
  amount_estimated: boolean;
  status: OccurrenceStatusWire;
  /** Civil date. Present only when settled. */
  paid_on?: string;
  /** Derived server-side against the authoritative today. Never recomputed. */
  overdue: boolean;
}

/**
 * A definition the month could not place: an annual recurrence written
 * before `due_month` existed, so nothing knows which month it falls in.
 *
 * Non-empty means the totals are INCOMPLETE and a surface has to say so.
 */
export interface ApiUnplaceableEntry {
  recurring_entry_id: string;
  description: string;
  /** Closed vocabulary. Today the only value is `missing_due_month`. */
  reason: string;
}

export interface ApiMonthlyCommitment {
  period: PeriodKey;
  /** True for a month that has not begun: computed, never stored. */
  is_projection: boolean;
  /** The authoritative date this month was cut against. Civil date. */
  today: string;
  time_zone: string;

  committed_cents: number;
  paid_cents: number;
  remaining_cents: number;
  estimated_cents: number;

  occurrence_count: number;
  paid_count: number;
  pending_count: number;
  estimated_count: number;
  overdue_count: number;

  items: ApiOccurrence[];
  unplaceable?: ApiUnplaceableEntry[];
}

/**
 * Reads one month.
 *
 * `period` omitted asks the SERVER which month is current. That is the only
 * correct way to ask: the browser's clock is in the reader's zone and the
 * month is cut in `FINANCE_TIMEZONE`, so on the last evening of a month the
 * two disagree.
 */
export function getMonthlyCommitment(period?: PeriodKey): Promise<ApiMonthlyCommitment> {
  const query = period ? `?period=${encodeURIComponent(period)}` : "";
  return apiFetch<ApiMonthlyCommitment>(`/finance/recurring-entries/commitment${query}`);
}

/** The occurrence a write targets, addressed by what a caller can know. */
function occurrencePath(entryID: string, period: PeriodKey): string {
  return `/finance/recurring-entries/${encodeURIComponent(entryID)}/occurrences/${encodeURIComponent(period)}`;
}

/** Settles one month. Idempotent: a repeat succeeds and moves nothing. */
export function markOccurrencePaid(entryID: string, period: PeriodKey): Promise<void> {
  return apiFetch<void>(`${occurrencePath(entryID, period)}/pay`, { method: "POST" });
}

/**
 * Unsettles one month. Idempotent in the same way.
 *
 * It deletes no transaction and changes no amount: being wrong about
 * whether a bill was paid says nothing about whether the figure was right.
 */
export function markOccurrencePending(entryID: string, period: PeriodKey): Promise<void> {
  return apiFetch<void>(`${occurrencePath(entryID, period)}/pay`, { method: "DELETE" });
}

/**
 * Records what this month actually cost, for a bill whose amount varies.
 *
 * It clears the estimate and does NOT settle the bill. It also leaves the
 * recurrence's usual amount alone: "a luz veio 437,20" is a fact about one
 * month, not a change to what the recurrence normally costs.
 *
 * A month already marked paid is refused by the backend with 409 — the
 * operator unsettles it, changes the figure, and settles it again.
 */
export function setOccurrenceAmount(
  entryID: string,
  period: PeriodKey,
  amountCents: number,
): Promise<void> {
  return apiFetch<void>(occurrencePath(entryID, period), {
    method: "PATCH",
    body: JSON.stringify({ amount_cents: amountCents }),
  });
}
