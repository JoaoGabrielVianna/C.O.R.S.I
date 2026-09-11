import { useMemo } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowDownLeft, ArrowLeft, ArrowUpRight, CreditCard as CreditCardIcon, Users, Wallet } from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import { useFinance } from "@/pages/app/modules/finance/store";
import { BarChart, HorizontalBars } from "@/pages/app/modules/finance/FinanceCharts";
import { CompactCardRow } from "@/pages/app/modules/finance/CompactCardRow";
import { OriginBadge } from "@/pages/app/modules/finance/OriginBadge";
import { CategoryIcon } from "@/pages/app/modules/finance/CategoryIcon";
import {
  endOfMonth,
  formatBRL,
  formatBRLCompact,
  formatShortDate,
  isShared,
  personShare,
  startOfMonth,
} from "@/pages/app/modules/finance/format";
import type { Cents, Transaction } from "@/pages/app/modules/finance/types";
import { useArchiveCard } from "@/modules/finance/hooks/useCards";

/**
 * PersonDetail · /app/people/:id
 *
 * Workspace-level page that aggregates everything Finance knows about one
 * person, with shared expenses correctly proportioned via `personShare()`.
 * Today it reads from the Finance store directly; when Person graduates to
 * a workspace-level lib the import surface stays the same.
 *
 *   ┌─ Header · avatar · name · role · "back to People" ─┐
 *   ├─ 4 KPI tiles · monthly income · expenses · balance · card debts
 *   ├─ Monthly summaries · last 6 months (line + table)
 *   ├─ By category · horizontal bars (this month)
 *   ├─ Cards owned · CompactCardRow list
 *   ├─ Fixed expenses owed
 *   └─ Recent transactions · last 20
 */
