import { useMemo, useState, type FormEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { Plus, Tag, Trash2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { Button } from "@/components/ui/Button";
import { useT } from "@/lib/i18n";
import {
  AVAILABLE_COLORS,
  AVAILABLE_ICONS,
  endOfMonth,
  formatBRL,
  parseBRL,
  startOfMonth,
} from "./format";
import { CategoryIcon } from "./CategoryIcon";
import type { FinanceStore } from "./store";
import type { Category, CategoryType } from "./types";
import {
  useCategories,
  useCreateCategory,
  useDeleteCategory,
  useUpdateCategory,
} from "@/modules/finance/hooks/useCategories";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = { store: FinanceStore };

/**
 * Categories — backend-backed via TanStack Query. Reads/writes go to
 * `/finance/categories`; the local-only `budget` field rides along in
 * `categoryMetadata` localStorage until backend v0.2 grows it.
 *
 * The `store` prop is still here so the per-category month stats (which
 * read transactions, fixed expenses, etc.) keep working — those slices
 * haven't been migrated to the backend yet.
 */
export function Categories({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.finance.categories;
  const [editing, setEditing] = useState<Category | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  const categoriesQuery = useCategories();
  const deleteCategory = useDeleteCategory();

  const categories = categoriesQuery.data ?? [];

  const monthStart = startOfMonth(new Date());
  const monthEnd   = endOfMonth(new Date());

  const stats = useMemo(() => {
    const map = new Map<string, { spent: number; count: number }>();
    let totalExpense = 0;
    for (const tx of store.state.transactions) {
      if (tx.date < monthStart || tx.date > monthEnd) continue;
      if (tx.status !== "paid") continue;
      if (tx.type !== "expense") continue;
      const key = tx.categoryId ?? "";
      const entry = map.get(key) ?? { spent: 0, count: 0 };
      entry.spent += tx.amount;
      entry.count += 1;
      map.set(key, entry);
      totalExpense += tx.amount;
    }
    return { map, totalExpense };
  }, [store.state.transactions, monthStart, monthEnd]);

  // Sort: expense categories first (by spent desc), then income (alphabetical)
  const ordered = useMemo(() => {
    const expense = categories
      .filter((c) => c.type === "expense")
      .sort((a, b) => (stats.map.get(b.id)?.spent ?? 0) - (stats.map.get(a.id)?.spent ?? 0));
    const income = categories
      .filter((c) => c.type === "income")
      .sort((a, b) => a.name.localeCompare(b.name));
    return [...expense, ...income];
  }, [categories, stats.map]);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-y-auto pb-2">
      <header className="flex items-center justify-between gap-2">
        <div>
          <h2 className="font-display text-base font-semibold tracking-tight text-(--color-foreground)">{labels.title}</h2>
          <p className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {labels.monthTotal}: <span className="text-(--color-foreground)">{formatBRL(stats.totalExpense)}</span>
          </p>
        </div>
        <button
          type="button"
          onClick={() => { setEditing(null); setFormOpen(true); }}
          className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) hover:bg-(--color-muted)"
        >
          <Plus className="size-3.5" />
          {labels.add}
        </button>
      </header>

      {categoriesQuery.isError ? (
        <ErrorBanner message={categoriesQuery.error.message} />
      ) : null}

      <section className="rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
        {categoriesQuery.isLoading ? (
          <p className="px-5 py-8 text-center text-[12.5px] text-(--color-muted-foreground)">
            …
          </p>
        ) : ordered.length === 0 ? (
          <p className="px-5 py-8 text-center text-[12.5px] text-(--color-muted-foreground)">
            {labels.empty}
          </p>
        ) : (
          <ul className="divide-y divide-(--color-border)">
            {ordered.map((c) => (
              <CategoryRow
                key={c.id}
                category={c}
                stat={stats.map.get(c.id) ?? { spent: 0, count: 0 }}
                totalExpense={stats.totalExpense}
                onEdit={() => { setEditing(c); setFormOpen(true); }}
                onDelete={() => deleteCategory.mutate(c.id)}
              />
            ))}
          </ul>
        )}
      </section>

      <CategoryForm
        open={formOpen}
        editing={editing}
        onClose={() => setFormOpen(false)}
      />
    </div>
  );
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-[12px] text-rose-700 dark:text-rose-300">
      {message}
    </div>
  );
}

