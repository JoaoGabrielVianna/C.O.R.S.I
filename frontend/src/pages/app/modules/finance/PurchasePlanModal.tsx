import { useEffect, useState, type FormEvent, type KeyboardEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import {
  dateToInputValue,
  formatBRL,
  inputValueToDate,
  parseBRL,
} from "./format";
import type { FinanceStore } from "./store";
import { useCreatePurchasePlan } from "@/modules/finance/hooks/usePurchasePlans";
import { useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = {
  open: boolean;
  store: FinanceStore;
  onClose: () => void;
};

/**
 * PurchasePlanModal — open a new multi-installment credit purchase.
 *
 * Submitting calls the PurchasePlans aggregate (backend); on success the
 * backend materializes N child transactions (paid + scheduled) which
 * flow into the existing transactions feed — that's how the dashboard
 * picks up the new commitment without a plan-specific aggregation.
 *
 * Editing isn't supported here: per the totals contract, installment
 * rows are individual transactions and amount/status/date changes
 * belong on the transaction itself. Cancelling is handled in the
 * PurchasePlansSection's confirm dialog.
 */
export function PurchasePlanModal({ open, store, onClose }: Props) {
  return (
    <AnimatePresence>{open ? <Body store={store} onClose={onClose} /> : null}</AnimatePresence>
  );
}

function Body({ store, onClose }: { store: FinanceStore; onClose: () => void }) {
  const t = useT();
  const { state } = store;
  const createMut = useCreatePurchasePlan();

  const expenseCats = state.categories.filter((c) => c.type === "expense");
  const initialPerson = (state.people.find((p) => p.active) ?? state.people[0])?.id ?? "";
  const initialCard   = store.activeCreditCards[0]?.id ?? "";
  const initialCat    = expenseCats[0]?.id ?? "";

  const [name, setName]                 = useState<string>("");
  const [amountText, setAmount]         = useState<string>("");
  const [installments, setInstallments] = useState<number>(12);
  const [categoryId, setCategoryId]     = useState<string>(initialCat);
  const [cardId, setCardId]             = useState<string>(initialCard);
  const [personId, setPersonId]         = useState<string>(initialPerson);
  const [firstDue, setFirstDue]         = useState<string>(() => dateToInputValue(Date.now()));
  const [notes, setNotes]               = useState<string>("");
  const [error, setError]               = useState<string | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey as unknown as EventListener);
    return () => window.removeEventListener("keydown", onKey as unknown as EventListener);
  }, [onClose]);

  const total = parseBRL(amountText);
  const perInstallment = installments > 0 ? Math.round(total / installments) : 0;

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setError(null);
    if (!name.trim()) return setError("Give the purchase a name.");
    if (total <= 0) return setError("Total amount must be greater than zero.");
    if (installments < 1 || installments > 60) {
      return setError("Installments must be between 1 and 60.");
    }
    if (!cardId) return setError("Pick a credit card.");
    if (!categoryId) return setError("Pick a category.");

    createMut.mutate(
      {
        description: name.trim(),
        originalAmount: total,
        totalInstallments: installments,
        cardId,
        personId: personId || null,
        categoryId,
        firstDueDate: inputValueToDate(firstDue),
        notes: notes.trim(),
      },
      {
        onSuccess: () => onClose(),
        onError: (err) => setError(err.message),
      },
    );
  };

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center px-4 pt-[8vh]">
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
        aria-label={t.app.modules.finance.plans.newTitle}
        initial={{ opacity: 0, y: -12, scale: 0.985 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: -8, scale: 0.985 }}
        transition={{ duration: 0.2, ease }}
        className="relative w-full max-w-xl overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
      >
        <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-5 py-3">
          <div>
            <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
              {t.app.modules.finance.plans.eyebrow}
            </p>
            <h2 className="mt-0.5 font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
              {t.app.modules.finance.plans.newTitle}
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

        <form onSubmit={handleSubmit} className="space-y-3 px-5 py-4">
          <Field label={t.app.modules.finance.plans.name}>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t.app.modules.finance.plans.namePlaceholder}
              autoFocus
            />
          </Field>

          <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
            <Field label={t.app.modules.finance.plans.totalAmount}>
              <Input
                value={amountText}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="0,00"
                inputMode="decimal"
              />
              {amountText ? (
                <p className="font-mono text-[10.5px] text-(--color-muted-foreground)">
                  = {formatBRL(total)}
                </p>
              ) : null}
            </Field>
            <Field label={t.app.modules.finance.plans.installments}>
              <Input
                type="number"
                min={1}
                max={60}
                value={installments}
                onChange={(e) =>
                  setInstallments(Math.max(1, Math.min(60, parseInt(e.target.value || "1", 10) || 1)))
                }
              />
              {installments > 0 && total > 0 ? (
                <p className="font-mono text-[10px] text-(--color-muted-foreground)">
                  {installments}× · {formatBRL(perInstallment)} per month
                </p>
              ) : null}
            </Field>
          </div>

          <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
            <Field label={t.app.modules.finance.plans.category}>
              <Select value={categoryId} onChange={setCategoryId}>
                {expenseCats.map((c) => (
                  <option key={c.id} value={c.id}>{c.name}</option>
                ))}
              </Select>
            </Field>
            <Field label={t.app.modules.finance.plans.card}>
              <Select value={cardId} onChange={setCardId}>
                <option value="">—</option>
                {store.activeCreditCards.map((c) => (
                  <option key={c.id} value={c.id}>{c.name}</option>
                ))}
              </Select>
            </Field>
          </div>

          <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
            <Field label={t.app.modules.finance.plans.person}>
              <Select value={personId} onChange={setPersonId}>
                <option value="">—</option>
                {state.people.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </Select>
            </Field>
            <Field label={t.app.modules.finance.plans.firstDue}>
              <input
                type="date"
                value={firstDue}
                onChange={(e) => setFirstDue(e.target.value)}
                className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
              />
            </Field>
          </div>

          <Field label={t.app.modules.finance.plans.notes}>
            <textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder={t.app.modules.finance.plans.notesPlaceholder}
              rows={2}
              className="w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] text-(--color-foreground) placeholder:text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none resize-y focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
            />
          </Field>

          {error ? (
            <p className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-[12.5px] text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300">
              {error}
            </p>
          ) : null}

          <div className="flex items-center justify-end gap-2 pt-1">
            <Button type="button" variant="ghost" onClick={onClose} disabled={createMut.isPending}>
              {t.app.modules.finance.plans.cancel}
            </Button>
            <Button type="submit" disabled={createMut.isPending}>
              {createMut.isPending ? "Creating…" : "Create plan"}
            </Button>
          </div>
        </form>
      </motion.div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-[11.5px] text-(--color-muted-foreground)">{label}</Label>
      {children}
    </div>
  );
}

function Select({
  value,
  onChange,
  children,
}: {
  value: string;
  onChange: (v: string) => void;
  children: React.ReactNode;
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
    >
      {children}
    </select>
  );
}
