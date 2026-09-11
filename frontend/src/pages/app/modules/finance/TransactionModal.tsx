import { useEffect, useState, type FormEvent, type KeyboardEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { useT } from "@/lib/i18n";
import {
  dateToInputValue,
  formatBRL,
  inputValueToDate,
  parseBRL,
} from "./format";
import type { FinanceStore } from "./store";
import type {
  PaymentMethod,
  Transaction,
  TransactionSource,
  TransactionStatus,
  TransactionType,
} from "./types";
import {
  editWritesToBackend,
  isBackendTransactionId,
  useCreateTransaction,
  useDeleteTransaction,
  useUpdateTransaction,
} from "@/modules/finance/hooks/useTransactions";
import {
  useCreateTransfer,
  useDeleteTransfer,
} from "@/modules/finance/hooks/useTransfers";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = {
  open: boolean;
  editing: Transaction | null;
  store: FinanceStore;
  onClose: () => void;
};

/**
 * TransactionModal — add or edit a single transaction.
 *
 * The form body is keyed by `editing?.id ?? "new"` so each open of a different
 * transaction (or new vs edit) remounts the form with fresh, prop-derived
 * defaults via `useState` lazy init. No useEffect-from-props sync needed.
 *
 * Amounts are typed in BRL (1234,56 / 1234.56) and converted to cents on submit.
 */
export function TransactionModal({ open, editing, store, onClose }: Props) {
  return (
    <AnimatePresence>
      {open ? (
        <TransactionFormBody
          key={editing?.id ?? "new"}
          editing={editing}
          store={store}
          onClose={onClose}
        />
      ) : null}
    </AnimatePresence>
  );
}

function TransactionFormBody({
  editing,
  store,
  onClose,
}: {
  editing: Transaction | null;
  store: FinanceStore;
  onClose: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.transactionModal;
  const { state } = store;

  /**
   * Routing: backend handles income/expense. Transfers stay 100% local
   * (the backend doesn't model them yet). Multi-installment purchases
   * have ONE entry point — the dedicated PurchasePlanModal, which hits
   * `POST /finance/purchase-plans`. The "credit + N installments" branch
   * that used to live in this modal was removed to keep a single source
   * of truth on the backend.
   * Legacy seeded transactions (non-UUID ids) also stay local.
   *
   * TODO backend-v0.3: lift transfers to the backend.
   */
  const createMut = useCreateTransaction();
  const updateMut = useUpdateTransaction();
  const deleteMut = useDeleteTransaction();
  const createTransferMut = useCreateTransfer();
  const deleteTransferMut = useDeleteTransfer();
  const pending =
    createMut.isPending ||
    updateMut.isPending ||
    deleteMut.isPending ||
    createTransferMut.isPending ||
    deleteTransferMut.isPending;

  const initialType: TransactionType = editing?.type ?? "expense";
  const initialPersonId = editing?.personId ?? (state.people.find((p) => p.active) ?? state.people[0])?.id ?? "";
  const initialCategoryId =
    editing?.categoryId ?? state.categories.find((c) => c.type === (initialType === "transfer" ? "expense" : initialType))?.id ?? "";

  const [type, setType]           = useState<TransactionType>(() => initialType);
  const [amountText, setAmount]   = useState<string>(() =>
    editing ? (editing.amount / 100).toString().replace(".", ",") : "",
  );
  const [description, setDesc]    = useState<string>(() => editing?.description ?? "");
  const [personId, setPersonId]   = useState<string>(() => initialPersonId);
  const [categoryId, setCatId]    = useState<string>(() => initialCategoryId);
  const [method, setMethod]       = useState<PaymentMethod>(() => editing?.paymentMethod ?? "pix");
  const [accountId, setAccount]   = useState<string>(() => editing?.accountId ?? "");
  const [date, setDate]           = useState<string>(() => dateToInputValue(editing?.date ?? Date.now()));
  const [status, setStatus]       = useState<TransactionStatus>(() => editing?.status ?? "paid");
  const [source, setSource]       = useState<TransactionSource>(() => editing?.source ?? "manual");
  const [notes, setNotes]         = useState<string>(() => editing?.notes ?? "");
  const [splitAmong, setSplitAmong] = useState<string[]>(() => editing?.splitAmong ?? []);
  const [fromAccountId, setFromAccountId] = useState<string>(() => editing?.fromAccountId ?? "");
  const [toAccountId, setToAccountId]     = useState<string>(() => editing?.toAccountId ?? "");
  // Transfer leg categories — required by the backend (from must be an
  // expense category, to must be an income one). The pickers below
  // hand the picked ids straight to `POST /finance/transfers`.
  const [fromCategoryId, setFromCategoryId] = useState<string>(
    () => state.categories.find((c) => c.type === "expense")?.id ?? "",
  );
  const [toCategoryId, setToCategoryId]     = useState<string>(
    () => state.categories.find((c) => c.type === "income")?.id ?? "",
  );
  const [error, setError]         = useState<string | null>(null);

  const toggleSplit = (id: string) => {
    setSplitAmong((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey as unknown as EventListener);
    return () => window.removeEventListener("keydown", onKey as unknown as EventListener);
  }, [onClose]);

  const filteredCategories = state.categories.filter((c) => c.type === (type === "transfer" ? "expense" : type));

  const onTypeChange = (next: TransactionType) => {
    if (next === type) return;
    setType(next);
    if (next === "transfer") {
      setMethod("transfer");
      setSplitAmong([]);
      return;
    }
    const stillValid = state.categories.some((c) => c.id === categoryId && c.type === next);
    if (!stillValid) {
      const fallback = state.categories.find((c) => c.type === next);
      if (fallback) setCatId(fallback.id);
    }
  };

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setError(null);
    const cents = parseBRL(amountText);
    if (cents <= 0) return setError(labels.errors.amount);
    if (!personId) return setError(labels.errors.person);

    // ── Transfer: backend-owned. POST /finance/transfers creates both
    // legs atomically (expense + income with a shared transfer_pair_id).
    // There's no PATCH endpoint, so we only allow create here — editing
    // a backend transfer means delete-and-recreate. Legacy local
    // transfers (non-UUID id) keep the in-memory update path.
    if (type === "transfer") {
      if (!fromAccountId || !toAccountId) return setError(labels.errors.transferAccounts);
      if (fromAccountId === toAccountId) return setError(labels.errors.transferAccounts);
      if (!fromCategoryId || !toCategoryId) {
        return setError("Pick both a 'from' (expense) and a 'to' (income) category.");
      }
      if (editing && !isBackendTransactionId(editing.id)) {
        store.updateTransaction(editing.id, {
          type,
          personId,
          amount: cents,
          description: description.trim(),
          categoryId: undefined,
          paymentMethod: "transfer" as PaymentMethod,
          accountId: null,
          fromAccountId,
          toAccountId,
          date: inputValueToDate(date),
          status,
          source,
          notes: notes.trim(),
        });
        onClose();
        return;
      }
      if (editing) {
        return setError("Transfers can't be edited — delete and recreate.");
      }
      createTransferMut.mutate(
        {
          fromCategoryId,
          toCategoryId,
          fromAccountId: isBackendTransactionId(fromAccountId) ? fromAccountId : null,
          toAccountId:   isBackendTransactionId(toAccountId)   ? toAccountId   : null,
          amount: cents,
          occurredAt: inputValueToDate(date),
          description: description.trim(),
          notes: notes.trim(),
        },
        { onSuccess: () => onClose(), onError: (err) => setError(err.message) },
      );
      return;
    }

    if (!categoryId) return setError(labels.errors.category);
    const cleanedSplit = type === "expense" && splitAmong.length >= 2 ? splitAmong : undefined;
    const baseStart = inputValueToDate(date);

    // ── Income / expense ─────────────────────────────────────────────
    //   - editing a backend row (UUID id) → backend update
    //   - editing a legacy row (non-UUID) → local update (no wire)
    //   - creating new                    → backend create
    //
    // A plan installment used to be forced down the local path. It is an
    // ordinary backend transaction that happens to carry a plan_id, and
    // routing it locally meant the edit was written to an array that holds
    // no backend rows — silently, with the modal closing as though it had
    // saved. See editWritesToBackend.
    if (editing) {
      const goLocal = !editWritesToBackend(editing);
      if (goLocal) {
        store.updateTransaction(editing.id, {
          type,
          personId,
          amount: cents,
          description: description.trim(),
          categoryId,
          paymentMethod: method,
          accountId: method === "credit" ? (accountId || null) : null,
          date: baseStart,
          status,
          source,
          notes: notes.trim(),
          splitAmong: cleanedSplit,
          planId: editing.planId,
          installmentNumber: editing.installmentNumber,
        });
        onClose();
        return;
      }
      updateMut.mutate(
        {
          id: editing.id,
          categoryId,
          amount: cents,
          description: description.trim(),
          occurredAt: baseStart,
          status,
          paymentMethod: method,
          source,
          notes: notes.trim(),
          personId,
          splitAmong: cleanedSplit,
          // If user moved off credit, explicitly clear the backend
          // account_id. Otherwise: UUID → wire; non-UUID → sidecar (the
          // hook routes internally).
          accountId: method === "credit" ? (accountId || null) : undefined,
          clearAccountId: method !== "credit",
        },
        { onSuccess: () => onClose(), onError: (err) => setError(err.message) },
      );
      return;
    }

    createMut.mutate(
      {
        type,
        categoryId,
        amount: cents,
        description: description.trim(),
        occurredAt: baseStart,
        status,
        paymentMethod: method,
        source,
        notes: notes.trim(),
        personId,
        splitAmong: cleanedSplit,
        accountId: method === "credit" ? (accountId || null) : null,
      },
      { onSuccess: () => onClose(), onError: (err) => setError(err.message) },
    );
  };

  const handleDelete = () => {
    if (!editing) return;
    setError(null);
    if (!isBackendTransactionId(editing.id)) {
      store.removeTransaction(editing.id);
      onClose();
      return;
    }
    // Backend transfer legs can't be deleted individually — the BE
    // returns 409 on DELETE /transactions/{id}. Use the pair endpoint
    // so both legs go away together.
    if (editing.transferPairId) {
      deleteTransferMut.mutate(editing.transferPairId, {
        onSuccess: () => onClose(),
        onError: (err) => setError(err.message),
      });
      return;
    }
    deleteMut.mutate(editing.id, {
      onSuccess: () => onClose(),
      onError: (err) => setError(err.message),
    });
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
        aria-label={editing ? labels.titleEdit : labels.titleNew}
        initial={{ opacity: 0, y: -12, scale: 0.985 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: -8, scale: 0.985 }}
        transition={{ duration: 0.2, ease }}
        className="relative w-full max-w-xl overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
      >
        <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-5 py-3">
          <div>
            <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
              Finance
            </p>
            <h2 className="mt-0.5 font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
              {editing ? labels.titleEdit : labels.titleNew}
            </h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label={labels.cancel}
            className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
          >
            <X className="size-3.5" />
          </button>
        </header>

        <form onSubmit={handleSubmit} className="space-y-3 px-5 py-4">
          <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
            <Field label={labels.fields.type}>
              <div className="flex gap-1">
                <ToggleButton active={type === "expense"}  onClick={() => onTypeChange("expense")}>{labels.types.expense}</ToggleButton>
                <ToggleButton active={type === "income"}   onClick={() => onTypeChange("income")}>{labels.types.income}</ToggleButton>
                <ToggleButton active={type === "transfer"} onClick={() => onTypeChange("transfer")}>{labels.types.transfer}</ToggleButton>
              </div>
            </Field>
            <Field label={labels.fields.amount}>
              <Input
                value={amountText}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="0,00"
                inputMode="decimal"
                autoFocus
              />
              {amountText ? (
                <p className="font-mono text-[10.5px] text-(--color-muted-foreground)">
                  = {formatBRL(parseBRL(amountText))}
                </p>
              ) : null}
            </Field>
          </div>

          <Field label={labels.fields.description}>
            <Input
              value={description}
              onChange={(e) => setDesc(e.target.value)}
              placeholder={labels.placeholders.description}
            />
          </Field>

          <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
            <Field label={labels.fields.person}>
              <Select value={personId} onChange={setPersonId}>
                {state.people.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </Select>
            </Field>
            {type === "transfer" ? (
              <Field label={labels.fields.fromAccount}>
                <Select value={fromAccountId} onChange={setFromAccountId}>
                  <option value="">—</option>
                  {store.activeAccounts.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name} {a.type === "card" ? "· card" : ""}
                    </option>
                  ))}
                </Select>
              </Field>
            ) : (
              <Field label={labels.fields.category}>
                <Select value={categoryId} onChange={setCatId}>
                  {filteredCategories.map((c) => (
                    <option key={c.id} value={c.id}>{c.name}</option>
                  ))}
                </Select>
              </Field>
            )}
          </div>

          {type === "transfer" ? (
            <Field label={labels.fields.toAccount}>
              <Select value={toAccountId} onChange={setToAccountId}>
                <option value="">—</option>
                {store.activeAccounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} {a.type === "card" ? "· card" : ""}
                  </option>
                ))}
              </Select>
            </Field>
          ) : null}

          {type === "transfer" ? (
            <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
              <Field label={t.app.modules.finance.transfer.fromCategory}>
                <Select value={fromCategoryId} onChange={setFromCategoryId}>
                  {state.categories
                    .filter((c) => c.type === "expense")
                    .map((c) => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                </Select>
              </Field>
              <Field label={t.app.modules.finance.transfer.toCategory}>
                <Select value={toCategoryId} onChange={setToCategoryId}>
                  {state.categories
                    .filter((c) => c.type === "income")
                    .map((c) => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                </Select>
              </Field>
            </div>
          ) : null}

          {type === "transfer" ? (
            <Field label={labels.fields.date}>
              <input
                type="date"
                value={date}
                onChange={(e) => setDate(e.target.value)}
                className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
              />
            </Field>
          ) : (
            <>
              <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
                <Field label={labels.fields.method}>
                  <Select value={method} onChange={(v) => setMethod(v as PaymentMethod)}>
                    <option value="pix">{labels.methods.pix}</option>
                    <option value="debit">{labels.methods.debit}</option>
                    <option value="credit">{labels.methods.credit}</option>
                    <option value="cash">{labels.methods.cash}</option>
                    <option value="transfer">{labels.methods.transfer}</option>
                  </Select>
                </Field>
                {method === "credit" ? (
                  <Field label={labels.fields.account}>
                    <Select value={accountId} onChange={setAccount}>
                      <option value="">—</option>
                      {store.activeCreditCards.map((c) => (
                        <option key={c.id} value={c.id}>{c.name}</option>
                      ))}
                      {editing && accountId && store.cardsById.get(accountId)?.deletedAt ? (
                        <option value={accountId}>
                          {store.cardsById.get(accountId)?.name} (deleted)
                        </option>
                      ) : null}
                    </Select>
                  </Field>
                ) : (
                  <Field label={labels.fields.date}>
                    <input
                      type="date"
                      value={date}
                      onChange={(e) => setDate(e.target.value)}
                      className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
                    />
                  </Field>
                )}
              </div>

              {method === "credit" ? (
                <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
                  <Field label={labels.fields.date}>
                    <input
                      type="date"
                      value={date}
                      onChange={(e) => setDate(e.target.value)}
                      className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
                    />
                  </Field>
                </div>
              ) : null}
            </>
          )}

          <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
            <Field label={labels.fields.status}>
              <Select value={status} onChange={(v) => setStatus(v as TransactionStatus)}>
                <option value="paid">{labels.statuses.paid}</option>
                <option value="pending">{labels.statuses.pending}</option>
                <option value="scheduled">{labels.statuses.scheduled}</option>
              </Select>
            </Field>
            <Field label={labels.fields.source}>
              <Select value={source} onChange={(v) => setSource(v as TransactionSource)}>
                <option value="manual">{labels.sources.manual}</option>
                <option value="whatsapp">{labels.sources.whatsapp}</option>
                <option value="import">{labels.sources.import}</option>
                <option value="ai">{labels.sources.ai}</option>
              </Select>
            </Field>
          </div>

          {type === "expense" && state.people.length >= 2 ? (
            <Field label={labels.fields.splitWith}>
              <div className="flex flex-wrap gap-1.5">
                {state.people.map((p) => {
                  const active = splitAmong.includes(p.id);
                  return (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() => toggleSplit(p.id)}
                      className={cn(
                        "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11.5px] font-medium transition-colors",
                        active
                          ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
                          : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:text-(--color-foreground)",
                      )}
                    >
                      <span
                        aria-hidden
                        className={cn(
                          "size-3.5 rounded-sm border",
                          active
                            ? "border-(--color-brand-500) bg-(--color-brand-500)"
                            : "border-(--color-border-strong) bg-(--color-card)",
                        )}
                      />
                      {p.name}
                    </button>
                  );
                })}
              </div>
              {splitAmong.length >= 2 ? (
                <p className="font-mono text-[10px] text-(--color-muted-foreground)">
                  {labels.fields.splitHint.replace("{{n}}", String(splitAmong.length))}
                </p>
              ) : (
                <p className="font-mono text-[10px] text-(--color-muted-foreground)/80">
                  {labels.fields.splitNone}
                </p>
              )}
            </Field>
          ) : null}

          <Field label={labels.fields.notes}>
            <textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder={labels.placeholders.notes}
              rows={2}
              className="w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] text-(--color-foreground) placeholder:text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none resize-y focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
            />
          </Field>

          {error ? (
            <p className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-[12.5px] text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300">
              {error}
            </p>
          ) : null}

          <div className="flex items-center justify-between gap-2 pt-1">
            {editing ? (
              <button
                type="button"
                onClick={handleDelete}
                disabled={pending}
                className="inline-flex items-center gap-1.5 rounded-md text-[12px] font-medium text-rose-600 hover:text-rose-500 disabled:opacity-50"
              >
                {labels.delete}
              </button>
            ) : <span />}
            <div className="flex items-center gap-2">
              <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>{labels.cancel}</Button>
              <Button type="submit" disabled={pending}>{editing ? labels.save : labels.create}</Button>
            </div>
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

function ToggleButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex flex-1 items-center justify-center rounded-lg border px-2 py-1.5 text-[12px] font-medium transition-colors",
        active
          ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
          : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:text-(--color-foreground)",
      )}
    >
      {children}
    </button>
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