export function PersonDetailPage() {
  const t = useT();
  const labels = t.app.personDetail;
  const { id } = useParams<{ id: string }>();
  const store = useFinance();
  const archiveCard = useArchiveCard();

  const person = id ? store.peopleById.get(id) ?? null : null;

  // ── Computed (always, even when person is null, to keep hook order stable) ──
  const monthly = useMemo(() => {
    if (!person) return null;
    return computeMonthlyBreakdown(store.state.transactions, person.id, 6);
  }, [store.state.transactions, person]);

  const byCategory = useMemo(() => {
    if (!person) return [];
    const start = startOfMonth(new Date());
    const end = endOfMonth(new Date());
    const map = new Map<string, Cents>();
    for (const tx of store.state.transactions) {
      if (tx.type !== "expense" || tx.status !== "paid") continue;
      if (tx.date < start || tx.date > end) continue;
      const share = personShare(tx, person.id);
      if (share === 0) continue;
      const key = tx.categoryId ?? "";
      map.set(key, (map.get(key) ?? 0) + share);
    }
    return Array.from(map.entries())
      .map(([categoryId, value]) => {
        const c = store.categoriesById.get(categoryId);
        return { key: categoryId, label: c?.name ?? "—", color: c?.color ?? "slate", value };
      })
      .sort((a, b) => b.value - a.value)
      .slice(0, 8);
  }, [store.state.transactions, store.categoriesById, person]);

  const ownedCards = useMemo(() => {
    if (!person) return [];
    return store.state.creditCards.filter((c) => c.ownerId === person.id && !c.deletedAt);
  }, [store.state.creditCards, person]);

  const ownedFixed = useMemo(() => {
    if (!person) return [];
    return store.state.recurringEntries.filter((fx) => fx.personId === person.id);
  }, [store.state.recurringEntries, person]);

  const recentTx = useMemo(() => {
    if (!person) return [];
    return store.state.transactions
      .filter((tx) => personShare(tx, person.id) > 0)
      .sort((a, b) => b.date - a.date)
      .slice(0, 20);
  }, [store.state.transactions, person]);

  if (!person) {
    return (
      <div className="max-w-3xl space-y-3">
        <Link to="/app/modules/finance" className="inline-flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)">
          <ArrowLeft className="size-3" />
          {labels.back}
        </Link>
        <div className="rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-12 text-center">
          <p className="text-sm font-medium text-(--color-foreground)">{labels.notFound.title}</p>
          <p className="mt-1 max-w-md text-[12.5px] text-(--color-muted-foreground)">{labels.notFound.body}</p>
        </div>
      </div>
    );
  }

  const currentMonth = monthly!.entries[monthly!.entries.length - 1];
  const cardDebt = ownedCards.reduce((acc, c) => {
    const today = new Date();
    const start = new Date(today.getFullYear(), today.getMonth() - 1, c.closingDay).getTime();
    const end   = new Date(today.getFullYear(), today.getMonth(), c.closingDay - 1, 23, 59, 59, 999).getTime();
    let used = 0;
    for (const tx of store.state.transactions) {
      if (tx.paymentMethod !== "credit" || tx.accountId !== c.id) continue;
      if (tx.date >= start && tx.date <= end) used += tx.amount;
    }
    return acc + used;
  }, 0);

  return (
    <div className="max-w-7xl space-y-4">
      <Link
        to="/app/modules/finance"
        className="inline-flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
      >
        <ArrowLeft className="size-3" />
        {labels.back}
      </Link>

      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-(--color-border) pb-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="flex size-12 shrink-0 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) font-mono text-[15px] font-semibold text-(--color-foreground)">
            {person.initials}
          </span>
          <div className="min-w-0">
            <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-(--color-muted-foreground)">
              {labels.eyebrow}
            </p>
            <h1 className="font-display text-2xl font-semibold tracking-tight text-(--color-foreground)">
              {person.name}
            </h1>
            <p className="mt-0.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {person.role} {person.active ? "" : `· ${labels.inactive}`}
            </p>
          </div>
        </div>
      </header>

      <section className="grid grid-cols-12 gap-3">
        <Kpi label={labels.tiles.income}    value={formatBRL(currentMonth?.income ?? 0)}     icon={ArrowDownLeft}  tone="emerald" />
        <Kpi label={labels.tiles.expenses}  value={formatBRL(currentMonth?.expense ?? 0)}    icon={ArrowUpRight}   tone="rose" />
        <Kpi label={labels.tiles.balance}   value={formatBRL((currentMonth?.income ?? 0) - (currentMonth?.expense ?? 0))} icon={Wallet} tone={(currentMonth?.income ?? 0) >= (currentMonth?.expense ?? 0) ? "sky" : "rose"} />
        <Kpi label={labels.tiles.cardDebt}  value={formatBRL(cardDebt)}                       icon={CreditCardIcon} tone="violet" />
      </section>

      <section className="grid grid-cols-12 gap-3">
        <Panel title={labels.monthlyTrend.title} hint={labels.monthlyTrend.hint} className="col-span-12 lg:col-span-7">
          <div className="min-h-[180px]">
            <BarChart
              segments={monthly!.entries.map((e) => ({
                key: e.month,
                label: e.month.slice(5),
                color: e.income - e.expense >= 0 ? "emerald" : "rose",
                value: Math.max(0, e.income - e.expense),
              }))}
              height={180}
              format={(v) => formatBRLCompact(v)}
            />
          </div>
          <p className="mt-2 font-mono text-[10px] text-(--color-muted-foreground)">{labels.monthlyTrend.captionBalance}</p>
        </Panel>

        <Panel title={labels.byCategory.title} hint={labels.byCategory.hint} className="col-span-12 lg:col-span-5">
          <div className="min-h-[180px] overflow-y-auto pr-1">
            <HorizontalBars segments={byCategory} format={formatBRL} />
          </div>
        </Panel>
      </section>

      <Panel title={labels.monthlyTable.title} hint={labels.monthlyTable.hint}>
        <ul className="divide-y divide-(--color-border)">
          {[...monthly!.entries].reverse().map((entry) => (
            <li key={entry.month} className="flex items-center justify-between gap-3 px-1 py-2 text-[12.5px]">
              <span className="font-mono uppercase tracking-wide text-(--color-foreground)">{entry.month}</span>
              <span className="hidden sm:inline font-mono text-[11px] text-emerald-700 dark:text-emerald-300">
                {formatBRL(entry.income)}
              </span>
              <span className="hidden sm:inline font-mono text-[11px] text-rose-700 dark:text-rose-300">
                {formatBRL(entry.expense)}
              </span>
              <span className={cn(
                "font-mono",
                entry.income - entry.expense >= 0 ? "text-(--color-foreground)" : "text-rose-700 dark:text-rose-300",
              )}>
                {formatBRL(entry.income - entry.expense)}
              </span>
            </li>
          ))}
        </ul>
      </Panel>

      <Panel title={labels.cards.title} hint={labels.cards.hint}>
        {ownedCards.length === 0 ? (
          <p className="rounded-md border border-dashed border-(--color-border) py-4 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
            {labels.cards.empty}
          </p>
        ) : (
          <ul className="space-y-2">
            {ownedCards.map((c) => (
              <li key={c.id}>
                <CompactCardRow
                  card={c}
                  transactions={store.state.transactions}
                  owner={person}
                  onOpen={() => { /* clicking here could open the card detail modal globally — out of scope */ }}
                  onDelete={() => {
                    const txCount = store.state.transactions.filter(
                      (t) => t.paymentMethod === "credit" && t.accountId === c.id,
                    ).length;
                    const msg = txCount === 0
                      ? `Delete ${c.name}?`
                      : `Delete ${c.name}? ${txCount} transaction${txCount === 1 ? "" : "s"} will be kept under this card for history.`;
                    if (window.confirm(msg)) archiveCard.mutate(c.id);
                  }}
                />
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <Panel title={labels.fixed.title} hint={labels.fixed.hint}>
        {ownedFixed.length === 0 ? (
          <p className="rounded-md border border-dashed border-(--color-border) py-4 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
            {labels.fixed.empty}
          </p>
        ) : (
          <ul className="divide-y divide-(--color-border)">
            {ownedFixed.map((fx) => {
              const cat = store.categoriesById.get(fx.categoryId);
              return (
                <li key={fx.id} className="flex items-center gap-3 py-2">
                  <span
                    className={cn(
                      "flex size-7 shrink-0 items-center justify-center rounded-md",
                      `bg-${cat?.color ?? "slate"}-500/10`,
                      `text-${cat?.color ?? "slate"}-700 dark:text-${cat?.color ?? "slate"}-300`,
                    )}
                  >
                    <CategoryIcon name={cat?.icon ?? "tag"} className="size-3.5" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">{fx.description}</p>
                    <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                      {labels.fixed.dueDay} {fx.dueDay} · {fx.recurrence === "monthly" ? labels.fixed.monthly : labels.fixed.annual}
                    </p>
                  </div>
                  <span className="font-mono text-[12.5px] text-(--color-foreground)">{formatBRL(fx.amount)}</span>
                </li>
              );
            })}
          </ul>
        )}
      </Panel>

      <Panel title={labels.recent.title} hint={labels.recent.hint}>
        {recentTx.length === 0 ? (
          <p className="rounded-md border border-dashed border-(--color-border) py-4 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
            {labels.recent.empty}
          </p>
        ) : (
          <ul className="divide-y divide-(--color-border)">
            {recentTx.map((tx) => (
              <li key={tx.id} className="flex items-center gap-3 py-2 text-[12.5px]">
                <span className="w-16 shrink-0 font-mono text-[10px] uppercase tracking-wide text-(--color-muted-foreground)">
                  {formatShortDate(tx.date)}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="line-clamp-1 text-(--color-foreground)">{tx.description || "—"}</p>
                  <p className="font-mono text-[10px] text-(--color-muted-foreground)">
                    {store.categoriesById.get(tx.categoryId ?? "")?.name ?? "—"}
                    {isShared(tx) ? (
                      <span className="ml-1.5 inline-flex items-center gap-0.5 rounded-full border border-(--color-border) bg-(--color-muted) px-1 py-px text-[9px]">
                        <Users className="size-2.5" />
                        ÷{tx.splitAmong?.length ?? 2}
                      </span>
                    ) : null}
                  </p>
                </div>
                <OriginBadge source={tx.source} showLabel={false} size="xs" />
                <span className={cn(
                  "shrink-0 font-mono",
                  tx.type === "income" ? "text-emerald-700 dark:text-emerald-300" : "text-(--color-foreground)",
                )}>
                  {tx.type === "income" ? "+" : "−"}{formatBRL(personShare(tx, person.id))}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}

/* ── Helpers ──────────────────────────────────────────────────────────── */

function Kpi({
  label,
  value,
  icon: Icon,
  tone,
}: {
  label: string;
  value: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  tone: string;
}) {
  return (
    <article className="col-span-12 sm:col-span-6 lg:col-span-3 rounded-xl border border-(--color-border) bg-(--color-card) p-3.5">
      <header className="flex items-center justify-between">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">{label}</p>
        <span aria-hidden className={cn("flex size-6 items-center justify-center rounded-md", `bg-${tone}-500/10`, `text-${tone}-700 dark:text-${tone}-300`)}>
          <Icon className="size-3" />
        </span>
      </header>
      <p className="mt-2 font-display text-lg font-semibold tracking-tight text-(--color-foreground)">{value}</p>
    </article>
  );
}

function Panel({
  title,
  hint,
  className,
  children,
}: {
  title: string;
  hint?: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section className={cn("rounded-xl border border-(--color-border) bg-(--color-card) p-3.5", className)}>
      <header className="mb-3 space-y-0.5">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">{title}</p>
        {hint ? <p className="text-[11px] text-(--color-muted-foreground)">{hint}</p> : null}
      </header>
      {children}
    </section>
  );
}

function computeMonthlyBreakdown(
  txs: Transaction[],
  personId: string,
  monthsBack: number,
): { entries: Array<{ month: string; income: Cents; expense: Cents }> } {
  const today = new Date();
  const entries: Array<{ month: string; income: Cents; expense: Cents }> = [];
  for (let i = monthsBack - 1; i >= 0; i--) {
    const ref = new Date(today.getFullYear(), today.getMonth() - i, 1);
    const start = ref.getTime();
    const end = new Date(ref.getFullYear(), ref.getMonth() + 1, 0, 23, 59, 59, 999).getTime();
    const key = `${ref.getFullYear()}-${String(ref.getMonth() + 1).padStart(2, "0")}`;
    let income = 0;
    let expense = 0;
    for (const tx of txs) {
      if (tx.status !== "paid") continue;
      if (tx.type === "transfer") continue;
      if (tx.date < start || tx.date > end) continue;
      const share = personShare(tx, personId);
      if (share === 0) continue;
      if (tx.type === "income") income += share;
      else expense += share;
    }
    entries.push({ month: key, income, expense });
  }
  return { entries };
}
