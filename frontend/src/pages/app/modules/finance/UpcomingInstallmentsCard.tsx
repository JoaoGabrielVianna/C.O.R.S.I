import { useMemo, useState } from "react";
import { CalendarClock } from "lucide-react";
import { cn } from "@/lib/utils";
import { formatBRL, formatShortDate } from "./format";
import type { FinanceStore } from "./store";
import { useT } from "@/lib/i18n";

type Props = {
  store: FinanceStore;
  className?: string;
  /** How many upcoming rows to surface. Defaults to 6. */
  limit?: number;
};

/**
 * UpcomingInstallmentsCard — surface the next few non-paid installment
 * transactions across all active plans.
 *
 * Reads from `state.transactions` (which is the merged backend feed in
 * the store) and filters by `planId != null && status !== "paid"`. No
 * plan-level math runs here — each row is its own scheduled/pending
 * transaction, classified per the totals contract.
 */
export function UpcomingInstallmentsCard({ store, className, limit = 6 }: Props) {
  const t = useT();
  const { state, purchasePlansById, cardsById, categoriesById } = store;
  // Snapshot once on mount — "overdue" is a soft hint, not a number anyone
  // sums up. Re-mounting on tab switches keeps it close enough to wall.
  const [now] = useState(() => Date.now());

  const rows = useMemo(() => {
    const items = state.transactions
      .filter((tx) => tx.planId != null && tx.status !== "paid")
      .sort((a, b) => a.date - b.date);

    return items.slice(0, limit).map((tx) => {
      const plan = tx.planId ? purchasePlansById.get(tx.planId) : undefined;
      const card = tx.accountId ? cardsById.get(tx.accountId) : undefined;
      const cat = tx.categoryId ? categoriesById.get(tx.categoryId) : undefined;
      const total = plan?.totalInstallments ?? 0;
      const number = tx.installmentNumber ?? 0;
      const remaining = total > 0 ? Math.max(0, total - number + 1) : 0;
      const overdue = tx.status === "pending" && tx.date < now;
      return { tx, plan, card, cat, total, number, remaining, overdue };
    });
  }, [state.transactions, purchasePlansById, cardsById, categoriesById, limit, now]);

  const totalUpcoming = useMemo(
    () =>
      state.transactions
        .filter((tx) => tx.planId != null && tx.status !== "paid")
        .reduce((sum, tx) => sum + tx.amount, 0),
    [state.transactions],
  );

  return (
    <section
      className={cn(
        "rounded-xl border border-(--color-border) bg-(--color-card) p-3.5",
        className,
      )}
    >
      <header className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">
            {t.app.modules.finance.installmentsCard.title}
          </p>
          <p className="mt-0.5 text-[11px] text-(--color-muted-foreground)">
            {t.app.modules.finance.plans.upcomingSummary
              .replace("{count}", String(rows.length))
              .replace("{amount}", formatBRL(totalUpcoming))}
          </p>
        </div>
        <span
          aria-hidden
          className="inline-flex size-7 items-center justify-center rounded-md border border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)"
        >
          <CalendarClock className="size-3.5" />
        </span>
      </header>

      {rows.length === 0 ? (
        <p className="rounded-lg border border-dashed border-(--color-border) px-3 py-6 text-center text-[12px] text-(--color-muted-foreground)">
          {t.app.modules.finance.installmentsCard.empty}
        </p>
      ) : (
        <ul className="divide-y divide-(--color-border)">
          {rows.map(({ tx, plan, card, cat, total, number, remaining, overdue }) => (
            <li key={tx.id} className="grid grid-cols-[1fr_auto] items-center gap-3 py-2">
              <div className="min-w-0">
                <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">
                  {plan?.description ?? tx.description}
                  {total > 0 ? (
                    <span className="ml-1.5 font-mono text-[10.5px] text-(--color-muted-foreground)">
                      {number}/{total}
                    </span>
                  ) : null}
                </p>
                <p className="mt-0.5 truncate font-mono text-[10.5px] text-(--color-muted-foreground)">
                  {formatShortDate(tx.date)}
                  {card ? <> · {card.name}</> : null}
                  {cat ? <> · {cat.name}</> : null}
                  {remaining > 0 ? (
                    <>{" "}{t.app.modules.finance.plans.installmentsLeft.replace("{count}", String(remaining))}</>
                  ) : null}
                </p>
              </div>
              <div className="text-right">
                <p className="font-mono text-[12px] font-semibold text-(--color-foreground)">
                  {formatBRL(tx.amount)}
                </p>
                <p
                  className={cn(
                    "mt-0.5 font-mono text-[9.5px] uppercase tracking-[0.14em]",
                    overdue
                      ? "text-rose-700 dark:text-rose-300"
                      : "text-(--color-muted-foreground)",
                  )}
                >
                  {overdue ? "overdue" : tx.status}
                </p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
