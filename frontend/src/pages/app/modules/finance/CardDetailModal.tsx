import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ChevronLeft, ChevronRight, Pencil, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import { CardPreview } from "./CardPreview";
import { formatBRL, formatShortDate } from "./format";
import type { FinanceStore } from "./store";
import type { Cents, CreditCard, Transaction } from "./types";
import { useCancelPurchasePlan } from "@/modules/finance/hooks/usePurchasePlans";
import { activeFormat } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = {
  open: boolean;
  card: CreditCard | null;
  store: FinanceStore;
  onClose: () => void;
  onEdit: (card: CreditCard) => void;
};

/**
 * CardDetailModal — rich card-scoped drill-in.
 *
 *   ┌─ Header ─────────────────────────────────────────┐
 *   │  [CardPreview · sm]   Nubank Black · ••••4271    │
 *   │                       Limite · Vencimento · …    │
 *   ├─ Limit summary ───────────────────────────────────┤
 *   │  Used   Available   Limit   Util%                 │
 *   ├─ Cycle switcher ──────────────────────────────────┤
 *   │  [maio 26] [abr 26] [mar 26]                      │
 *   ├─ Transactions for active cycle ───────────────────┤
 *   ├─ Installments · honest empty state                │
 *   ├─ Projection   · honest empty state                │
 *   └─ History · last 3 cycle totals                    ┘
 */
export function CardDetailModal({ open, card, store, onClose, onEdit }: Props) {
  return (
    <AnimatePresence>
      {open && card ? (
        <CardDetailBody
          key={card.id}
          card={card}
          store={store}
          onClose={onClose}
          onEdit={onEdit}
        />
      ) : null}
    </AnimatePresence>
  );
}

