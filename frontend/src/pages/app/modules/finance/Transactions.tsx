import { useMemo } from "react";
import { Filter as FilterIcon, Search, Trash2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import { ArrowRightLeft, Users } from "lucide-react";
import { formatBRL, formatShortDate, isShared } from "./format";
import { CategoryIcon } from "./CategoryIcon";
import { OriginBadge } from "./OriginBadge";
import type { FinanceStore } from "./store";
import type { PaymentMethod, Transaction } from "./types";
import {
  isBackendTransactionId,
  useDeleteTransaction,
} from "@/modules/finance/hooks/useTransactions";

type Props = {
  store: FinanceStore;
  /** Module-hoisted opener; clicking a row routes to this. */
  onEditTx: (tx: Transaction) => void;
};

/**
 * Transactions tab — compact table of every transaction.
 *
 * Columns: date · description · person · category · type · method · amount · status · source.
 * Filters: free-text search · month · type · person(s) · category(ies) · method(s).
 * Click a row to edit, click Add to create.
 */
export function Transactions({ store, onEditTx }: Props) {
  const t = useT();
  const labels = t.app.modules.finance.transactions;
  const { state, peopleById, categoriesById, removeTransaction } = store;
  const f = state.filters;
  const deleteMut = useDeleteTransaction();

  // Route by id format: backend rows go through the mutation hook (with
  // optimistic update + workspace-scoped invalidation); legacy / local
  // rows (transfers, plan children, pre-Phase-3 seed) stay on the store.
  const handleDelete = (txId: string) => {
    if (isBackendTransactionId(txId)) {
      deleteMut.mutate(txId);
    } else {
      removeTransaction(txId);
    }
  };

  const filtered = useMemo(() => {
    const q = f.query.trim().toLowerCase();
    const personSet = new Set(f.personIds);
    const categorySet = new Set(f.categoryIds);
    const methodSet = new Set(f.paymentMethods);
    return state.transactions
      .filter((tx) => {
        if (f.month && monthKey(tx.date) !== f.month) return false;
        if (f.type !== "all" && tx.type !== f.type) return false;
        if (personSet.size > 0 && !personSet.has(tx.personId)) return false;
        if (categorySet.size > 0 && !categorySet.has(tx.categoryId ?? "")) return false;
        if (methodSet.size > 0 && !methodSet.has(tx.paymentMethod)) return false;
        if (q) {
          const cat = categoriesById.get(tx.categoryId ?? "")?.name?.toLowerCase() ?? "";
          const person = peopleById.get(tx.personId)?.name?.toLowerCase() ?? "";
          const hay = `${tx.description} ${tx.notes} ${cat} ${person}`.toLowerCase();
          if (!hay.includes(q)) return false;
        }
        return true;
      })
      .sort((a, b) => b.date - a.date);
  }, [state.transactions, f, peopleById, categoriesById]);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-hidden">
      <FilterRow store={store} />

      <div className="min-h-0 flex-1 overflow-y-auto rounded-xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
        {filtered.length === 0 ? (
          <EmptyRow />
        ) : (
          <table className="w-full text-[12.5px]">
            <thead className="sticky top-0 z-10 bg-(--color-card) shadow-[0_1px_0_0_var(--color-border)]">
              <tr className="text-left font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                <Th>{labels.cols.date}</Th>
                <Th className="min-w-[200px]">{labels.cols.description}</Th>
                <Th>{labels.cols.person}</Th>
                <Th>{labels.cols.category}</Th>
                <Th>{labels.cols.method}</Th>
                <Th className="text-right">{labels.cols.amount}</Th>
                <Th>{labels.cols.status}</Th>
                <Th>{labels.cols.source}</Th>
                <Th className="w-8" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((tx) => {
                const plan = tx.planId ? store.purchasePlansById.get(tx.planId) : undefined;
                const total = plan?.totalInstallments ?? tx.installment?.total;
                const current = tx.installmentNumber ?? tx.installment?.current;
                const installmentLabel = current && total ? `${current}/${total}` : null;
                const transferFrom = tx.fromAccountId
                  ? store.allAccountsById.get(tx.fromAccountId)?.name ?? "—"
                  : tx.fromAccount ?? "—";
                const transferTo = tx.toAccountId
                  ? store.allAccountsById.get(tx.toAccountId)?.name ?? "—"
                  : tx.toAccount ?? "—";
                return (
                  <Row
                    key={tx.id}
                    tx={tx}
                    personName={peopleById.get(tx.personId)?.name ?? "—"}
                    personInitials={peopleById.get(tx.personId)?.initials ?? "?"}
                    category={categoriesById.get(tx.categoryId ?? "")}
                    installmentLabel={installmentLabel}
                    transferFrom={transferFrom}
                    transferTo={transferTo}
                    onEdit={() => onEditTx(tx)}
                    onDelete={() => handleDelete(tx.id)}
                  />
                );
              })}
            </tbody>
          </table>
        )}
      </div>

    </div>
  );
}

