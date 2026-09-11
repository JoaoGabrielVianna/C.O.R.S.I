import { useMemo, useState, type FormEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ArrowUpRight, Plus, Trash2, X } from "lucide-react";
import { Link } from "react-router-dom";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { Button } from "@/components/ui/Button";
import { useT } from "@/lib/i18n";
import { endOfMonth, formatBRL, formatShortDate, personShare, startOfMonth } from "./format";
import type { FinanceStore } from "./store";
import type { Cents, Person, Transaction } from "./types";
import {
  useCreatePerson,
  useDeletePerson,
  useUpdatePerson,
} from "@/modules/finance/hooks/usePersons";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = { store: FinanceStore };

export function People({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.finance.people;
  const [editing, setEditing] = useState<Person | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const updateMut = useUpdatePerson();
  const deleteMut = useDeletePerson();

  const monthStart = startOfMonth(new Date());
  const monthEnd   = endOfMonth(new Date());

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-y-auto pb-2">
      <header className="flex items-center justify-between">
        <h2 className="font-display text-base font-semibold tracking-tight text-(--color-foreground)">{labels.title}</h2>
        <button
          type="button"
          onClick={() => { setEditing(null); setFormOpen(true); }}
          className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) hover:bg-(--color-muted)"
        >
          <Plus className="size-3.5" />
          {labels.add}
        </button>
      </header>

      <section className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(320px,1fr))]">
        {store.state.people.map((p) => (
          <PersonCard
            key={p.id}
            person={p}
            store={store}
            monthStart={monthStart}
            monthEnd={monthEnd}
            onEdit={() => { setEditing(p); setFormOpen(true); }}
            onDelete={() => deleteMut.mutate(p.id)}
            onToggleActive={() => updateMut.mutate({ id: p.id, active: !p.active })}
          />
        ))}
      </section>

      <PersonForm
        open={formOpen}
        editing={editing}
        onClose={() => setFormOpen(false)}
      />
    </div>
  );
}

