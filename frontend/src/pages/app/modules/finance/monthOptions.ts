import { currentMonthKey } from "./types";

/**
 * Which months the Finance header offers.
 *
 * ── The defect this replaces ───────────────────────────────────────────
 * The selector used to build its list from `state.transactions`, so a month
 * with no transaction in it simply did not exist as an option. That was
 * survivable while every Finance surface was about transactions. It is not
 * survivable now: a monthly commitment is about OBLIGATIONS, and a month
 * where the operator recorded no spending at all is exactly a month whose
 * bills are worth looking at. Historical navigation, which the monthly view
 * is built around, was unreachable for those months.
 *
 * ── The window, and why it has both halves ─────────────────────────────
 * Twelve months back so "what did I pay last March" is one click, three
 * forward so an upcoming month can be inspected as a projection, and the
 * current month always. Both halves are needed: the backend will
 * reconstruct a past month lazily on first read and will project a future
 * one without writing anything, and neither is reachable from a selector
 * that only knows about months with spending in them.
 *
 * ── Why the transaction months are still unioned in ────────────────────
 * Because the window is a convenience and the ledger is a fact. A
 * transaction from two years ago is real history, and a rolling window
 * that dropped it would make a month the operator can see in the ledger
 * unreachable from the selector that is supposed to reach it.
 */

/** Twelve months back. */
export const MONTHS_BACK = 12;
/** Three months forward: enough to inspect what is coming, not a planner. */
export const MONTHS_FORWARD = 3;

/** `YYYY-MM` for a timestamp, in the reader's own zone. */
export function monthKeyOf(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

/** The first instant of a `YYYY-MM`, as a Date, for formatting only. */
export function monthKeyToDate(key: string): Date {
  const [y, m] = key.split("-").map(Number);
  // UTC, because this is a MONTH and not a moment: built in local time, the
  // first of the month in a negative-offset zone renders as the last day of
  // the previous one.
  return new Date(Date.UTC(y, (m ?? 1) - 1, 1));
}

/**
 * Shifts a `YYYY-MM` by whole months. Goes through UTC date arithmetic
 * rather than string maths so the year boundary is not a special case.
 */
export function shiftMonthKey(key: string, delta: number): string {
  const d = monthKeyToDate(key);
  d.setUTCMonth(d.getUTCMonth() + delta);
  return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}`;
}

/**
 * Every month the selector offers, newest first.
 *
 * Newest first because the months a person actually opens are this one and
 * the one before it, and a chronological list would put them at the bottom
 * of sixteen options.
 *
 * `selected` is unioned in as well: a month reached some other way — a
 * deep link, a value restored from storage — must not vanish from the
 * control that is meant to be showing it.
 */
export function monthOptions(
  transactionDates: readonly number[],
  selected?: string,
  today = new Date(),
): string[] {
  const current = currentMonthKey(today);
  const keys = new Set<string>([current]);

  for (let i = 1; i <= MONTHS_BACK; i++) keys.add(shiftMonthKey(current, -i));
  for (let i = 1; i <= MONTHS_FORWARD; i++) keys.add(shiftMonthKey(current, i));

  for (const ms of transactionDates) keys.add(monthKeyOf(ms));
  if (selected) keys.add(selected);

  // `YYYY-MM` sorts lexicographically exactly as it sorts chronologically,
  // which is half the reason the key is shaped this way.
  return Array.from(keys).sort((a, b) => b.localeCompare(a));
}