function FilterRow({ store }: { store: FinanceStore }) {
  const t = useT();
  const labels = t.app.modules.finance.transactions.filters;
  const { state, setFilters, clearFilters } = store;
  const f = state.filters;

  const methodLabels = t.app.modules.finance.methodLabels;
  const methods: PaymentMethod[] = ["debit", "credit", "pix", "cash", "transfer"];

  const hasActive =
    f.query.trim() !== "" ||
    f.type !== "all" ||
    f.personIds.length > 0 ||
    f.categoryIds.length > 0 ||
    f.paymentMethods.length > 0;

  const togglePerson   = (id: string) => setFilters({ personIds:      toggle(f.personIds, id) });
  const toggleCategory = (id: string) => setFilters({ categoryIds:    toggle(f.categoryIds, id) });
  const toggleMethod   = (m: PaymentMethod) => setFilters({ paymentMethods: toggle(f.paymentMethods, m) });

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[200px] flex-1 sm:max-w-md">
          <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-(--color-muted-foreground)" />
          <input
            type="text"
            value={f.query}
            onChange={(e) => setFilters({ query: e.target.value })}
            placeholder={labels.queryPlaceholder}
            aria-label={labels.query}
            className="h-9 w-full rounded-lg border border-(--color-border) bg-(--color-card) pl-9 pr-3 text-sm text-(--color-foreground) placeholder:text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
          />
        </div>

        <select
          value={f.type}
          onChange={(e) => setFilters({ type: e.target.value as typeof f.type })}
          className="h-9 rounded-lg border border-(--color-border) bg-(--color-card) px-2 text-[12.5px] text-(--color-foreground) shadow-(--shadow-soft) outline-none"
        >
          <option value="all">{labels.types.all}</option>
          <option value="income">{labels.types.income}</option>
          <option value="expense">{labels.types.expense}</option>
          <option value="transfer">{labels.types.transfer}</option>
        </select>

        {hasActive ? (
          <button
            type="button"
            onClick={clearFilters}
            className="ml-auto inline-flex items-center gap-1 rounded-md border border-(--color-border) bg-(--color-card) px-2 py-1 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
          >
            <X className="size-3" /> {labels.clear}
          </button>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
          <FilterIcon className="size-3" /> {labels.facets}
        </span>

        {state.people.map((p) => {
          const active = f.personIds.includes(p.id);
          return (
            <button
              key={p.id}
              type="button"
              onClick={() => togglePerson(p.id)}
              className={chipClass(active)}
            >
              {p.name}
            </button>
          );
        })}

        {methods.map((m) => {
          const active = f.paymentMethods.includes(m);
          return (
            <button
              key={m}
              type="button"
              onClick={() => toggleMethod(m)}
              className={chipClass(active)}
            >
              {methodLabels[m]}
            </button>
          );
        })}

        {state.categories.map((c) => {
          const active = f.categoryIds.includes(c.id);
          return (
            <button
              key={c.id}
              type="button"
              onClick={() => toggleCategory(c.id)}
              className={chipClass(active)}
            >
              {c.name}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function Row({
  tx,
  personName,
  personInitials,
  category,
  installmentLabel,
  transferFrom,
  transferTo,
  onEdit,
  onDelete,
}: {
  tx: Transaction;
  personName: string;
  personInitials: string;
  category: ReturnType<FinanceStore["categoriesById"]["get"]>;
  installmentLabel: string | null;
  transferFrom: string;
  transferTo: string;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const methodLabels = t.app.modules.finance.methodLabels;
  const statusLabels = t.app.modules.finance.statusLabels;

  return (
    <tr
      onClick={onEdit}
      className="group cursor-pointer border-t border-(--color-border) text-[12.5px] hover:bg-(--color-muted)/40"
    >
      <Td className="font-mono text-[10.5px] text-(--color-muted-foreground)">{formatShortDate(tx.date)}</Td>
      <Td className="min-w-[200px]">
        <p className="line-clamp-1 text-(--color-foreground)">{tx.description || "—"}</p>
      </Td>
      <Td>
        <span className="inline-flex items-center gap-1.5">
          <span className="flex size-5 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) font-mono text-[9px] font-semibold text-(--color-foreground)">
            {personInitials}
          </span>
          <span className="truncate text-(--color-foreground)/85">{personName}</span>
          {isShared(tx) ? (
            <span
              className="inline-flex items-center gap-0.5 rounded-full border border-(--color-border) bg-(--color-muted) px-1 py-px font-mono text-[9px] uppercase tracking-wide text-(--color-muted-foreground)"
              title={`Split among ${tx.splitAmong?.length ?? 2} people`}
            >
              <Users className="size-2.5" />
              ÷{tx.splitAmong?.length ?? 2}
            </span>
          ) : null}
        </span>
      </Td>
      <Td>
        {tx.type === "transfer" ? (
          <span className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-muted) px-1.5 py-px font-mono text-[10px] uppercase tracking-wide text-(--color-foreground)">
            <ArrowRightLeft className="size-2.5" />
            {transferFrom} → {transferTo}
          </span>
        ) : category ? (
          <span className={cn("inline-flex items-center gap-1 rounded-full border px-1.5 py-px font-mono text-[10px] uppercase tracking-wide", `border-${category.color}-200 bg-${category.color}-50 text-${category.color}-700 dark:border-${category.color}-700 dark:bg-${category.color}-500/10 dark:text-${category.color}-300`)}>
            <CategoryIcon name={category.icon} className="size-2.5" />
            {category.name}
          </span>
        ) : "—"}
        {installmentLabel ? (
          <span
            className="ml-1.5 inline-flex items-center rounded-sm border border-(--color-border) bg-(--color-card) px-1 py-px font-mono text-[9px] uppercase tracking-wide text-(--color-muted-foreground)"
            title={`Installment ${installmentLabel}`}
          >
            {installmentLabel}
          </span>
        ) : null}
      </Td>
      <Td className="font-mono text-[10.5px] text-(--color-muted-foreground)">{methodLabels[tx.paymentMethod]}</Td>
      <Td className={cn(
        "text-right font-mono",
        tx.type === "income"   ? "text-emerald-700 dark:text-emerald-300" :
        tx.type === "transfer" ? "text-(--color-muted-foreground)" :
        "text-(--color-foreground)",
      )}>
        {tx.type === "income" ? "+" : tx.type === "transfer" ? "" : "−"}{formatBRL(tx.amount)}
      </Td>
      <Td>
        <StatusBadge status={tx.status} label={statusLabels[tx.status]} />
      </Td>
      <Td>
        <OriginBadge source={tx.source} />
      </Td>
      <Td className="w-8">
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
          aria-label={t.app.modules.finance.actions.delete}
          className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) opacity-0 transition-opacity hover:bg-rose-500/10 hover:text-rose-500 group-hover:opacity-100"
        >
          <Trash2 className="size-3" />
        </button>
      </Td>
    </tr>
  );
}

function StatusBadge({ status, label }: { status: Transaction["status"]; label: string }) {
  const tone =
    status === "paid"
      ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300"
      : status === "scheduled"
        ? "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-700 dark:bg-sky-500/10 dark:text-sky-300"
        : "border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-500/10 dark:text-amber-300";
  return (
    <span className={cn("inline-flex items-center rounded-full border px-1.5 py-px font-mono text-[9.5px] uppercase tracking-wide", tone)}>
      {label}
    </span>
  );
}

function EmptyRow() {
  const t = useT();
  return (
    <div className="flex min-h-[200px] items-center justify-center text-center">
      <div className="px-6 py-10">
        <p className="text-sm font-medium text-(--color-foreground)">
          {t.app.modules.finance.transactions.empty.title}
        </p>
        <p className="mt-1 text-[12.5px] text-(--color-muted-foreground)">
          {t.app.modules.finance.transactions.empty.body}
        </p>
      </div>
    </div>
  );
}

function Th({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <th className={cn("px-3 py-2 font-normal", className)}>{children}</th>;
}

function Td({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <td className={cn("px-3 py-1.5", className)}>{children}</td>;
}

function chipClass(active: boolean): string {
  return cn(
    "inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium transition-colors",
    active
      ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-brand-700) dark:text-(--color-brand-300)"
      : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:text-(--color-foreground)",
  );
}

function toggle<T>(arr: ReadonlyArray<T>, value: T): T[] {
  return arr.includes(value) ? arr.filter((x) => x !== value) : [...arr, value];
}

function monthKey(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}
