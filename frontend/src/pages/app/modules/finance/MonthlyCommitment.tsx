import { useState } from "react";
import { CalendarClock, CircleAlert, Loader2, TriangleAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import { useFormat, useT } from "@/lib/i18n";
import {
  useMarkOccurrencePaid,
  useMarkOccurrencePending,
  useMonthlyCommitment,
  useSetOccurrenceAmount,
} from "@/modules/finance/hooks/useMonthlyCommitment";
import type { ApiUnplaceableEntry } from "@/modules/finance/api/monthlyCommitment";
import { CommitmentTotals } from "./CommitmentTotals";
import { OccurrenceRow } from "./OccurrenceRow";
import { monthKeyToDate } from "./monthOptions";
import type { FinanceStore } from "./store";
import type { RecurringEntry } from "./types";

/**
 * "O que falta pagar esse mês", as a screen.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE MONTH IS THE SURFACE; THE RECURRENCES ARE ITS CONFIGURATION
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── Why the hierarchy inverted ─────────────────────────────────────────
 * The tab used to open on the list of recurrences, which answers "what do I
 * pay every month" — a question somebody asks when they are setting things
 * up, perhaps twice a year. The question they ask on a Tuesday is "what
 * have I not paid yet", and that had no answer anywhere in the product.
 * So the month came to the top and the definitions moved below it.
 *
 * ── Why this component fetches and the rest only render ────────────────
 * One read per month, here, and the two children are given a payload. That
 * keeps the rule that matters enforceable in one place: nothing below this
 * line recomputes a total, and neither child has a way to.
 *
 * ── Why the month comes from the global filter ─────────────────────────
 * Because the Finance header already has a month selector driving Overview
 * and Transactions, and a second one inside this tab would be two controls
 * for one concept — reliably out of sync the first time somebody changed
 * one and looked at the other.
 */

type Props = {
  store: FinanceStore;
  /** Opens the definitions form, so a repair prompt has somewhere to go. */
  onEditDefinition: (entry: RecurringEntry) => void;
};

export function MonthlyCommitment({ store, onEditDefinition }: Props) {
  const t = useT();
  const fmt = useFormat();
  const labels = t.app.modules.finance.recurring.month;

  const period = store.state.filters.month;
  const query = useMonthlyCommitment(period);
  const markPaid = useMarkOccurrencePaid();
  const markPending = useMarkOccurrencePending();
  const setAmount = useSetOccurrenceAmount();

  // Which row currently owns a request. Scoped to the row rather than
  // global so settling one bill does not freeze the other four, and
  // per-row rather than a boolean so a second click on the SAME row while
  // the first is in flight cannot queue a duplicate.
  const [busyEntry, setBusyEntry] = useState<string | null>(null);

  const data = query.data;
  const isProjection = data?.is_projection ?? false;
  // The current month, decided by the SERVER: `today` is the Finance
  // clock's date in the reporting zone, so comparing its month to the one
  // being viewed never disagrees with the backend about which is which.
  const isCurrentMonth = data ? data.today.slice(0, 7) === data.period : false;
  const isPast = data ? !isProjection && !isCurrentMonth : false;

  const run = async (entryID: string, fn: () => Promise<unknown>) => {
    if (busyEntry) return; // no request spam, and no second write per row
    setBusyEntry(entryID);
    try {
      await fn();
    } catch {
      // The mutation's own error state is what the banner below reads.
      // Swallowed here so an unhandled rejection does not escape a click.
    } finally {
      setBusyEntry(null);
    }
  };

  const mutationError = markPaid.error ?? markPending.error ?? setAmount.error;

  return (
    <div className="flex flex-col gap-3">
      <header className="flex flex-wrap items-baseline justify-between gap-2">
        <div className="flex flex-wrap items-baseline gap-2">
          <h2 className="font-display text-base font-semibold tracking-tight text-(--color-foreground)">
            {fmt.utcDate(monthKeyToDate(period), "monthYear")}
          </h2>
          {isProjection ? (
            <span className="rounded-full border border-(--color-border) bg-(--color-muted) px-1.5 py-px font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {labels.projection}
            </span>
          ) : null}
        </div>
        {query.isFetching ? (
          <span className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            <Loader2 className="size-3 animate-spin" aria-hidden />
            {labels.loading}
          </span>
        ) : null}
      </header>

      {query.isError ? (
        <Banner tone="error" icon={<CircleAlert className="size-3.5 shrink-0" />}>
          <span>{labels.error}</span>
          <button
            type="button"
            onClick={() => void query.refetch()}
            className="ml-2 underline underline-offset-2 hover:no-underline"
          >
            {labels.retry}
          </button>
        </Banner>
      ) : null}

      {mutationError ? (
        <Banner tone="error" icon={<CircleAlert className="size-3.5 shrink-0" />}>
          {labels.writeError}
        </Banner>
      ) : null}

      {data ? (
        <>
          <CommitmentTotals data={data} />

          {/* The two honesty notes about what the figures above ARE. Both
              are about provenance, not about failure, so they read as
              context rather than as errors. */}
          {isProjection ? (
            <Banner tone="muted" icon={<CalendarClock className="size-3.5 shrink-0" />}>
              {labels.projectionHint}
            </Banner>
          ) : null}
          {isPast && data.estimated_count > 0 ? (
            <Banner tone="muted" icon={<CalendarClock className="size-3.5 shrink-0" />}>
              {labels.reconstructedHint}
            </Banner>
          ) : null}

          {data.unplaceable && data.unplaceable.length > 0 ? (
            <UnplaceableNotice
              entries={data.unplaceable}
              store={store}
              onEditDefinition={onEditDefinition}
            />
          ) : null}

          <section className="overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
            {data.items.length === 0 ? (
              <p className="px-5 py-8 text-center text-[12.5px] text-(--color-muted-foreground)">
                {labels.empty}
              </p>
            ) : (
              <ul className="divide-y divide-(--color-border)">
                {data.items.map((o) => {
                  const cat = o.category_id ? store.categoriesById.get(o.category_id) : undefined;
                  return (
                    <OccurrenceRow
                      key={`${o.recurring_entry_id}:${o.period}`}
                      occurrence={o}
                      categoryIcon={cat?.icon}
                      categoryColor={cat?.color}
                      // A projected month has no rows to write to. The
                      // backend refuses it too; disabling here means the
                      // operator is not invited into a refusal.
                      writable={!isProjection}
                      busy={busyEntry === o.recurring_entry_id}
                      onMarkPaid={() =>
                        void run(o.recurring_entry_id, () =>
                          markPaid.mutateAsync({ entryID: o.recurring_entry_id, period: o.period }))
                      }
                      onMarkPending={() =>
                        void run(o.recurring_entry_id, () =>
                          markPending.mutateAsync({ entryID: o.recurring_entry_id, period: o.period }))
                      }
                      onSetAmount={(cents) =>
                        void run(o.recurring_entry_id, () =>
                          setAmount.mutateAsync({
                            entryID: o.recurring_entry_id, period: o.period, amountCents: cents,
                          }))
                      }
                    />
                  );
                })}
              </ul>
            )}
          </section>

          {/* Why a settled bill offers no pencil. Shown only when there is
              a settled bill to be puzzled by. */}
          {!isProjection && data.paid_count > 0 ? (
            <p className="px-1 font-mono text-[10px] text-(--color-muted-foreground)/80">
              {labels.amount.lockedHint}
            </p>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

/**
 * The legacy annual rows nothing can place.
 *
 * ── Why this is stated and not hidden ──────────────────────────────────
 * Because while it is present the totals above are INCOMPLETE, and a total
 * that is quietly short is worse than no total: the operator reads a
 * smaller "remaining" and believes it. It is data repair rather than a
 * failure, so it is a compact strip with a way to fix it, not a blocking
 * screen — and it never guesses a month on the operator's behalf.
 */
function UnplaceableNotice({
  entries,
  store,
  onEditDefinition,
}: {
  entries: ApiUnplaceableEntry[];
  store: FinanceStore;
  onEditDefinition: (entry: RecurringEntry) => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.recurring.month.unplaceable;

  return (
    <Banner tone="warning" icon={<TriangleAlert className="size-3.5 shrink-0" />}>
      <span className="block font-medium">{labels.title}</span>
      <span className="block text-[11.5px] opacity-90">{labels.body}</span>
      <span className="mt-1 flex flex-wrap gap-1.5">
        {entries.map((u) => {
          const entry = store.state.recurringEntries.find((e) => e.id === u.recurring_entry_id);
          return (
            <button
              key={u.recurring_entry_id}
              type="button"
              disabled={!entry}
              onClick={() => entry && onEditDefinition(entry)}
              className="inline-flex items-center gap-1 rounded-md border border-current/25 px-1.5 py-0.5 text-[11.5px] font-medium hover:bg-current/10 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {u.description}
              <span className="opacity-70">· {labels.fix}</span>
            </button>
          );
        })}
      </span>
    </Banner>
  );
}

function Banner({
  tone,
  icon,
  children,
}: {
  tone: "error" | "warning" | "muted";
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={cn(
        "flex items-start gap-2 rounded-xl border px-3 py-2 text-[12px]",
        tone === "error"
          ? "border-rose-300 bg-rose-50 text-rose-800 dark:border-rose-800 dark:bg-rose-500/10 dark:text-rose-200"
          : tone === "warning"
            ? "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-500/10 dark:text-amber-200"
            : "border-(--color-border) bg-(--color-muted)/50 text-(--color-muted-foreground)",
      )}
    >
      <span className="mt-px" aria-hidden>{icon}</span>
      <span className="min-w-0 flex-1">{children}</span>
    </div>
  );
}
