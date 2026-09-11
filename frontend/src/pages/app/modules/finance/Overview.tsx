import { useMemo, useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import { BarChart, DonutChart, HorizontalBars, StackedBar } from "./FinanceCharts";
import { SourcesPanel } from "./SourcesPanel";
import { formatBRL, formatBRLCompact, personShare } from "./format";
import type { FinanceStore } from "./store";
import type { Cents, PaymentMethod } from "./types";
import { useTransactionTotals } from "@/modules/finance/hooks/useTransactions";

type Props = { store: FinanceStore };

const METHOD_COLOR: Record<PaymentMethod, string> = {
  debit:    "sky",
  credit:   "violet",
  pix:      "emerald",
  cash:     "amber",
  transfer: "indigo",
};

/**
 * Overview — dense Linear-style dashboard.
 *
 *   Row 1 · 4 KPI cards (Revenue · Expenses · Balance · Projected end-of-month)
 *   Row 2 · By category (6 cols) + Monthly evolution (6 cols)
 *   Row 3 · By person   (4 cols) + Payment methods   (8 cols)
 *   Row 4 · SourcesPanel — future ingestion paths
 *
 * Month is driven by `state.filters.month` so the header selector controls
 * both this view and the Transactions tab.
 *
 * Totals contract surface (docs/totals-contract.md):
 *   • KPI tiles → `GET /finance/transactions/totals` via
 *     `useTransactionTotals`. The contract's realized / projected /
 *     excluded buckets are server-computed; the dashboard never
 *     hand-rolls them. Transfer legs are excluded server-side.
 *   • Charts (by category, by person, by method, monthly evolution) →
 *     intentionally aggregated client-side from `state.transactions`.
 *     The totals contract scopes ONLY the tile buckets; charts are
 *     presentation aggregations that need per-person sharing (local
 *     concept), category color tokens (FE state), etc. Keeping them
 *     local for the MVP. Future iteration could promote `by_category`
 *     and `by_method` to server reads once those breakdowns are
 *     stabilized — but it's a follow-up, not a freeze blocker.
 */
export function Overview({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.finance.overview;
  const { state, peopleById, categoriesById } = store;

  const month = state.filters.month;
  const { monthStart, monthEnd, prevStart, prevEnd } = useMemo(() => boundsFor(month), [month]);

  const monthTx = useMemo(
    () => state.transactions.filter((tx) => tx.date >= monthStart && tx.date <= monthEnd),
    [state.transactions, monthStart, monthEnd],
  );

  // KPI tiles consume the backend totals contract (realized / projected /
  // excluded). Charts below still aggregate the local merged feed since
  // their breakdowns aren't part of the dashboard totals contract.
  const monthWindow = useMemo(
    () => ({ from: new Date(monthStart).toISOString(), to: new Date(monthEnd).toISOString() }),
    [monthStart, monthEnd],
  );
  const prevWindow = useMemo(
    () => ({ from: new Date(prevStart).toISOString(), to: new Date(prevEnd).toISOString() }),
    [prevStart, prevEnd],
  );
  const totalsQuery     = useTransactionTotals(monthWindow);
  const prevTotalsQuery = useTransactionTotals(prevWindow);

  const realized   = totalsQuery.data?.by_realization.realized;
  const projectedB = totalsQuery.data?.by_realization.projected;
  const prevRealized = prevTotalsQuery.data?.by_realization.realized;

  const income     = realized?.income_cents ?? 0;
  const expenses   = realized?.expense_cents ?? 0;
  const balance    = income - expenses;
  const prevIncome   = prevRealized?.income_cents ?? 0;
  const prevExpenses = prevRealized?.expense_cents ?? 0;
  const prevBalance  = prevIncome - prevExpenses;

  // Projected = realized balance + projected income − projected expense.
  // Backend's `projected` bucket already covers pending/scheduled/future-
  // dated paid rows per the contract; transfer legs are in `excluded`
  // and never enter either side, so this sum honors invariants §§3, 4.
  const projected = balance
    + (projectedB?.income_cents ?? 0)
    - (projectedB?.expense_cents ?? 0);

  // ── Charts ──────────────────────────────────────────────────

  const byCategory = useMemo(() => {
    const map = new Map<string, Cents>();
    for (const tx of monthTx) {
      if (tx.type !== "expense" || tx.status !== "paid") continue;
      const key = tx.categoryId ?? "";
      map.set(key, (map.get(key) ?? 0) + tx.amount);
    }
    return Array.from(map.entries())
      .map(([categoryId, value]) => {
        const c = categoriesById.get(categoryId);
        return { key: categoryId, label: c?.name ?? "—", color: c?.color ?? "slate", value };
      })
      .sort((a, b) => b.value - a.value)
      .slice(0, 7);
  }, [monthTx, categoriesById]);

  const byPerson = useMemo(() => {
    // Use `personShare` so split transactions count proportionally for each person.
    const map = new Map<string, Cents>();
    for (const tx of monthTx) {
      if (tx.type !== "expense" || tx.status !== "paid") continue;
      for (const p of state.people) {
        const share = personShare(tx, p.id);
        if (share === 0) continue;
        map.set(p.id, (map.get(p.id) ?? 0) + share);
      }
    }
    return Array.from(map.entries())
      .filter(([, value]) => value > 0)
      .map(([personId, value], i) => {
        const p = peopleById.get(personId);
        return {
          key: personId,
          label: p?.name ?? "—",
          color: i === 0 ? "violet" : i === 1 ? "sky" : "emerald",
          value,
        };
      });
  }, [monthTx, peopleById, state.people]);

  const byMethod = useMemo(() => {
    const map = new Map<PaymentMethod, Cents>();
    for (const tx of monthTx) {
      if (tx.type !== "expense" || tx.status !== "paid") continue;
      map.set(tx.paymentMethod, (map.get(tx.paymentMethod) ?? 0) + tx.amount);
    }
    return Array.from(map.entries())
      .map(([method, value]) => ({
        key: method,
        label: t.app.modules.finance.methodLabels[method],
        color: METHOD_COLOR[method],
        value,
      }))
      .sort((a, b) => b.value - a.value);
  }, [monthTx, t.app.modules.finance.methodLabels]);

  return (
    <div className="flex flex-col gap-3 overflow-y-auto pb-2">
      {/* KPIs */}
      <section className="grid grid-cols-12 gap-3">
        <Kpi
          label={labels.tiles.income}
          value={formatBRL(income)}
          delta={percentDelta(income, prevIncome)}
          tone="emerald"
        />
        <Kpi
          label={labels.tiles.expenses}
          value={formatBRL(expenses)}
          delta={percentDelta(expenses, prevExpenses)}
          tone="rose"
          invertDelta
        />
        <Kpi
          label={labels.tiles.balance}
          value={formatBRL(balance)}
          delta={percentDelta(balance, prevBalance)}
          tone={balance >= 0 ? "emerald" : "rose"}
        />
        <Kpi
          label={labels.tiles.projected}
          value={formatBRL(projected)}
          delta={null}
          tone={projected >= 0 ? "sky" : "rose"}
        />
      </section>

      {/* Charts row 1 */}
      <section className="grid grid-cols-12 gap-3">
        <Panel
          title={labels.charts.byCategory.title}
          hint={labels.charts.byCategory.hint}
          className="col-span-12 lg:col-span-6"
        >
          <div className="min-h-[180px] max-h-[220px] overflow-y-auto pr-1">
            <HorizontalBars segments={byCategory} format={formatBRL} />
          </div>
        </Panel>
        <MonthlyEvolution store={store} className="col-span-12 lg:col-span-6" />
      </section>

      {/* Charts row 2 */}
      <section className="grid grid-cols-12 gap-3">
        <Panel
          title={labels.charts.byPerson.title}
          hint={labels.charts.byPerson.hint}
          className="col-span-12 lg:col-span-4"
        >
          <div className="min-h-[180px]">
            <DonutChart
              segments={byPerson}
              size={140}
              thickness={14}
              centerLabel={labels.charts.byPerson.title}
              centerValue={formatBRLCompact(expenses)}
            />
          </div>
        </Panel>
        <Panel
          title={labels.charts.byMethod.title}
          hint={labels.charts.byMethod.hint}
          className="col-span-12 lg:col-span-8"
        >
          <div className="min-h-[180px]">
            <StackedBar segments={byMethod} format={formatBRL} />
          </div>
        </Panel>
      </section>

      {/* Sources */}
      <SourcesPanel />
    </div>
  );
}

/* ── KPI tile ─────────────────────────────────────────────────────────── */

function Kpi({
  label,
  value,
  delta,
  tone,
  invertDelta = false,
}: {
  label: string;
  value: string;
  delta: number | null;
  tone: string;
  invertDelta?: boolean;
}) {
  const t = useT();
  const positive = delta !== null && (invertDelta ? delta < 0 : delta > 0);
  const negative = delta !== null && (invertDelta ? delta > 0 : delta < 0);
  const Arrow = delta !== null && delta > 0 ? ArrowUp : ArrowDown;
  return (
    <article className="col-span-12 sm:col-span-6 lg:col-span-3 rounded-xl border border-(--color-border) bg-(--color-card) p-3.5">
      <header className="flex items-center justify-between">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {label}
        </p>
        <span aria-hidden className={cn("size-1.5 rounded-full", `bg-${tone}-500`)} />
      </header>
      <p className="mt-2 font-display text-lg font-semibold tracking-tight text-(--color-foreground)">
        {value}
      </p>
      {delta !== null ? (
        <p
          className={cn(
            "mt-1 inline-flex items-center gap-1 font-mono text-[10px]",
            positive ? "text-emerald-700 dark:text-emerald-300" :
            negative ? "text-rose-700 dark:text-rose-300" :
            "text-(--color-muted-foreground)",
          )}
        >
          <Arrow className="size-2.5" />
          {Math.abs(delta).toFixed(0)}%
          <span className="text-(--color-muted-foreground)/80">{t.app.modules.finance.overview.delta.up}</span>
        </p>
      ) : (
        <p className="mt-1 font-mono text-[10px] text-(--color-muted-foreground)/80">
          {t.app.modules.finance.overview.delta.none}
        </p>
      )}
    </article>
  );
}

/* ── Panel shell ──────────────────────────────────────────────────────── */

function Panel({
  title,
  hint,
  className,
  actions,
  children,
}: {
  title: string;
  hint?: string;
  className?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className={cn("rounded-xl border border-(--color-border) bg-(--color-card) p-3.5", className)}>
      <header className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">
            {title}
          </p>
          {hint ? <p className="mt-0.5 text-[11px] text-(--color-muted-foreground)">{hint}</p> : null}
        </div>
        {actions ? <div className="flex shrink-0 items-center gap-1">{actions}</div> : null}
      </header>
      {children}
    </section>
  );
}

/* ── Monthly evolution (with Month/Year toggle) ───────────────────────── */

function MonthlyEvolution({ store, className }: { store: FinanceStore; className?: string }) {
  const t = useT();
  const labels = t.app.modules.finance.overview.charts.monthly;
  const [scope, setScope] = useState<"month" | "year">("month");

  const segments = useMemo(() => {
    const map = new Map<string, { income: Cents; expense: Cents }>();
    for (const tx of store.state.transactions) {
      if (tx.status !== "paid") continue;
      if (tx.type === "transfer") continue;
      const key = scope === "month"
        ? `${new Date(tx.date).getFullYear()}-${String(new Date(tx.date).getMonth() + 1).padStart(2, "0")}`
        : String(new Date(tx.date).getFullYear());
      const entry = map.get(key) ?? { income: 0, expense: 0 };
      if (tx.type === "income") entry.income += tx.amount;
      else entry.expense += tx.amount;
      map.set(key, entry);
    }
    const entries = Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]));
    const horizon = scope === "month" ? 6 : 4;
    return entries.slice(-horizon).map(([key, value]) => ({
      key,
      label: scope === "month" ? key.slice(5) : key,
      color: "cyan",
      value: value.income - value.expense,
    }));
  }, [store.state.transactions, scope]);

  return (
    <Panel
      title={labels.title}
      hint={labels.hint}
      className={className}
      actions={
        <div className="flex rounded-md border border-(--color-border) bg-(--color-card) p-0.5">
          <button
            type="button"
            onClick={() => setScope("month")}
            className={cn(
              "rounded px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.14em] transition-colors",
              scope === "month"
                ? "bg-(--color-muted) text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            {labels.toggleMonth}
          </button>
          <button
            type="button"
            onClick={() => setScope("year")}
            className={cn(
              "rounded px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.14em] transition-colors",
              scope === "year"
                ? "bg-(--color-muted) text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            {labels.toggleYear}
          </button>
        </div>
      }
    >
      <div className="min-h-[180px]">
        <BarChart segments={segments} height={180} format={(v) => formatBRLCompact(v)} />
      </div>
    </Panel>
  );
}

/* ── Helpers ─────────────────────────────────────────────────────────── */

function boundsFor(monthKey: string): {
  monthStart: number;
  monthEnd: number;
  prevStart: number;
  prevEnd: number;
} {
  const [y, m] = monthKey.split("-").map(Number);
  const monthStart = new Date(y, m - 1, 1).getTime();
  const monthEnd   = new Date(y, m, 0, 23, 59, 59, 999).getTime();
  const prevStart  = new Date(y, m - 2, 1).getTime();
  const prevEnd    = new Date(y, m - 1, 0, 23, 59, 59, 999).getTime();
  return { monthStart, monthEnd, prevStart, prevEnd };
}

function percentDelta(current: Cents, previous: Cents): number | null {
  if (previous === 0) return current === 0 ? null : 100;
  return ((current - previous) / Math.abs(previous)) * 100;
}
