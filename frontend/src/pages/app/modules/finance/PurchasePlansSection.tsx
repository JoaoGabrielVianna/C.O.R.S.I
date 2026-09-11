import { useMemo, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { Plus, ShoppingBag, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/Button";
import { formatBRL, formatShortDate } from "./format";
import type { FinanceStore } from "./store";
import { PurchasePlanModal } from "./PurchasePlanModal";
import { UpcomingInstallmentsCard } from "./UpcomingInstallmentsCard";
import {
  useCancelPurchasePlan,
  usePurchasePlans,
} from "@/modules/finance/hooks/usePurchasePlans";
import type { PurchasePlan, Transaction } from "./types";
import { useFormat, useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = { store: FinanceStore };

type PlanRow = {
  plan: PurchasePlan;
  paid: number;
  remaining: number;
  total: number;
  paidAmount: number;
  remainingAmount: number;
  nextInstallment?: Transaction;
  highestPaidInstallment: number;
};

/**
 * PurchasePlansSection — list of active multi-installment plans plus a
 * panel of the next upcoming installment rows.
 *
 * Renders inside Finance as its own tab. Cancelling a plan opens a
 * confirmation that preserves the paid history and removes future
 * scheduled installments (the backend executes both — see
 * `POST /finance/purchase-plans/{id}/cancel`).
 */
export function PurchasePlansSection({ store }: Props) {
  const t = useT();
  const plansQuery = usePurchasePlans();
  const cancelMut = useCancelPurchasePlan();
  const [modalOpen, setModalOpen] = useState(false);
  const [confirm, setConfirm] = useState<{ planId: string; row: PlanRow } | null>(null);

  const rows = useMemo<PlanRow[]>(() => {
    const plans = plansQuery.data ?? [];
    return plans.map((plan) => buildRow(plan, store.state.transactions)).sort(sortRows);
  }, [plansQuery.data, store.state.transactions]);

  const active = rows.filter((r) => r.plan.status === "active");
  const past = rows.filter((r) => r.plan.status !== "active");

  return (
    <div className="flex flex-col gap-3 overflow-y-auto pb-2">
      <header className="flex shrink-0 items-center justify-between gap-3 rounded-xl border border-(--color-border) bg-(--color-card) px-3.5 py-2.5">
        <div className="min-w-0">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">
            {t.app.modules.finance.plans.title}
          </p>
          <p className="mt-0.5 text-[11.5px] text-(--color-muted-foreground)">
            {t.app.modules.finance.plans.plansSummary
              .replace("{active}", String(active.length))
              .replace("{past}", String(past.length))}
          </p>
        </div>
        <Button
          type="button"
          onClick={() => setModalOpen(true)}
          className="h-8 px-2.5 text-[12px]"
        >
          <Plus className="size-3.5" />
          <span>{t.app.modules.finance.plans.new}</span>
        </Button>
      </header>

      <div className="grid grid-cols-12 gap-3">
        <PlansListPanel
          title={t.app.modules.finance.plans.active}
          rows={active}
          store={store}
          onCancel={(row) => setConfirm({ planId: row.plan.id, row })}
          loading={plansQuery.isLoading}
          className="col-span-12 lg:col-span-8"
          emptyHint="No active purchase plans. Open one for a multi-installment credit purchase."
        />
        <UpcomingInstallmentsCard
          store={store}
          className="col-span-12 lg:col-span-4"
        />
      </div>

      {past.length > 0 ? (
        <PlansListPanel
          title={t.app.modules.finance.plans.history}
          rows={past}
          store={store}
          loading={false}
          className=""
          emptyHint=""
          historical
        />
      ) : null}

      <PurchasePlanModal
        open={modalOpen}
        store={store}
        onClose={() => setModalOpen(false)}
      />

      <CancelConfirm
        open={confirm != null}
        row={confirm?.row}
        pending={cancelMut.isPending}
        onConfirm={() => {
          if (!confirm) return;
          // Backend cancel takes no body; it soft-deletes every still-
          // scheduled installment of the plan and preserves paid ones.
          cancelMut.mutate(
            { id: confirm.planId },
            { onSuccess: () => setConfirm(null) },
          );
        }}
        onClose={() => setConfirm(null)}
      />
    </div>
  );
}

/* ── List panel ─────────────────────────────────────────────────────── */

function PlansListPanel({
  title,
  rows,
  store,
  onCancel,
  loading,
  className,
  emptyHint,
  historical = false,
}: {
  title: string;
  rows: PlanRow[];
  store: FinanceStore;
  onCancel?: (row: PlanRow) => void;
  loading: boolean;
  className?: string;
  emptyHint: string;
  historical?: boolean;
}) {
  const t = useT();
  return (
    <section
      className={cn(
        "rounded-xl border border-(--color-border) bg-(--color-card) p-3.5",
        className,
      )}
    >
      <header className="mb-3 flex items-start justify-between gap-2">
        <div>
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">
            {title}
          </p>
        </div>
        <span
          aria-hidden
          className="inline-flex size-7 items-center justify-center rounded-md border border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)"
        >
          <ShoppingBag className="size-3.5" />
        </span>
      </header>

      {loading ? (
        <p className="rounded-lg border border-dashed border-(--color-border) px-3 py-6 text-center text-[12px] text-(--color-muted-foreground)">
          {t.app.modules.finance.plans.loading}
        </p>
      ) : rows.length === 0 ? (
        <p className="rounded-lg border border-dashed border-(--color-border) px-3 py-6 text-center text-[12px] text-(--color-muted-foreground)">
          {emptyHint}
        </p>
      ) : (
        <ul className="divide-y divide-(--color-border)">
          {rows.map((row) => (
            <PlanRowItem
              key={row.plan.id}
              row={row}
              store={store}
              onCancel={onCancel}
              historical={historical}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function PlanRowItem({
  row,
  store,
  onCancel,
  historical,
}: {
  row: PlanRow;
  store: FinanceStore;
  onCancel?: (row: PlanRow) => void;
  historical: boolean;
}) {
  const t = useT();
  const { plan, paid, total, paidAmount, remainingAmount, nextInstallment } = row;
  const card = store.cardsById.get(plan.accountId);
  const cat = store.categoriesById.get(plan.categoryId);
  const person = plan.personId ? store.peopleById.get(plan.personId) : null;
  const progress = total > 0 ? Math.min(100, Math.round((paid / total) * 100)) : 0;

  return (
    <li className="grid gap-2 py-3 sm:grid-cols-[1fr_auto] sm:items-center">
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <p className="truncate text-[13px] font-medium text-(--color-foreground)">
            {plan.description}
          </p>
          <span className="font-mono text-[10.5px] text-(--color-muted-foreground)">
            {paid}/{total} paid
          </span>
          {plan.status === "cancelled" ? (
            <span className="rounded-full border border-amber-200 bg-amber-50 px-1.5 py-px font-mono text-[9.5px] uppercase tracking-[0.14em] text-amber-800 dark:border-amber-700 dark:bg-amber-500/10 dark:text-amber-300">
              {t.app.modules.finance.plans.cancelled}
            </span>
          ) : plan.status === "completed" ? (
            <span className="rounded-full border border-emerald-200 bg-emerald-50 px-1.5 py-px font-mono text-[9.5px] uppercase tracking-[0.14em] text-emerald-800 dark:border-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300">
              {t.app.modules.finance.plans.completed}
            </span>
          ) : null}
        </div>
        <p className="mt-0.5 truncate font-mono text-[10.5px] text-(--color-muted-foreground)">
          {formatBRL(plan.originalAmount)} · {formatBRL(plan.perInstallment)}/mo
          {card ? <> · {card.name}</> : null}
          {cat ? <> · {cat.name}</> : null}
          {person ? <> · {person.name}</> : null}
        </p>
        <div className="mt-2 h-1 w-full overflow-hidden rounded-full bg-(--color-muted)">
          <div
            aria-hidden
            className="h-full rounded-full bg-(--color-brand-500) transition-[width] duration-300"
            style={{ width: `${progress}%` }}
          />
        </div>
        <p className="mt-1 font-mono text-[10px] text-(--color-muted-foreground)">
          {formatBRL(paidAmount)} paid
          {remainingAmount > 0 ? <> · {formatBRL(remainingAmount)} remaining</> : null}
          {nextInstallment ? <> · next {formatShortDate(nextInstallment.date)}</> : null}
        </p>
      </div>

      {!historical && onCancel && plan.status === "active" ? (
        <div className="flex shrink-0 items-center justify-end">
          <button
            type="button"
            onClick={() => onCancel(row)}
            className="inline-flex h-7 items-center gap-1 rounded-md border border-(--color-border) bg-(--color-card) px-2 text-[11.5px] font-medium text-(--color-muted-foreground) hover:text-rose-600"
          >
            {t.app.modules.finance.plans.cancelRemaining}
          </button>
        </div>
      ) : null}
    </li>
  );
}

/* ── Cancel confirm ─────────────────────────────────────────────────── */

function CancelConfirm({
  open,
  row,
  pending,
  onConfirm,
  onClose,
}: {
  open: boolean;
  row?: PlanRow;
  pending: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const fmt = useFormat();
  return (
    <AnimatePresence>
      {open && row ? (
        <div className="fixed inset-0 z-[110] flex items-center justify-center px-4">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.12 }}
            onClick={onClose}
            className="absolute inset-0 bg-black/55 backdrop-blur-sm"
            aria-hidden
          />
          <motion.div
            role="alertdialog"
            aria-modal="true"
            aria-label={t.app.modules.finance.plans.cancelRemainingLabel}
            initial={{ opacity: 0, y: -8, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -6, scale: 0.98 }}
            transition={{ duration: 0.16, ease }}
            className="relative w-full max-w-md overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
          >
            <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-5 py-3">
              <div>
                <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                  {t.app.common.shared.confirm}
                </p>
                <h2 className="mt-0.5 font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
                  {t.app.modules.finance.plans.confirmCancelTitle}
                </h2>
              </div>
              <button
                type="button"
                onClick={onClose}
                aria-label={t.app.modules.finance.plans.close}
                className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
              >
                <X className="size-3.5" />
              </button>
            </header>

            <div className="space-y-3 px-5 py-4 text-[12.5px]">
              <p className="text-(--color-foreground)">
                <span className="font-medium">{row.plan.description}</span> ·{" "}
                {t.app.modules.finance.plans.paidOf
                  .replace("{paid}", String(row.paid))
                  .replace("{total}", String(row.total))}
              </p>
              <ul className="space-y-1 rounded-lg border border-(--color-border) bg-(--color-muted) px-3 py-2 text-[12px] text-(--color-muted-foreground)">
                <li>
                  {fmt
                    .plural(row.remaining, t.app.modules.finance.plans.willRemove)
                    .replace("{count}", String(row.remaining))
                    .replace("{amount}", formatBRL(row.remainingAmount))}
                </li>
                <li>
                  {t.app.modules.finance.plans.historyPreserved
                    .replace("{count}", String(row.paid))
                    .replace("{amount}", formatBRL(row.paidAmount))}
                </li>
                <li>{t.app.modules.finance.plans.confirmCancelTail}</li>
              </ul>
            </div>

            <div className="flex items-center justify-end gap-2 border-t border-(--color-border) px-5 py-3">
              <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>
                {t.app.modules.finance.plans.keepPlan}
              </Button>
              <Button
                type="button"
                onClick={onConfirm}
                disabled={pending}
                className="bg-rose-600 text-white hover:bg-rose-500"
              >
                {pending ? t.app.modules.finance.plans.cancelling : t.app.modules.finance.plans.cancelRemaining}
              </Button>
            </div>
          </motion.div>
        </div>
      ) : null}
    </AnimatePresence>
  );
}

/* ── helpers ────────────────────────────────────────────────────────── */

function buildRow(plan: PurchasePlan, transactions: Transaction[]): PlanRow {
  let paid = 0;
  let paidAmount = 0;
  let remaining = 0;
  let remainingAmount = 0;
  let highestPaidInstallment = 0;
  let nextInstallment: Transaction | undefined;
  let nextDate = Infinity;
  // Backend plan response is intentionally minimal; the display facts
  // (card, category, purchase date) live on the child installments and
  // get hoisted onto the plan view here.
  let earliestDate = Infinity;
  let derivedAccountId = plan.accountId;
  let derivedCategoryId = plan.categoryId;
  for (const tx of transactions) {
    if (tx.planId !== plan.id) continue;
    if (!derivedAccountId && tx.accountId) derivedAccountId = tx.accountId;
    if (!derivedCategoryId && tx.categoryId) derivedCategoryId = tx.categoryId;
    if (tx.date < earliestDate) earliestDate = tx.date;
    if (tx.status === "paid") {
      paid += 1;
      paidAmount += tx.amount;
      if ((tx.installmentNumber ?? 0) > highestPaidInstallment) {
        highestPaidInstallment = tx.installmentNumber ?? 0;
      }
    } else {
      remaining += 1;
      remainingAmount += tx.amount;
      if (tx.date < nextDate) {
        nextDate = tx.date;
        nextInstallment = tx;
      }
    }
  }
  // Status is not on the backend plan record. Derive: all paid → completed;
  // remaining child rows gone but some still unpaid → cancelled (BE soft-
  // deleted the scheduled installments); otherwise → active.
  const total = plan.totalInstallments;
  let status: PurchasePlan["status"] = "active";
  if (paid >= total && total > 0) status = "completed";
  else if (remaining === 0 && paid < total) status = "cancelled";
  const enrichedPlan: PurchasePlan = {
    ...plan,
    accountId: derivedAccountId,
    categoryId: derivedCategoryId,
    purchasedAt: earliestDate === Infinity ? plan.purchasedAt : earliestDate,
    status,
  };
  return {
    plan: enrichedPlan,
    paid,
    remaining,
    total,
    paidAmount,
    remainingAmount,
    nextInstallment,
    highestPaidInstallment,
  };
}

function sortRows(a: PlanRow, b: PlanRow): number {
  // Active first, then by next installment date (or createdAt fallback).
  if (a.plan.status !== b.plan.status) {
    if (a.plan.status === "active") return -1;
    if (b.plan.status === "active") return 1;
  }
  const aDate = a.nextInstallment?.date ?? a.plan.createdAt;
  const bDate = b.nextInstallment?.date ?? b.plan.createdAt;
  return aDate - bDate;
}