function CategoryRow({
  category,
  stat,
  totalExpense,
  onEdit,
  onDelete,
}: {
  category: Category;
  stat: { spent: number; count: number };
  totalExpense: number;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.categories;
  const isExpense = category.type === "expense";
  const pctOfTotal = isExpense && totalExpense > 0 ? Math.round((stat.spent / totalExpense) * 100) : 0;
  const budget = category.budget ?? null;
  const budgetPct = budget && budget > 0 ? Math.round((stat.spent / budget) * 100) : null;
  const overBudget = budgetPct !== null && budgetPct > 100;

  return (
    <li
      onClick={onEdit}
      className="group flex cursor-pointer items-center gap-3 px-4 py-2.5 transition-colors hover:bg-(--color-muted)/40"
    >
      {/* Icon */}
      <span
        className={cn(
          "flex size-7 shrink-0 items-center justify-center rounded-md",
          `bg-${category.color}-500/10`,
          `text-${category.color}-700 dark:text-${category.color}-300`,
        )}
      >
        <CategoryIcon name={category.icon} className="size-3.5" />
      </span>

      {/* Name + type pill */}
      <div className="min-w-0 flex-1">
        <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">
          {category.name}
          <span className="ml-1.5 font-mono text-[9.5px] font-normal uppercase tracking-[0.16em] text-(--color-muted-foreground)">
            {isExpense ? labels.types.expense : labels.types.income}
          </span>
        </p>
        {isExpense ? (
          <p className="mt-0.5 font-mono text-[10px] text-(--color-muted-foreground)">
            {stat.count} {labels.transactions} · {pctOfTotal}% {labels.ofTotalShort}
          </p>
        ) : (
          <p className="mt-0.5 font-mono text-[10px] text-(--color-muted-foreground)">
            {labels.incomeNoteShort}
          </p>
        )}
      </div>

      {/* Right: spent + budget bar */}
      <div className="flex shrink-0 flex-col items-end gap-1">
        <span className="font-mono text-[12.5px] text-(--color-foreground)">
          {formatBRL(stat.spent)}
        </span>
        {isExpense && budget ? (
          <div className="flex w-28 items-center gap-1.5">
            <div className="h-1 flex-1 overflow-hidden rounded-full bg-(--color-muted)">
              <div
                className={cn("h-full rounded-full", overBudget ? "bg-rose-500" : `bg-${category.color}-500`)}
                style={{ width: `${Math.min(100, budgetPct ?? 0)}%` }}
              />
            </div>
            <span className={cn(
              "shrink-0 font-mono text-[9.5px]",
              overBudget ? "text-rose-600 dark:text-rose-300" : "text-(--color-muted-foreground)",
            )}>
              {budgetPct}%
            </span>
          </div>
        ) : isExpense ? (
          <span className="font-mono text-[9.5px] text-(--color-muted-foreground)/70">
            {labels.noBudget}
          </span>
        ) : null}
      </div>

      {/* Hover delete */}
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onDelete();
        }}
        aria-label={t.app.modules.finance.actions.delete}
        className="flex size-6 shrink-0 items-center justify-center rounded-md text-(--color-muted-foreground) opacity-0 transition-opacity hover:bg-rose-500/10 hover:text-rose-500 group-hover:opacity-100"
      >
        <Trash2 className="size-3" />
      </button>
    </li>
  );
}

/* ── Form ───────────────────────────────────────────────────────────── */