function PersonCard({
  person,
  store,
  monthStart,
  monthEnd,
  onEdit,
  onDelete,
  onToggleActive,
}: {
  person: Person;
  store: FinanceStore;
  monthStart: number;
  monthEnd: number;
  onEdit: () => void;
  onDelete: () => void;
  onToggleActive: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.people;

  const stats = useMemo(() => {
    // Transactions where this person has any share — own or split.
    const relevant = store.state.transactions.filter(
      (tx) =>
        tx.date >= monthStart &&
        tx.date <= monthEnd &&
        tx.status === "paid" &&
        personShare(tx, person.id) > 0,
    );
    const income  = sumShare(relevant, person.id, "income");
    const expense = sumShare(relevant, person.id, "expense");
    const topCategories = topNShare(
      relevant.filter((t) => t.type === "expense"),
      person.id,
      (t) => t.categoryId ?? "",
    );
    const recent = [...relevant].sort((a, b) => b.date - a.date).slice(0, 4);
    return { income, expense, topCategories, recent };
  }, [store.state.transactions, person.id, monthStart, monthEnd]);

  return (
    <article className={cn("flex flex-col gap-3 rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-soft)", person.active ? "" : "opacity-60")}>
      <header className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) font-mono text-[12px] font-semibold text-(--color-foreground)">
            {person.initials}
          </span>
          <div className="min-w-0">
            <p className="truncate text-[13px] font-semibold text-(--color-foreground)">{person.name}</p>
            <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {person.role} {person.active ? "" : `· ${labels.inactive}`}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-1">
          <Link
            to={`/app/people/${person.id}`}
            className="inline-flex items-center gap-1 rounded-md border border-(--color-border) bg-(--color-card) px-2 py-1 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground) transition-colors hover:border-(--color-brand-300) hover:text-(--color-foreground) dark:hover:border-(--color-brand-700)"
            title={labels.openDetail}
          >
            {labels.openDetail}
            <ArrowUpRight className="size-2.5" />
          </Link>
          <button
            type="button"
            onClick={onToggleActive}
            className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
          >
            {person.active ? labels.deactivate : labels.activate}
          </button>
          <button
            type="button"
            onClick={onEdit}
            className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground) hover:text-(--color-foreground)"
          >
            {t.app.modules.finance.common.edit}
          </button>
          <button
            type="button"
            onClick={onDelete}
            aria-label={t.app.modules.finance.actions.delete}
            className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-rose-500/10 hover:text-rose-500"
          >
            <Trash2 className="size-3" />
          </button>
        </div>
      </header>

      <div className="grid grid-cols-2 gap-2">
        <Stat label={labels.income}  value={formatBRL(stats.income)}  tone="emerald" />
        <Stat label={labels.expense} value={formatBRL(stats.expense)} tone="rose" />
      </div>

      {stats.topCategories.length > 0 ? (
        <div>
          <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {labels.topCategories}
          </p>
          <ul className="space-y-0.5">
            {stats.topCategories.map(([categoryId, value]) => {
              const cat = store.categoriesById.get(categoryId);
              return (
                <li key={categoryId} className="flex items-baseline justify-between gap-2">
                  <span className="inline-flex items-center gap-1.5 text-[12px] text-(--color-foreground)">
                    {cat ? (
                      <span aria-hidden className={cn("size-1.5 rounded-full", `bg-${cat.color}-500`)} />
                    ) : null}
                    {cat?.name ?? "—"}
                  </span>
                  <span className="font-mono text-[10.5px] text-(--color-muted-foreground)">{formatBRL(value)}</span>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}

      {stats.recent.length > 0 ? (
        <div className="border-t border-(--color-border) pt-2">
          <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {labels.recent}
          </p>
          <ul className="space-y-0.5">
            {stats.recent.map((tx) => (
              <li key={tx.id} className="flex items-baseline justify-between gap-2 text-[12px]">
                <span className="line-clamp-1 text-(--color-foreground)">{tx.description || "—"}</span>
                <span className={cn("font-mono text-[10.5px]", tx.type === "income" ? "text-emerald-700 dark:text-emerald-300" : "text-(--color-muted-foreground)")}>
                  {tx.type === "income" ? "+" : "−"}{formatBRL(tx.amount)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div className="flex items-baseline justify-between font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)/80">
        <span>{labels.lastUpdated}</span>
        <span>{stats.recent[0] ? formatShortDate(stats.recent[0].date) : "—"}</span>
      </div>
    </article>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone: string }) {
  return (
    <div className="rounded-lg border border-(--color-border) bg-(--color-background)/40 px-2.5 py-1.5">
      <p className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{label}</p>
      <p className={cn("mt-0.5 font-display text-sm font-semibold tracking-tight", `text-${tone}-700 dark:text-${tone}-300`)}>{value}</p>
    </div>
  );
}

function PersonForm({
  open,
  editing,
  onClose,
}: {
  open: boolean;
  editing: Person | null;
  onClose: () => void;
}) {
  return (
    <AnimatePresence>
      {open ? (
        <PersonFormBody
          key={editing?.id ?? "new"}
          editing={editing}
          onClose={onClose}
        />
      ) : null}
    </AnimatePresence>
  );
}

function PersonFormBody({
  editing,
  onClose,
}: {
  editing: Person | null;
  onClose: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.people.form;
  const createMut = useCreatePerson();
  const updateMut = useUpdatePerson();
  const pending   = createMut.isPending || updateMut.isPending;

  const [name, setName]   = useState<string>(() => editing?.name ?? "");
  const [role, setRole]   = useState<string>(() => editing?.role ?? "partner");
  const [whatsapp, setWa] = useState<string>(() => editing?.whatsappNumber ?? "");

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!name.trim()) return;
    const trimmedName = name.trim();
    const trimmedRole = role.trim();
    const trimmedWa   = whatsapp.trim() || undefined;
    if (editing) {
      updateMut.mutate(
        {
          id: editing.id,
          name: trimmedName,
          role: trimmedRole,
          active: true,
          whatsappNumber: trimmedWa,
        },
        { onSuccess: () => onClose() },
      );
    } else {
      createMut.mutate(
        {
          name: trimmedName,
          role: trimmedRole,
          active: true,
          whatsappNumber: trimmedWa,
        },
        { onSuccess: () => onClose() },
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
          <button type="button" onClick={onClose} className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)">
            <X className="size-3.5" />
          </button>
        </header>
        <form onSubmit={handleSubmit} className="space-y-3 px-5 py-4">
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.name}</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="João Corsi" autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.role}</Label>
            <Input value={role} onChange={(e) => setRole(e.target.value)} placeholder="operator · partner · child · shared" />
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.whatsapp}</Label>
            <Input value={whatsapp} onChange={(e) => setWa(e.target.value)} placeholder="+55 11 9 0000 0000" inputMode="tel" />
            <p className="font-mono text-[10px] text-(--color-muted-foreground)/80">{labels.whatsappHint}</p>
          </div>
          <div className="flex items-center justify-end gap-2 pt-1">
            <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>{t.app.modules.finance.common.cancel}</Button>
            <Button type="submit" disabled={pending}>{editing ? t.app.modules.finance.common.save : t.app.modules.finance.common.create}</Button>
          </div>
        </form>
      </motion.div>
    </div>
  );
}

// ── Helpers ──────────────────────────────────────────────────────────────

function sumShare(txs: Transaction[], personId: string, type: "income" | "expense"): Cents {
  let total = 0;
  for (const t of txs) if (t.type === type) total += personShare(t, personId);
  return total;
}

function topNShare(
  txs: Transaction[],
  personId: string,
  key: (t: Transaction) => string,
  n = 3,
): Array<readonly [string, Cents]> {
  const map = new Map<string, Cents>();
  for (const t of txs) {
    const share = personShare(t, personId);
    if (share === 0) continue;
    map.set(key(t), (map.get(key(t)) ?? 0) + share);
  }
  return Array.from(map.entries()).sort((a, b) => b[1] - a[1]).slice(0, n);
}
