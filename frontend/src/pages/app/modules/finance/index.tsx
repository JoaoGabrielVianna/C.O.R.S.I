import { useMemo, useState } from "react";
import {
  ArrowLeftRight,
  BarChart3,
  CreditCard as CreditCardIcon,
  CalendarClock,
  Filter as FilterIcon,
  LineChart as LineChartIcon,
  Plus,
  ShoppingBag,
  Tag,
  Users,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import { CardsInvoices } from "./CardsInvoices";
import { Categories } from "./Categories";
import { RecurringEntries } from "./RecurringEntries";
import { Overview } from "./Overview";
import { People } from "./People";
import { Projection } from "./Projection";
import { PurchasePlansSection } from "./PurchasePlansSection";
import { Transactions } from "./Transactions";
import { TransactionModal } from "./TransactionModal";
import { useFinance } from "./store";
import type { FinanceTab, Transaction } from "./types";
import { currentMonthKey } from "./types";

/**
 * Finance — dense personal money command center (v0.0.0).
 *
 * Compact header: title + subtitle on the left · month selector + Filters
 * shortcut + Add transaction CTA on the right.
 * Tabs row below: Overview · Transactions · Cards · Categories · People ·
 * Fixed · Projection.
 *
 * The transaction modal is hoisted here so the Add CTA works from any tab.
 * The Filters shortcut routes the user to the Transactions tab where the
 * full filter set lives.
 */
export function FinancePage() {
  const t = useT();
  const store = useFinance();
  const [tab, setTab] = useState<FinanceTab>("overview");
  const [txModal, setTxModal] = useState<{ open: boolean; editing: Transaction | null }>({
    open: false,
    editing: null,
  });

  const labels = t.app.modules.finance;
  const overviewCtas = labels.overview.ctas;

  const monthOptions = useMemo(() => {
    const set = new Set<string>([currentMonthKey()]);
    for (const tx of store.state.transactions) set.add(monthKey(tx.date));
    return Array.from(set).sort((a, b) => b.localeCompare(a));
  }, [store.state.transactions]);

  return (
    <div className="flex flex-col gap-3 lg:h-[calc(100dvh-6.5rem)]">
      <header className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-(--color-border) pb-3">
        <div className="flex min-w-0 items-baseline gap-3">
          <h1 className="font-display text-xl font-semibold tracking-tight text-(--color-foreground)">
            {labels.title}
          </h1>
          <span className="text-[12.5px] text-(--color-muted-foreground)">
            {labels.overview.subtitle}
          </span>
          <span className="hidden rounded-full border border-amber-200 bg-amber-50 px-1.5 py-px font-mono text-[9.5px] uppercase tracking-[0.14em] text-amber-800 dark:border-amber-700 dark:bg-amber-500/10 dark:text-amber-300 sm:inline-flex">
            {labels.previewBadge}
          </span>
          {/*
            Records sitting in this browser that Postgres does not have.
            They are not counted in anything on this screen and they are not
            deleted — see `localOnlyTransactions` in store.ts. Saying so is
            the whole point: dropping them from the totals silently would
            trade one invisible problem for another.
          */}
          {store.localOnlyTransactions > 0 ? (
            <span
              title={labels.localOnlyHint}
              className="hidden rounded-full border border-(--color-border) bg-(--color-muted) px-1.5 py-px font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) sm:inline-flex"
            >
              {labels.localOnlyBadge.replace("{count}", String(store.localOnlyTransactions))}
            </span>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <select
            value={store.state.filters.month}
            onChange={(e) => store.setFilters({ month: e.target.value })}
            aria-label={overviewCtas.month}
            className="h-8 rounded-md border border-(--color-border) bg-(--color-card) px-2 text-[12px] text-(--color-foreground) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
          >
            {monthOptions.map((m) => (
              <option key={m} value={m}>{formatMonthOption(m)}</option>
            ))}
          </select>
          <button
            type="button"
            onClick={() => setTab("transactions")}
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-(--color-border) bg-(--color-card) px-2.5 text-[12px] font-medium text-(--color-foreground) hover:bg-(--color-muted)"
          >
            <FilterIcon className="size-3" />
            {overviewCtas.filters}
          </button>
          <button
            type="button"
            onClick={() => setTxModal({ open: true, editing: null })}
            className="inline-flex h-8 items-center gap-1.5 rounded-md bg-(--color-accent) px-2.5 text-[12px] font-medium text-(--color-accent-foreground) shadow-(--shadow-soft) hover:-translate-y-px hover:shadow-(--shadow-card)"
          >
            <Plus className="size-3.5" />
            <span>{overviewCtas.addTx}</span>
          </button>
        </div>
      </header>

      <TabsRow tab={tab} onChange={setTab} />

      <div className="min-h-0 flex-1">
        {tab === "overview"     ? <Overview      store={store} /> : null}
        {tab === "transactions" ? <Transactions  store={store} onEditTx={(tx) => setTxModal({ open: true, editing: tx })} /> : null}
        {tab === "cards"        ? <CardsInvoices store={store} /> : null}
        {tab === "categories"   ? <Categories    store={store} /> : null}
        {tab === "people"       ? <People        store={store} /> : null}
        {tab === "recurring"    ? <RecurringEntries store={store} /> : null}
        {tab === "plans"        ? <PurchasePlansSection store={store} /> : null}
        {tab === "projection"   ? <Projection    store={store} /> : null}
      </div>

      <TransactionModal
        open={txModal.open}
        editing={txModal.editing}
        store={store}
        onClose={() => setTxModal({ open: false, editing: null })}
      />
    </div>
  );
}

type TabDef = { id: FinanceTab; label: string; icon: React.ComponentType<React.SVGProps<SVGSVGElement>> };

function TabsRow({ tab, onChange }: { tab: FinanceTab; onChange: (next: FinanceTab) => void }) {
  const t = useT();
  const labels = t.app.modules.finance.tabs;

  const tabs: TabDef[] = [
    { id: "overview",     label: labels.overview,     icon: BarChart3 },
    { id: "transactions", label: labels.transactions, icon: ArrowLeftRight },
    { id: "cards",        label: labels.cards,        icon: CreditCardIcon },
    { id: "categories",   label: labels.categories,   icon: Tag },
    { id: "people",       label: labels.people,       icon: Users },
    { id: "recurring",        label: labels.recurring,    icon: CalendarClock },
    { id: "plans",        label: labels.plans,        icon: ShoppingBag },
    { id: "projection",   label: labels.projection,   icon: LineChartIcon },
  ];

  return (
    <div
      role="tablist"
      className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-(--color-border) pb-px"
    >
      {tabs.map((entry) => {
        const Icon = entry.icon;
        const active = entry.id === tab;
        return (
          <button
            key={entry.id}
            role="tab"
            aria-selected={active}
            type="button"
            onClick={() => onChange(entry.id)}
            className={cn(
              "relative inline-flex shrink-0 items-center gap-1.5 rounded-t-md px-3 py-1.5 text-[12.5px] font-medium transition-colors",
              active
                ? "text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            <Icon
              className={cn(
                "size-3.5",
                active ? "text-(--color-brand-600) dark:text-(--color-brand-400)" : "",
              )}
            />
            {entry.label}
            {active ? (
              <span
                aria-hidden
                className="absolute inset-x-2 bottom-[-1px] h-[2px] rounded-full bg-(--color-brand-500)"
              />
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

function monthKey(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

function formatMonthOption(key: string): string {
  const [y, m] = key.split("-");
  return `${m}/${y.slice(2)}`;
}