function CategoryForm({
  open,
  editing,
  onClose,
}: {
  open: boolean;
  editing: Category | null;
  onClose: () => void;
}) {
  return (
    <AnimatePresence>
      {open ? (
        <CategoryFormBody
          key={editing?.id ?? "new"}
          editing={editing}
          onClose={onClose}
        />
      ) : null}
    </AnimatePresence>
  );
}

function CategoryFormBody({
  editing,
  onClose,
}: {
  editing: Category | null;
  onClose: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.categories.form;
  const createMut = useCreateCategory();
  const updateMut = useUpdateCategory();

  const [name, setName]   = useState<string>(() => editing?.name ?? "");
  const [type, setType]   = useState<CategoryType>(() => editing?.type ?? "expense");
  const [icon, setIcon]   = useState<string>(() => editing?.icon ?? "tag");
  const [color, setColor] = useState<string>(() => editing?.color ?? "slate");
  const [budgetText, setBudget] = useState<string>(() =>
    editing?.budget ? (editing.budget / 100).toString().replace(".", ",") : "",
  );
  const [submitError, setSubmitError] = useState<string | null>(null);

  const pending = createMut.isPending || updateMut.isPending;

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!name.trim()) return;
    setSubmitError(null);
    const budget = budgetText ? parseBRL(budgetText) : null;
    if (editing) {
      // Backend doesn't allow type changes on update — silently keep current
      // type (the form's `type` toggle is disabled in edit mode).
      updateMut.mutate(
        { id: editing.id, name: name.trim(), icon, color, budget },
        {
          onSuccess: () => onClose(),
          onError: (err) => setSubmitError(err.message),
        },
      );
    } else {
      createMut.mutate(
        { name: name.trim(), type, icon, color, budget },
        {
          onSuccess: () => onClose(),
          onError: (err) => setSubmitError(err.message),
        },
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
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t.app.modules.finance.placeholders.categoryExample} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.type}</Label>
            <div className="flex gap-1">
              <button
                type="button"
                onClick={() => !editing && setType("expense")}
                disabled={!!editing}
                className={togglePill(type === "expense", !!editing)}
              >
                {labels.types.expense}
              </button>
              <button
                type="button"
                onClick={() => !editing && setType("income")}
                disabled={!!editing}
                className={togglePill(type === "income", !!editing)}
              >
                {labels.types.income}
              </button>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.icon}</Label>
            <IconPicker value={icon} onChange={setIcon} />
          </div>
          <div className="space-y-1.5">
            <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.color}</Label>
            <div className="flex flex-wrap gap-1.5">
              {AVAILABLE_COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  onClick={() => setColor(c)}
                  aria-label={c}
                  className={cn("size-6 rounded-md border-2 transition-transform", `bg-${c}-500`, color === c ? "border-(--color-foreground) scale-110" : "border-transparent")}
                />
              ))}
            </div>
          </div>
          {type === "expense" ? (
            <div className="space-y-1.5">
              <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.budget}</Label>
              <Input value={budgetText} onChange={(e) => setBudget(e.target.value)} placeholder="500,00" inputMode="decimal" />
            </div>
          ) : null}
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

function IconPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="flex max-h-32 flex-wrap gap-1 overflow-y-auto rounded-md border border-(--color-border) p-1.5">
      {AVAILABLE_ICONS.map((key) => {
        const active = key === value;
        return (
          <button
            key={key}
            type="button"
            onClick={() => onChange(key)}
            aria-label={key}
            className={cn(
              "flex size-7 items-center justify-center rounded-md border transition-colors",
              active
                ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
                : "border-transparent text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
            )}
          >
            <CategoryIcon name={key} className="size-3.5" />
          </button>
        );
      })}
      <span aria-hidden className="hidden">
        <Tag />
      </span>
    </div>
  );
}

function togglePill(active: boolean, disabled: boolean = false): string {
  return cn(
    "inline-flex flex-1 items-center justify-center rounded-lg border px-2 py-1.5 text-[12px] font-medium transition-colors",
    active
      ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
      : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:text-(--color-foreground)",
    disabled ? "opacity-60 cursor-not-allowed" : "",
  );
}
