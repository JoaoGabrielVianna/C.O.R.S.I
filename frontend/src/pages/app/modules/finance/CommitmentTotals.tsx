import { CircleAlert, Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";
import { useFormat, useT } from "@/lib/i18n";
import { formatBRL } from "./format";
import type { ApiMonthlyCommitment } from "@/modules/finance/api/monthlyCommitment";

/**
 * The month, in three numbers.
 *
 * ── Why REMAINING is the big one ───────────────────────────────────────
 * Because the question this surface exists to answer in two seconds is
 * "quanto ainda falta pagar". Committed and paid are context for it. A
 * layout that gave all three equal weight would be an accounting summary,
 * and the operator would have to do the subtraction with their eyes every
 * time they opened the tab.
 *
 * ── Why nothing here is computed ───────────────────────────────────────
 * Every figure and every count is read straight off the payload. The
 * component does not sum `items`, does not subtract paid from committed,
 * and does not count what is overdue. That is not caution for its own
 * sake: an annual premium belongs whole to one month and a twelfth to the
 * recurring summary, a reconstructed past month is partly estimates, and a
 * truncated list is not the whole month. The server knows all three and a
 * loop over rows knows none of them.
 */

type Props = { data: ApiMonthlyCommitment };

export function CommitmentTotals({ data }: Props) {
  const t = useT();
  const fmt = useFormat();
  const labels = t.app.modules.finance.recurring.month;

  const total = data.occurrence_count;
  const done = data.paid_count;
  // Guarded because a month with no obligations is a real, reachable state
  // and 0/0 renders as NaN%.
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;

  return (
    <section
      aria-label={labels.summaryLabel}
      className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-soft)"
    >
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
        {/* The answer, first and largest. */}
        <div className="min-w-0">
          <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {labels.remaining}
          </p>
          <p className="font-display text-2xl font-semibold tracking-tight text-(--color-foreground) tabular-nums sm:text-3xl">
            {formatBRL(data.remaining_cents)}
          </p>
        </div>

        {/* Its context, quieter and side by side. */}
        <dl className="flex flex-wrap items-end gap-x-6 gap-y-2">
          <div>
            <dt className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {labels.committed}
            </dt>
            <dd className="font-mono text-[13.5px] text-(--color-foreground) tabular-nums">
              {formatBRL(data.committed_cents)}
            </dd>
          </div>
          <div>
            <dt className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {labels.paid}
            </dt>
            <dd className="font-mono text-[13.5px] text-(--color-foreground) tabular-nums">
              {formatBRL(data.paid_cents)}
            </dd>
          </div>
        </dl>
      </div>

      {/* Progress. `role="img"` with a label rather than a progressbar,
          because the number of bills settled is not a task completing —
          and the same sentence is printed beside it in text, so the bar is
          decoration over a fact that is already readable. */}
      <div className="mt-3.5 flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div
          role="img"
          aria-label={fmt.plural(done, labels.progressLabel).replace("{total}", String(total))}
          className="h-1.5 min-w-32 flex-1 overflow-hidden rounded-full bg-(--color-muted)"
        >
          <div
            className="h-full rounded-full bg-(--color-brand-500) transition-[width] duration-300"
            style={{ width: `${pct}%` }}
          />
        </div>
        <p className="font-mono text-[11px] text-(--color-muted-foreground) tabular-nums">
          {labels.progress
            .replace("{paid}", fmt.number(done))
            .replace("{total}", fmt.number(total))}
        </p>
      </div>

      {/* The two honesty lines. Both are facts about how much of the number
          above can be trusted, so they sit directly under it rather than in
          a tooltip. */}
      {data.overdue_count > 0 ? (
        <p className={cn(
          "mt-2 inline-flex items-center gap-1.5 text-[12px] font-medium",
          "text-amber-700 dark:text-amber-300",
        )}>
          <CircleAlert className="size-3.5 shrink-0" aria-hidden />
          {fmt.plural(data.overdue_count, labels.overdueCount)}
        </p>
      ) : null}

      {data.estimated_count > 0 ? (
        <p className="mt-1.5 inline-flex items-center gap-1.5 text-[12px] text-(--color-muted-foreground)">
          <Sparkles className="size-3.5 shrink-0" aria-hidden />
          {fmt
            .plural(data.estimated_count, labels.estimatedNote)
            .replace("{amount}", formatBRL(data.estimated_cents))}
        </p>
      ) : null}
    </section>
  );
}
