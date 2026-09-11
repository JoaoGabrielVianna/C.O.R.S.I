import { useState, type FormEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { CreditCard as CreditCardIcon, Plus, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { Button } from "@/components/ui/Button";
import { useT } from "@/lib/i18n";
import { CardDetailModal } from "./CardDetailModal";
import { CardPreview } from "./CardPreview";
import { CompactCardRow } from "./CompactCardRow";
import { parseBRL } from "./format";
import type { FinanceStore } from "./store";
import type { CreditCard } from "./types";
import {
  useArchiveCard,
  useCreateCard,
  useUpdateCard,
} from "@/modules/finance/hooks/useCards";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = { store: FinanceStore };

/**
 * CardsInvoices — compact list of credit-card rows.
 *
 * Each row scans in ≤ 88 px (Linear/Stripe/ERP density). Click a row to open
 * `<CardDetailModal />` with the limit summary, cycle switcher, transactions,
 * history and honest empty states for installments / projection. Card
 * metadata (name, limit, colour, etc.) is edited via `<CardForm />` which
 * opens from the detail modal's Edit button OR the top-level Add card CTA.
 */
export function CardsInvoices({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.finance.cards;
  const archiveCard = useArchiveCard();
  const [editing, setEditing] = useState<CreditCard | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [showArchived, setShowArchived] = useState(false);

  const openNew = () => {
    setEditing(null);
    setFormOpen(true);
  };

  const openDetail = (c: CreditCard) => setDetailId(c.id);

  const detailCard = detailId
    ? store.state.creditCards.find((c) => c.id === detailId) ?? null
    : null;

  const archivedCards = store.state.creditCards.filter((c) => c.deletedAt);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-y-auto pb-2">
      <header className="flex items-center justify-between">
        <h2 className="font-display text-base font-semibold tracking-tight text-(--color-foreground)">
          {labels.cardsTitle}
        </h2>
        <div className="flex items-center gap-2">
          {archivedCards.length > 0 ? (
            <button
              type="button"
              onClick={() => setShowArchived((s) => !s)}
              className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
            >
              {showArchived ? "Hide" : "Show"} archived ({archivedCards.length})
            </button>
          ) : null}
          <button
            type="button"
            onClick={openNew}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) hover:bg-(--color-muted)"
          >
            <Plus className="size-3.5" />
            {labels.addCard}
          </button>
        </div>
      </header>

      {store.activeCreditCards.length === 0 ? (
        <Empty />
      ) : (
        <ul className="space-y-2">
          {store.activeCreditCards.map((c) => (
            <li key={c.id}>
              <CompactCardRow
                card={c}
                transactions={store.state.transactions}
                owner={store.peopleById.get(c.ownerId)}
                onOpen={() => openDetail(c)}
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

      {showArchived && archivedCards.length > 0 ? (
        <section className="mt-2 space-y-2">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
            {t.app.modules.finance.accounts.archived}
          </p>
          <ul className="space-y-2 opacity-60">
            {archivedCards.map((c) => (
              <li key={c.id}>
                <CompactCardRow
                  card={c}
                  transactions={store.state.transactions}
                  owner={store.peopleById.get(c.ownerId)}
                  onOpen={() => openDetail(c)}
                  onDelete={() => { /* already archived */ }}
                />
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <BankAccounts store={store} />

      <CardForm
        open={formOpen}
        editing={editing}
        store={store}
        onClose={() => setFormOpen(false)}
      />

      <CardDetailModal
        open={detailId !== null}
        card={detailCard}
        store={store}
        onClose={() => setDetailId(null)}
        onEdit={(c) => {
          setDetailId(null);
          setEditing(c);
          setFormOpen(true);
        }}
      />
    </div>
  );
}

/* ── Bank accounts mini-admin ────────────────────────────────────────── */

function BankAccounts({ store }: { store: FinanceStore }) {
  const t = useT();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [ownerId, setOwnerId] = useState<string>(() => store.state.people[0]?.id ?? "");
  const [institution, setInstitution] = useState("");

  const banks = store.state.accounts.filter((a) => a.type === "bank" && !a.deletedAt);

  const submit = () => {
    if (!name.trim() || !ownerId) return;
    store.addAccount({
      name: name.trim(),
      ownerId,
      type: "bank",
      institution: institution.trim() || undefined,
    });
    setName("");
    setInstitution("");
    setAdding(false);
  };

  return (
    <section className="mt-4 space-y-2">
      <header className="flex items-center justify-between">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">
          {t.app.modules.finance.accounts.bankAccounts}
        </p>
        <button
          type="button"
          onClick={() => setAdding((s) => !s)}
          className="inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
        >
          <Plus className="size-3" />
          {adding ? "Cancel" : "Add account"}
        </button>
      </header>

      {adding ? (
        <div className="grid grid-cols-1 gap-2 rounded-lg border border-(--color-border) bg-(--color-card) p-3 sm:grid-cols-[1fr_1fr_1fr_auto]">
          <Input
            placeholder={t.app.modules.finance.accounts.namePlaceholder}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Input
            placeholder={t.app.modules.finance.accounts.institutionPlaceholder}
            value={institution}
            onChange={(e) => setInstitution(e.target.value)}
          />
          <select
            value={ownerId}
            onChange={(e) => setOwnerId(e.target.value)}
            className="h-10 rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground)"
          >
            {store.state.people.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </select>
          <Button type="button" onClick={submit}>{t.app.modules.finance.accounts.save}</Button>
        </div>
      ) : null}

      {banks.length === 0 ? (
        <p className="rounded-md border border-dashed border-(--color-border) py-3 text-center font-mono text-[10.5px] text-(--color-muted-foreground)">
          {t.app.modules.finance.accounts.empty}
        </p>
      ) : (
        <ul className="divide-y divide-(--color-border) rounded-md border border-(--color-border)">
          {banks.map((a) => {
            const owner = store.peopleById.get(a.ownerId);
            return (
              <li key={a.id} className="flex items-center gap-3 px-3 py-2">
                <span className="flex size-5 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) font-mono text-[9.5px] text-(--color-foreground)" title={owner?.name}>
                  {owner?.initials ?? "?"}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[12.5px] text-(--color-foreground)">{a.name}</p>
                  {a.institution ? (
                    <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{a.institution}</p>
                  ) : null}
                </div>
                <button
                  type="button"
                  onClick={() => {
                    if (window.confirm(`Archive ${a.name}? Historical transfers stay intact.`)) {
                      store.removeAccount(a.id);
                    }
                  }}
                  aria-label={t.app.modules.finance.accounts.archiveLabel}
                  className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-rose-500"
                >
                  {t.app.modules.finance.accounts.archive}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

// ── Card add/edit form ──────────────────────────────────────────────────

function CardForm({
  open,
  editing,
  store,
  onClose,
}: {
  open: boolean;
  editing: CreditCard | null;
  store: FinanceStore;
  onClose: () => void;
}) {
  return (
    <AnimatePresence>
      {open ? (
        <CardFormBody
          key={editing?.id ?? "new"}
          editing={editing}
          store={store}
          onClose={onClose}
        />
      ) : null}
    </AnimatePresence>
  );
}

function CardFormBody({
  editing,
  store,
  onClose,
}: {
  editing: CreditCard | null;
  store: FinanceStore;
  onClose: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.cards.form;
  const createMut = useCreateCard();
  const updateMut = useUpdateCard();

  const [name, setName]            = useState<string>(() => editing?.name ?? "");
  const [ownerId, setOwnerId]      = useState<string>(() =>
    editing?.ownerId ?? (store.state.people.find((p) => p.active) ?? store.state.people[0])?.id ?? "",
  );
  const [limitText, setLimit]      = useState<string>(() =>
    editing ? (editing.limit / 100).toString().replace(".", ",") : "",
  );
  // Backend constrains closing_day / due_day to 1..28. The form clamps on
  // submit; pre-Phase-2 cards with day > 28 round down on first edit.
  const [closingDay, setClosing]   = useState<number>(() => Math.min(editing?.closingDay ?? 1, 28));
  const [dueDay, setDue]           = useState<number>(() => Math.min(editing?.dueDay ?? 10, 28));
  const [brand, setBrand]          = useState<string>(() => editing?.brand ?? "");
  const [last4, setLast4]          = useState<string>(() => editing?.last4 ?? "");
  const [color, setColor]          = useState<string>(() => editing?.color ?? "slate");
  const [submitError, setSubmitError] = useState<string | null>(null);

  const pending = createMut.isPending || updateMut.isPending;

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!name.trim() || !ownerId) return;
    const cleanLast4 = last4.replace(/\D/g, "").slice(0, 4);
    // Backend requires exactly 4 digits.
    if (cleanLast4.length !== 4) {
      setSubmitError("Last 4 digits are required (4 numeric digits).");
      return;
    }
    setSubmitError(null);
    const limit = parseBRL(limitText);
    const onError = (err: Error) => setSubmitError(err.message);
    if (editing) {
      updateMut.mutate(
        {
          id: editing.id,
          name: name.trim(),
          ownerId,
          limit,
          closingDay,
          dueDay,
          brand: brand.trim() || undefined,
          last4: cleanLast4,
          color,
        },
        { onSuccess: () => onClose(), onError },
      );
    } else {
      createMut.mutate(
        {
          name: name.trim(),
          ownerId,
          limit,
          closingDay,
          dueDay,
          brand: brand.trim() || undefined,
          last4: cleanLast4,
          color,
        },
        { onSuccess: () => onClose(), onError },
      );
    }
  };

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center px-4 pt-[10vh]">
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
        initial={{ opacity: 0, y: -12, scale: 0.985 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: -8, scale: 0.985 }}
        transition={{ duration: 0.2, ease }}
        className="relative w-full max-w-md overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
      >
        <header className="flex items-center justify-between border-b border-(--color-border) px-5 py-3">
          <h2 className="font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
            {editing ? labels.titleEdit : labels.titleNew}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)"
          >
            <X className="size-3.5" />
          </button>
        </header>
        <form onSubmit={handleSubmit} className="space-y-3 px-5 py-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.name}</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Nubank · Black" autoFocus />
            </div>
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.owner}</Label>
              <select
                value={ownerId}
                onChange={(e) => setOwnerId(e.target.value)}
                className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
              >
                {store.state.people.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.limit}</Label>
            <Input value={limitText} onChange={(e) => setLimit(e.target.value)} placeholder="15000,00" inputMode="decimal" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.closing}</Label>
              <Input type="number" min={1} max={28} value={closingDay} onChange={(e) => setClosing(parseInt(e.target.value || "1", 10))} />
            </div>
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.due}</Label>
              <Input type="number" min={1} max={28} value={dueDay} onChange={(e) => setDue(parseInt(e.target.value || "10", 10))} />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.brand}</Label>
              <Input value={brand} onChange={(e) => setBrand(e.target.value)} placeholder="visa · mastercard · amex" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.last4}</Label>
              <Input
                value={last4}
                onChange={(e) => setLast4(e.target.value.replace(/\D/g, "").slice(0, 4))}
                placeholder="1234"
                inputMode="numeric"
                maxLength={4}
                className="font-mono"
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.color}</Label>
            <ColorPicker value={color} onChange={setColor} />
          </div>
          {/* Live preview */}
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.preview}</Label>
            <div className="w-full max-w-[240px]">
              <CardPreview
                card={{
                  id: editing?.id ?? "draft",
                  name: name || "—",
                  ownerId,
                  limit: parseBRL(limitText),
                  closingDay,
                  dueDay,
                  brand: brand || undefined,
                  color,
                  last4: last4 || undefined,
                }}
                size="sm"
              />
            </div>
          </div>
          {submitError ? (
            <p className="text-[11.5px] text-rose-600 dark:text-rose-300">{submitError}</p>
          ) : null}
          <div className="flex items-center justify-end gap-2 pt-1">
            <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>{t.app.modules.finance.common.cancel}</Button>
            <Button type="submit" disabled={pending}>
              {editing ? t.app.modules.finance.common.save : t.app.modules.finance.common.create}
            </Button>
          </div>
        </form>
      </motion.div>
    </div>
  );
}

function ColorPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const colors = ["slate", "rose", "amber", "emerald", "teal", "cyan", "sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink"];
  return (
    <div className="flex flex-wrap gap-1.5">
      {colors.map((c) => (
        <button
          key={c}
          type="button"
          onClick={() => onChange(c)}
          aria-label={c}
          className={cn(
            "size-6 rounded-md border-2 transition-transform",
            `bg-${c}-500`,
            value === c ? "border-(--color-foreground) scale-110" : "border-transparent",
          )}
        />
      ))}
    </div>
  );
}

function Empty() {
  const t = useT();
  return (
    <div className="flex flex-col items-center gap-2 rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-10 text-center">
      <CreditCardIcon className="size-5 text-(--color-muted-foreground)" />
      <p className="text-sm font-medium text-(--color-foreground)">{t.app.modules.finance.cards.empty.title}</p>
      <p className="max-w-sm text-[12.5px] text-(--color-muted-foreground)">{t.app.modules.finance.cards.empty.body}</p>
    </div>
  );
}