function CardDetailBody({
  card,
  store,
  onClose,
  onEdit,
}: {
  card: CreditCard;
  store: FinanceStore;
  onClose: () => void;
  onEdit: (card: CreditCard) => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.cards;

  const cycles = useMemo(() => buildCycles(card, store.state.transactions, 6), [card, store.state.transactions]);
  const [cycleIdx, setCycleIdx] = useState(0);
  const [expandedPlanId, setExpandedPlanId] = useState<string | null>(null);
  const cycle = cycles[cycleIdx] ?? cycles[0];

  const txs = useMemo(() => {
    if (!cycle) return [];
    return store.state.transactions
      .filter((tx) => tx.paymentMethod === "credit" && tx.accountId === card.id)
      .filter((tx) => tx.date >= cycle.cycleStart && tx.date <= cycle.cycleEnd)
      .sort((a, b) => b.date - a.date);
  }, [cycle, card.id, store.state.transactions]);

  // Limit consumption = sum of credit transactions on this card that the
  // issuer still has locked against the limit. A row counts until its
  // invoice is settled (`status === "paid"`); scheduled future installments
  // and the open-cycle charges all consume limit immediately. This is the
  // operational truth for Brazilian credit cards.
  const used = useMemo(() => {
    let total: Cents = 0;
    for (const tx of store.state.transactions) {
      if (tx.paymentMethod !== "credit" || tx.accountId !== card.id) continue;
      if (tx.status === "paid") continue;
      total += tx.amount;
    }
    return total;
  }, [card.id, store.state.transactions]);
  const available = Math.max(0, card.limit - used);
  const usedPct = card.limit > 0 ? Math.min(100, Math.round((used / card.limit) * 100)) : 0;

  // ── Operational stats: next due, min payment, subscriptions, installments, avg invoice
  /**
   * Active installment plans on this card — read directly from
   * `state.purchasePlans`. Each row carries:
   *   · `progress` = highest installment marked `paid` (0 if none yet)
   *   · `remaining` = sum of unpaid child amounts (= what the bank still has
   *      locked against the limit for this plan)
   */
  const activeInstallments = useMemo(() => {
    const txsByPlan = new Map<string, Transaction[]>();
    for (const tx of store.state.transactions) {
      if (!tx.planId) continue;
      const arr = txsByPlan.get(tx.planId) ?? [];
      arr.push(tx);
      txsByPlan.set(tx.planId, arr);
    }
    return store.state.purchasePlans
      .filter((p) => p.accountId === card.id && p.status === "active")
      .map((p) => {
        const txs = txsByPlan.get(p.id) ?? [];
        let progress = 0;
        let remaining = 0;
        for (const tx of txs) {
          if (tx.status === "paid" && (tx.installmentNumber ?? 0) > progress) {
            progress = tx.installmentNumber ?? 0;
          }
          if (tx.status !== "paid") remaining += tx.amount;
        }
        return {
          planId: p.id,
          description: p.description,
          total: p.totalInstallments,
          current: progress,
          perInstallment: p.perInstallment,
          remaining,
        };
      })
      .filter((p) => p.current < p.total)
      .sort((a, b) => (b.total - b.current) - (a.total - a.current));
  }, [card.id, store.state.transactions, store.state.purchasePlans]);

  const operational = useMemo(() => {
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const todayMs = today.getTime();
    const thisDue = new Date(today.getFullYear(), today.getMonth(), card.dueDay).getTime();
    const nextDueMs = thisDue >= todayMs
      ? thisDue
      : new Date(today.getFullYear(), today.getMonth() + 1, card.dueDay).getTime();
    const daysUntilDue = Math.round((nextDueMs - todayMs) / 86_400_000);

    // Minimum payment heuristic: 15% of the most recent CLOSED invoice
    const closedCycle = cycles.find((c) => c.cycleEnd < todayMs);
    const minPayment = Math.round((closedCycle?.total ?? cycles[0]?.total ?? 0) * 0.15);

    // Subscriptions: distinct merchants on this card that recur across ≥2 cycles
    const merchantCycles = new Map<string, Set<number>>();
    for (const tx of store.state.transactions) {
      if (tx.paymentMethod !== "credit" || tx.accountId !== card.id) continue;
      const merchant = tx.description.trim().toLowerCase();
      if (!merchant) continue;
      const idx = cycles.findIndex((cy) => tx.date >= cy.cycleStart && tx.date <= cy.cycleEnd);
      if (idx === -1) continue;
      const set = merchantCycles.get(merchant) ?? new Set<number>();
      set.add(idx);
      merchantCycles.set(merchant, set);
    }
    let subscriptions = 0;
    for (const set of merchantCycles.values()) if (set.size >= 2) subscriptions += 1;

    // Active installment plans on this card: count `PurchasePlan` rows whose
    // status is still active (children may already be partially paid; the
    // plan isn't complete until all children are paid or it's cancelled).
    let installments = 0;
    for (const p of store.state.purchasePlans) {
      if (p.accountId !== card.id) continue;
      if (p.status === "active") installments += 1;
    }

    // Average invoice across the last 3 CLOSED cycles (excludes current cycle
    // because it's still accumulating). Falls back to all available closed
    // cycles when there are fewer than 3.
    const closedCycles = cycles.filter((c) => c.cycleEnd < todayMs).slice(0, 3);
    const avgInvoice = closedCycles.length > 0
      ? Math.round(closedCycles.reduce((sum, c) => sum + c.total, 0) / closedCycles.length)
      : null;

    return { nextDueMs, daysUntilDue, minPayment, subscriptions, installments, avgInvoice, avgWindow: closedCycles.length };
  }, [card, cycles, store.state.transactions, store.state.purchasePlans]);

  const owner = store.peopleById.get(card.ownerId);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey as unknown as EventListener);
    return () => window.removeEventListener("keydown", onKey as unknown as EventListener);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center px-4 pt-[7vh]">
      <motion.div
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        transition={{ duration: 0.15 }}
        onClick={onClose}
        className="absolute inset-0 bg-black/45 backdrop-blur-sm"
        aria-hidden
      />
      <motion.div
        role="dialog"
        aria-modal="true"
        initial={{ opacity: 0, y: -12, scale: 0.985 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: -8, scale: 0.985 }}
        transition={{ duration: 0.2, ease }}
        className="relative flex max-h-[86vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
      >
        {/* Header */}
        <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-5 py-4">
          <div className="flex min-w-0 items-center gap-4">
            <div className="hidden w-44 shrink-0 sm:block">
              <CardPreview card={card} used={used} invoice={cycle?.total ?? 0} last4={card.last4} size="sm" />
            </div>
            <div className="min-w-0">
              <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                {labels.detail.eyebrow}
              </p>
              <h2 className="mt-0.5 truncate font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
                {card.name}
              </h2>
              <p className="mt-0.5 inline-flex items-center gap-1.5 font-mono text-[10.5px] text-(--color-muted-foreground)">
                {owner ? (
                  <>
                    <span className="flex size-4 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) text-[8.5px] font-semibold text-(--color-foreground)">
                      {owner.initials}
                    </span>
                    <span className="normal-case tracking-normal text-(--color-foreground)/85">{owner.name}</span>
                    <span aria-hidden className="text-(--color-muted-foreground)/60">·</span>
                  </>
                ) : null}
                {labels.closing} {card.closingDay} · {labels.due} {card.dueDay}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            {card.deletedAt ? null : (
              <button
                type="button"
                onClick={() => onEdit(card)}
                className="inline-flex h-7 items-center gap-1.5 rounded-md border border-(--color-border) bg-(--color-card) px-2 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
              >
                <Pencil className="size-3" />
                {t.app.modules.finance.common.edit}
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              aria-label={t.app.modules.finance.cardDetail.closeLabel}
              className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)"
            >
              <X className="size-3.5" />
            </button>
          </div>
        </header>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-5">
          {card.deletedAt ? (
            <div className="rounded-lg border border-dashed border-(--color-border) bg-(--color-muted)/40 px-3 py-2 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              Card deleted on {activeFormat().date(card.deletedAt, "long")} · history preserved
            </div>
          ) : null}

          {/* Limit summary */}
          <section className="space-y-2">
            <Header label={labels.detail.limit.title} />
            <div className="grid grid-cols-12 gap-2">
              <Stat className="col-span-6 sm:col-span-3" label={labels.detail.limit.used}      value={formatBRL(used)} />
              <Stat className="col-span-6 sm:col-span-3" label={labels.detail.limit.available} value={formatBRL(available)} />
              <Stat className="col-span-6 sm:col-span-3" label={labels.detail.limit.total}     value={formatBRL(card.limit)} />
              <Stat className="col-span-6 sm:col-span-3" label={labels.detail.limit.utilPct}   value={`${usedPct}%`} />
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-(--color-muted)">
              <div className="h-full rounded-full bg-(--color-brand-500)" style={{ width: `${usedPct}%` }} />
            </div>
          </section>

          {/* Operational glance */}
          <section className="space-y-2">
            <Header label={labels.detail.glance.title} />
            <div className="grid grid-cols-12 gap-2">
              <Stat
                className="col-span-6 sm:col-span-3"
                label={labels.detail.glance.nextDue}
                value={formatNextDue(operational.nextDueMs)}
                sub={
                  operational.daysUntilDue <= 0
                    ? labels.detail.glance.overdueIn.replace("{{n}}", String(Math.abs(operational.daysUntilDue)))
                    : labels.detail.glance.dueIn.replace("{{n}}", String(operational.daysUntilDue))
                }
                tone={
                  operational.daysUntilDue <= 3
                    ? "rose"
                    : operational.daysUntilDue <= 7
                      ? "amber"
                      : undefined
                }
              />
              <Stat
                className="col-span-6 sm:col-span-3"
                label={labels.detail.glance.avgInvoice}
                value={operational.avgInvoice === null ? "—" : formatBRL(operational.avgInvoice)}
                sub={
                  operational.avgWindow > 0
                    ? labels.detail.glance.avgInvoiceHint.replace("{{n}}", String(operational.avgWindow))
                    : labels.detail.glance.avgInvoiceEmpty
                }
              />
              <Stat
                className="col-span-6 sm:col-span-3"
                label={labels.detail.glance.minPayment}
                value={formatBRL(operational.minPayment)}
                sub={labels.detail.glance.minPaymentHint}
              />
              <Stat
                className="col-span-6 sm:col-span-3"
                label={labels.detail.glance.subscriptions}
                value={String(operational.subscriptions)}
                sub={labels.detail.glance.subscriptionsHint}
              />
              <Stat
                className="col-span-6 sm:col-span-3"
                label={labels.detail.glance.installments}
                value={String(operational.installments)}
                sub={labels.detail.glance.installmentsHint}
              />
            </div>
          </section>

          {/* Cycle switcher */}
          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <Header label={labels.detail.invoice.title} />
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  aria-label={t.app.modules.finance.cardDetail.prev}
                  disabled={cycleIdx >= cycles.length - 1}
                  onClick={() => setCycleIdx((i) => Math.min(cycles.length - 1, i + 1))}
                  className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) disabled:opacity-40 disabled:pointer-events-none"
                >
                  <ChevronLeft className="size-3" />
                </button>
                <button
                  type="button"
                  aria-label={t.app.modules.finance.cardDetail.next}
                  disabled={cycleIdx <= 0}
                  onClick={() => setCycleIdx((i) => Math.max(0, i - 1))}
                  className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) disabled:opacity-40 disabled:pointer-events-none"
                >
                  <ChevronRight className="size-3" />
                </button>
              </div>
            </div>
            <div className="flex flex-wrap gap-1">
              {cycles.slice(0, 4).map((c, i) => (
                <button
                  key={c.label}
                  type="button"
                  onClick={() => setCycleIdx(i)}
                  className={cn(
                    "rounded-md border px-2 py-0.5 text-[11px] font-medium transition-colors",
                    i === cycleIdx
                      ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
                      : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:text-(--color-foreground)",
                  )}
                >
                  {c.label}
                </button>
              ))}
            </div>
            <div className="flex items-baseline justify-between gap-2 rounded-md border border-(--color-border) bg-(--color-muted)/30 px-3 py-2">
              <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                {labels.detail.invoice.total}
              </span>
              <span className="font-mono text-[13px] font-semibold text-(--color-foreground)">
                {formatBRL(cycle?.total ?? 0)}
              </span>
            </div>

            {/* Transactions for active cycle */}
            {txs.length === 0 ? (
              <p className="rounded-md border border-dashed border-(--color-border) py-4 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
                {labels.detail.invoice.empty}
              </p>
            ) : (
              <ul className="divide-y divide-(--color-border) rounded-md border border-(--color-border)">
                {txs.map((tx) => (
                  <li key={tx.id} className="flex items-baseline justify-between gap-3 px-3 py-2">
                    <div className="min-w-0">
                      <p className="line-clamp-1 text-[12.5px] text-(--color-foreground)">{tx.description || "—"}</p>
                      <p className="font-mono text-[10px] text-(--color-muted-foreground)">
                        {formatShortDate(tx.date)} · {store.peopleById.get(tx.personId)?.name ?? "—"}
                      </p>
                    </div>
                    <span className="font-mono text-[12px] text-(--color-foreground)">{formatBRL(tx.amount)}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          {/* History */}
          {cycles.length > 1 ? (
            <section className="space-y-2">
              <Header label={labels.detail.history.title} />
              <ul className="space-y-1">
                {cycles.slice(0, 5).map((c, i) => (
                  <li key={c.label} className="flex items-baseline justify-between gap-2 rounded-md border border-(--color-border) px-3 py-1.5">
                    <span className="inline-flex items-center gap-2 text-[12px] text-(--color-foreground)">
                      <span aria-hidden className={cn("size-1.5 rounded-full", i === 0 ? "bg-(--color-brand-500)" : "bg-(--color-muted-foreground)/40")} />
                      {c.label}
                    </span>
                    <span className="font-mono text-[11px] text-(--color-foreground)">{formatBRL(c.total)}</span>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}

          {/* Active installments */}
          <section className="space-y-2">
            <Header label={labels.detail.installmentsList.title} hint={labels.detail.installmentsList.hint} />
            {activeInstallments.length === 0 ? (
              <p className="rounded-md border border-dashed border-(--color-border) py-4 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
                {labels.detail.installmentsList.empty}
              </p>
            ) : (
              <ul className="divide-y divide-(--color-border) rounded-md border border-(--color-border)">
                {activeInstallments.map((p) => (
                  <li key={p.planId}>
                    <button
                      type="button"
                      onClick={() => setExpandedPlanId((cur) => (cur === p.planId ? null : p.planId))}
                      className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-(--color-muted)/40"
                    >
                      <div className="min-w-0 flex-1">
                        <p className="line-clamp-1 text-[12.5px] text-(--color-foreground)">{p.description}</p>
                        <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                          {labels.detail.installmentsList.progress
                            .replace("{{current}}", String(p.current))
                            .replace("{{total}}", String(p.total))}
                          {p.remaining > 0 ? (
                            <>
                              <span className="mx-1.5 text-(--color-muted-foreground)/60">·</span>
                              {labels.detail.installmentsList.remaining.replace("{{value}}", formatBRL(p.remaining))}
                            </>
                          ) : null}
                        </p>
                      </div>
                      <span className="font-mono text-[12px] text-(--color-foreground)">
                        {formatBRL(p.perInstallment)}
                      </span>
                    </button>
                    {expandedPlanId === p.planId ? (
                      <PlanDetail
                        planId={p.planId}
                        store={store}
                        onClose={() => setExpandedPlanId(null)}
                      />
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </section>

          {/* Projection — honest empty */}
          <section className="space-y-2">
            <Header label={labels.detail.projection.title} hint={labels.detail.projection.hint} />
            <p className="rounded-md border border-dashed border-(--color-border) py-4 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
              {labels.detail.projection.empty}
            </p>
          </section>
        </div>
      </motion.div>
    </div>
  );
}

function Header({ label, hint }: { label: string; hint?: string }) {
  return (
    <header>
      <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">
        {label}
      </p>
      {hint ? <p className="mt-0.5 text-[11px] text-(--color-muted-foreground)">{hint}</p> : null}
    </header>
  );
}

function Stat({
  className,
  label,
  value,
  sub,
  tone,
}: {
  className?: string;
  label: string;
  value: string;
  sub?: string;
  tone?: "rose" | "amber";
}) {
  const toneCls =
    tone === "rose"
      ? "border-rose-200 bg-rose-50 dark:border-rose-700 dark:bg-rose-500/10"
      : tone === "amber"
        ? "border-amber-200 bg-amber-50 dark:border-amber-700 dark:bg-amber-500/10"
        : "border-(--color-border) bg-(--color-card)";
  const valueCls =
    tone === "rose"
      ? "text-rose-700 dark:text-rose-300"
      : tone === "amber"
        ? "text-amber-800 dark:text-amber-300"
        : "text-(--color-foreground)";
  return (
    <div className={cn("rounded-md border px-2.5 py-1.5", toneCls, className)}>
      <p className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{label}</p>
      <p className={cn("mt-0.5 font-mono text-[13px] font-semibold", valueCls)}>{value}</p>
      {sub ? (
        <p className="mt-0.5 font-mono text-[9.5px] text-(--color-muted-foreground)/80">{sub}</p>
      ) : null}
    </div>
  );
}

function formatNextDue(ms: number): string {
  return activeFormat().date(ms, "short");
}

/* ── Plan detail (inline expansion) ─────────────────────────────────── */

function PlanDetail({
  planId,
  store,
  onClose,
}: {
  planId: string;
  store: FinanceStore;
  onClose: () => void;
}) {
  const t = useT();
  const plan = store.purchasePlansById.get(planId);
  const cancelMut = useCancelPurchasePlan();
  const txs = store.state.transactions
    .filter((t) => t.planId === planId)
    .sort((a, b) => (a.installmentNumber ?? 0) - (b.installmentNumber ?? 0));
  if (!plan) return null;
  const paidCount = txs.filter((t) => t.status === "paid").length;
  const remaining = txs.reduce((sum, t) => (t.status !== "paid" ? sum + t.amount : sum), 0);
  const canCancel = paidCount < plan.totalInstallments;

  const handleCancelRemaining = () => {
    const msg = paidCount === 0
      ? `Cancel the entire plan? All ${plan.totalInstallments} installments will be removed.`
      : `Cancel installments after #${paidCount}? ${plan.totalInstallments - paidCount} scheduled rows will be removed; ${paidCount} paid row${paidCount === 1 ? "" : "s"} preserved.`;
    if (!window.confirm(msg)) return;
    cancelMut.mutate(
      { id: planId },
      { onSuccess: () => onClose() },
    );
  };

  return (
    <div className="border-t border-(--color-border) bg-(--color-muted)/30 px-3 py-3">
      <div className="mb-2 grid grid-cols-12 gap-2">
        <div className="col-span-6">
          <p className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{t.app.modules.finance.cardDetail.original}</p>
          <p className="font-mono text-[12px] text-(--color-foreground)">{formatBRL(plan.originalAmount)}</p>
        </div>
        <div className="col-span-6">
          <p className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{t.app.modules.finance.cardDetail.remaining}</p>
          <p className="font-mono text-[12px] text-(--color-foreground)">{formatBRL(remaining)}</p>
        </div>
      </div>
      <ul className="mb-2 max-h-48 space-y-0.5 overflow-y-auto rounded-md border border-(--color-border) bg-(--color-card) p-1">
        {txs.map((tx) => (
          <li
            key={tx.id}
            className="flex items-center justify-between gap-2 rounded-sm px-2 py-1 font-mono text-[10.5px]"
          >
            <span className="text-(--color-muted-foreground)">
              {tx.installmentNumber ?? "?"}/{plan.totalInstallments} · {activeFormat().date(tx.date, "dayMonth")}
            </span>
            <span className="flex items-center gap-2">
              <span
                className={cn(
                  "rounded-sm border px-1 py-px text-[9px] uppercase tracking-wide",
                  tx.status === "paid"
                    ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300"
                    : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)",
                )}
              >
                {tx.status}
              </span>
              <span className="text-(--color-foreground)">{formatBRL(tx.amount)}</span>
            </span>
          </li>
        ))}
      </ul>
      <div className="flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
        >
          {t.app.common.shared.close}
        </button>
        {canCancel ? (
          <button
            type="button"
            onClick={handleCancelRemaining}
            className="rounded-md border border-rose-300 bg-rose-50 px-2 py-1 font-mono text-[10px] uppercase tracking-[0.14em] text-rose-700 hover:bg-rose-100 dark:border-rose-700 dark:bg-rose-500/10 dark:text-rose-300"
          >
            {t.app.modules.finance.plans.cancelRemaining}
          </button>
        ) : null}
      </div>
    </div>
  );
}

/* ── Cycle math ─────────────────────────────────────────────────────── */

type Cycle = {
  label: string;
  cycleStart: number;
  cycleEnd: number;
  total: Cents;
};

function buildCycles(card: CreditCard, txs: Transaction[], n: number): Cycle[] {
  const cycles: Cycle[] = [];
  const today = new Date();
  for (let i = 0; i < n; i++) {
    const ref = new Date(today.getFullYear(), today.getMonth() - i, 1);
    const start = new Date(ref.getFullYear(), ref.getMonth() - 1, card.closingDay).getTime();
    const end   = new Date(ref.getFullYear(), ref.getMonth(),     card.closingDay - 1, 23, 59, 59, 999).getTime();
    const label = activeFormat().date(ref, "monthYear");
    let total = 0;
    for (const tx of txs) {
      if (tx.paymentMethod !== "credit" || tx.accountId !== card.id) continue;
      if (tx.date >= start && tx.date <= end) total += tx.amount;
    }
    cycles.push({ label, cycleStart: start, cycleEnd: end, total });
  }
  return cycles;
}
