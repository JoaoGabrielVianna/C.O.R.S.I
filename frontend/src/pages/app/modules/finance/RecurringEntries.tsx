import { useEffect, useRef, useState, type FormEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { CalendarClock, MoreHorizontal, Pause, Play, Plus, X } from "lucide-react";
import { MonthlyCommitment } from "./MonthlyCommitment";
import { monthKeyToDate } from "./monthOptions";
import { useMonthlyCommitment } from "@/modules/finance/hooks/useMonthlyCommitment";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { Button } from "@/components/ui/Button";
import { useT, useFormat } from "@/lib/i18n";
import { ApiError } from "@/lib/api/client";
import { useRecurringSummary } from "@/modules/finance/hooks/useRecurringEntries";
import { endOfCurrentMonth, formatBRL, parseBRL } from "./format";
import { CategoryIcon } from "./CategoryIcon";
import type { FinanceStore } from "./store";
import type { RecurringEntry, RecurringFrequency } from "./types";
import { activeFormat } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

/** 1..12. The month NAMES come from Intl, never from a literal list. */
const MONTH_NUMBERS = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12] as const;

type Props = { store: FinanceStore };

/**
 * The Recurring tab.
 *
 * ── The inversion ──────────────────────────────────────────────────────
 * The month comes first and the definitions come second. "What have I not
 * paid yet" is a Tuesday question; "what do I pay every month" is a
 * question somebody asks while setting things up. The tab used to open on
 * the second one and had no answer at all for the first.
 *
 * The form is hoisted to this level so the monthly view's repair prompt —
 * an annual recurrence with no due month — can open the very form that
 * fixes it, without either section reaching into the other.
 */
export function RecurringEntries({ store }: Props) {
  const [editing, setEditing] = useState<RecurringEntry | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  const openForEdit = (entry: RecurringEntry) => {
    setEditing(entry);
    setFormOpen(true);
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-y-auto pb-4">
      <MonthlyCommitment store={store} onEditDefinition={openForEdit} />

      <RecurringDefinitions
        store={store}
        onEdit={openForEdit}
        onCreate={() => { setEditing(null); setFormOpen(true); }}
      />

      <FixedForm
        open={formOpen}
        editing={editing}
        store={store}
        onClose={() => setFormOpen(false)}
      />
    </div>
  );
}

/**
 * The definitions: what repeats, and how it is configured.
 *
 * Reused as it was, demoted to a section under the month. It still owns
 * create, edit, pause, cancel and reactivate — none of which changed.
 */
function RecurringDefinitions({
  store,
  onEdit,
  onCreate,
}: {
  store: FinanceStore;
  onEdit: (entry: RecurringEntry) => void;
  onCreate: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.recurring;

  // ── The row actions need a place to fail visibly too ───────────────
  //
  // Pause, resume, cancel and remove all write, and all of them used to be
  // fire-and-forget. A pause that silently 400s leaves a row the operator
  // believes is paused and the ledger believes is running, which is the
  // same "visually implied success" defect the dialog had — just without a
  // dialog to keep open. One banner above the list, because these actions
  // are the list's and a per-row message would be four states to manage
  // for an error nobody expects to see twice.
  const [actionError, setActionError] = useState<string | null>(null);
  const run = (p: Promise<unknown>) => {
    setActionError(null);
    void p.catch((err) =>
      setActionError(err instanceof ApiError ? err.message : labels.form.saveFailed));
  };

  const items = [...store.state.recurringEntries].sort((a, b) => a.dueDay - b.dueDay);

  // ── Why the total comes from the backend and is not summed here ──────
  // Because summing `amount` is wrong for anything that is not monthly. An
  // annual insurance premium counted at face value overstates the monthly
  // commitment by twelve times its real weight, and the old client-side
  // sum did exactly that. `/finance/fixed-expenses/commitment` normalises
  // annual to a twelfth and excludes paused and ended commitments, using
  // the same predicate the domain applies — and it is the same figure
  // `finance.commitment.get` gives the agent, so the screen and Ledger
  // cannot disagree about what repeats.
  const summary = useRecurringSummary();
  const incomeMonthly  = summary.data?.income_monthly_cents  ?? 0;
  const expenseMonthly = summary.data?.expense_monthly_cents ?? 0;

  return (
    <div className="flex flex-col gap-3 border-t border-(--color-border) pt-4">
      <header className="flex items-center justify-between gap-2">
        <div>
          <h2 className="font-display text-base font-semibold tracking-tight text-(--color-foreground)">{labels.title}</h2>
          <p className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {labels.monthlyIncome}: <span className="text-(--color-foreground)">{formatBRL(incomeMonthly)}</span>
            {" · "}
            {labels.monthlyExpense}: <span className="text-(--color-foreground)">{formatBRL(expenseMonthly)}</span>
          </p>
          {/*
            Said on the screen, not only in a comment: a recurring entry is
            something that repeats, a transaction is money that moved, and
            the two must not be added. An entry already paid this month is
            ALSO a transaction, so summing them counts it twice.
          */}
          <p className="mt-0.5 text-[11.5px] text-(--color-muted-foreground)">
            {labels.commitmentMeaning}
          </p>
        </div>
        <button
          type="button"
          onClick={onCreate}
          className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) hover:bg-(--color-muted)"
        >
          <Plus className="size-3.5" />
          {labels.add}
        </button>
      </header>

      {actionError ? (
        <p
          role="alert"
          className="rounded-xl border border-rose-300 bg-rose-50 px-3 py-2 text-[12px] text-rose-800 dark:border-rose-800 dark:bg-rose-500/10 dark:text-rose-200"
        >
          {actionError}
        </p>
      ) : null}

      <section className="rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
        {items.length === 0 ? (
          <p className="px-5 py-8 text-center text-[12.5px] text-(--color-muted-foreground)">{labels.empty}</p>
        ) : (
          <ul className="divide-y divide-(--color-border)">
            {items.map((fx) => {
              const cat = store.categoriesById.get(fx.categoryId);
              const person = store.peopleById.get(fx.personId);
              const paused = fx.status === "paused";
              const ended = fx.endsAt !== undefined;
              return (
                <li
                  key={fx.id}
                  onClick={() => onEdit(fx)}
                  className={cn(
                    "group flex cursor-pointer items-center gap-3 px-4 py-2.5 transition-colors hover:bg-(--color-muted)/40",
                    (paused || ended) ? "opacity-60" : "",
                  )}
                >
                  <span className={cn("flex size-7 shrink-0 items-center justify-center rounded-md", `bg-${cat?.color ?? "slate"}-500/10`, `text-${cat?.color ?? "slate"}-700 dark:text-${cat?.color ?? "slate"}-300`)}>
                    <CategoryIcon name={cat?.icon ?? "tag"} className="size-3.5" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">
                      {fx.description}
                      {ended ? (
                        <span className="ml-1.5 rounded-sm border border-(--color-border) bg-(--color-muted) px-1 py-px font-mono text-[9px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                          {labels.endedBadge}
                        </span>
                      ) : null}
                    </p>
                    <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                      {labels.dueDay} {fx.dueDay} · {labels.recurrence[fx.recurrence]} · {person?.name ?? "—"}
                      {ended ? ` · ${labels.endedOn} ${activeFormat().date(fx.endsAt!, "long")}` : ""}
                    </p>
                  </div>
                  <span className="font-mono text-[12.5px] text-(--color-foreground)">{formatBRL(fx.amount)}</span>
                  {!ended ? (
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        run(store.updateRecurringEntry(fx.id, { status: paused ? "active" : "paused" }));
                      }}
                      aria-label={paused ? "resume" : "pause"}
                      className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)"
                    >
                      {paused ? <Play className="size-3" /> : <Pause className="size-3" />}
                    </button>
                  ) : null}
                  <CancelMenu fx={fx} store={store} onError={setActionError} />
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <p className="inline-flex items-center gap-1.5 font-mono text-[10px] text-(--color-muted-foreground)/80">
        <CalendarClock className="size-3" />
        {labels.note}
      </p>
    </div>
  );
}

function FixedForm({
  open,
  editing,
  store,
  onClose,
}: {
  open: boolean;
  editing: RecurringEntry | null;
  store: FinanceStore;
  onClose: () => void;
}) {
  return (
    <AnimatePresence>
      {open ? (
        <FixedFormBody
          key={editing?.id ?? "new"}
          editing={editing}
          store={store}
          onClose={onClose}
        />
      ) : null}
    </AnimatePresence>
  );
}

function FixedFormBody({
  editing,
  store,
  onClose,
}: {
  editing: RecurringEntry | null;
  store: FinanceStore;
  onClose: () => void;
}) {
  const t = useT();
  const fmt = useFormat();
  const labels = t.app.modules.finance.recurring.form;

  const [description, setDescription] = useState<string>(() => editing?.description ?? "");
  const [amountText, setAmount]       = useState<string>(() =>
    editing ? (editing.amount / 100).toString().replace(".", ",") : "",
  );
  const [categoryId, setCatId]        = useState<string>(
    () => editing?.categoryId ?? store.state.categories.find((c) => c.type === "expense")?.id ?? "",
  );
  const [personId, setPersonId]       = useState<string>(
    () => editing?.personId ?? (store.state.people.find((p) => p.active) ?? store.state.people[0])?.id ?? "",
  );
  const [dueDay, setDueDay]           = useState<number>(() => editing?.dueDay ?? 1);
  const [recurrence, setRecurrence]   = useState<RecurringFrequency>(() => editing?.recurrence ?? "monthly");
  const [dueMonth, setDueMonth]       = useState<number>(() => editing?.dueMonth ?? 1);
  const [amountVaries, setAmountVaries] = useState<boolean>(() => editing?.amountVaries ?? false);
  const [notes, setNotes]             = useState<string>(() => editing?.notes ?? "");

  /* ── Should this edit also touch the month on screen? ──────────────
   *
   * Offered only when every one of the backend's conditions already
   * holds, because the point of the checkbox is to let the operator say
   * yes — not to let them try and be refused:
   *
   *   · an existing definition, not a new one;
   *   · the selected month IS the current month, decided by the SERVER's
   *     `today` rather than by this browser's clock;
   *   · a materialised occurrence exists for this definition in it;
   *   · and it is still pending.
   *
   * ── Why it defaults OFF ────────────────────────────────────────────
   * F0 suggested defaulting it on when the month was pending. Reading
   * this form decides otherwise: `handleSubmit` sends EVERY field on
   * every save, so it is a full replace and not a patch of what changed.
   * With the box ticked by default, opening the form to fix a typo in the
   * description and pressing Save would also push the definition's amount
   * and due day into the current month — silently overwriting a figure
   * the operator may have confirmed against a real bill and re-marking it
   * as an estimate.
   *
   * An unrelated edit must not move this month's money. So the default is
   * off and ticking it is a deliberate act, which is what it describes.
   */
  const commitment = useMonthlyCommitment(store.state.filters.month);
  const selectedIsCurrentMonth =
    commitment.data ? commitment.data.today.slice(0, 7) === commitment.data.period : false;
  const pendingThisMonth = Boolean(
    editing &&
    selectedIsCurrentMonth &&
    !commitment.data?.is_projection &&
    commitment.data?.items.some(
      (o) => o.recurring_entry_id === editing.id && o.status === "pending",
    ),
  );
  const [applyToPeriod, setApplyToPeriod] = useState(false);

  /* ── Saving, and what happens when saving fails ────────────────────
   *
   * ══════════════════════════════════════════════════════════════════
   *
   *   A CLOSED DIALOG IS A CLAIM THAT SOMETHING WAS SAVED
   *
   * ══════════════════════════════════════════════════════════════════
   *
   * F8 found the failure mode in the field: the backend answered 400, the
   * dialog closed anyway, nothing was said, and the database kept the old
   * values. The operator had every reason to believe the edit landed. It
   * is the same family as the defect the write receipt exists for — a
   * surface implying a change that never happened — with the model
   * replaced by a `.mutate()` nobody awaited.
   *
   * So: await the mutation, and let the OUTCOME decide. Success closes.
   * Failure keeps the dialog open, says what went wrong in the operator's
   * language, and leaves every typed value exactly where it is, because
   * making somebody retype a form is how a retry turns into an
   * abandonment.
   */
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!description.trim() || saving) return;
    const cents = parseBRL(amountText);
    const payload = {
      description: description.trim(),
      amount: cents,
      categoryId,
      personId,
      dueDay,
      recurrence,
      // Carried only for an annual recurrence. A monthly entry falls due
      // every month, and the domain refuses one that also names a month.
      dueMonth: recurrence === "annual" ? dueMonth : undefined,
      amountVaries,
      status: "active" as const,
      startsAt: editing?.startsAt ?? 0,
      endsAt: editing?.endsAt,
      notes: notes.trim(),
    };
    setSaving(true);
    setSaveError(null);
    try {
      if (editing) {
        await store.updateRecurringEntry(
          editing.id,
          payload,
          applyToPeriod && pendingThisMonth ? store.state.filters.month : undefined,
        );
      } else {
        await store.addRecurringEntry(payload);
      }
      // Only here. Persistence is confirmed.
      onClose();
    } catch (err) {
      // The backend's own sentence when it has one: it names the rule that
      // was broken ("a past month is settled history…"), which is far more
      // use than a generic failure. A transport error has no such sentence,
      // so the generic line stands in.
      setSaveError(err instanceof ApiError ? err.message : labels.saveFailed);
    } finally {
      setSaving(false);
    }
  };

  const expenseCategories = store.state.categories.filter((c) => c.type === "expense");

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
                <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.description}</Label>
                <Input value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t.app.modules.finance.placeholders.fixedExample} autoFocus />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.amount}</Label>
                  <Input value={amountText} onChange={(e) => setAmount(e.target.value)} placeholder="500,00" inputMode="decimal" />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.dueDay}</Label>
                  <Input type="number" min={1} max={31} value={dueDay} onChange={(e) => setDueDay(parseInt(e.target.value || "1", 10))} />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.category}</Label>
                  <select
                    value={categoryId}
                    onChange={(e) => setCatId(e.target.value)}
                    className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
                  >
                    {expenseCategories.map((c) => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                  </select>
                </div>
                <div className="space-y-1.5">
                  <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.person}</Label>
                  <select
                    value={personId}
                    onChange={(e) => setPersonId(e.target.value)}
                    className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
                  >
                    {store.state.people.map((p) => (
                      <option key={p.id} value={p.id}>{p.name}</option>
                    ))}
                  </select>
                </div>
              </div>
              <div className="space-y-1.5">
                <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.recurrence}</Label>
                <div className="flex gap-1">
                  <button type="button" onClick={() => setRecurrence("monthly")} className={togglePill(recurrence === "monthly")}>{labels.recurrenceLabels.monthly}</button>
                  <button type="button" onClick={() => setRecurrence("annual")}  className={togglePill(recurrence === "annual")}>{labels.recurrenceLabels.annual}</button>
                </div>
              </div>

              {/*
                WHICH month an annual obligation falls in. Shown only for
                an annual recurrence, because a monthly one falls due every
                month and the domain refuses one that also names a month —
                so the invalid combination is not reachable from this form.

                It is asked rather than inferred from the start date: an
                IPVA written down in September is still due in January, and
                deriving it would file every annual bill in the month of its
                own data entry.
              */}
              {recurrence === "annual" ? (
                <div className="space-y-1.5">
                  <Label htmlFor="recurring-due-month" className="text-[11.5px] text-(--color-muted-foreground)">
                    {labels.dueMonth}
                  </Label>
                  <select
                    id="recurring-due-month"
                    value={dueMonth}
                    onChange={(e) => setDueMonth(parseInt(e.target.value, 10))}
                    className="h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
                  >
                    {MONTH_NUMBERS.map((m) => (
                      <option key={m} value={m}>
                        {fmt.utcDate(new Date(Date.UTC(2000, m - 1, 1)), "monthYear").split(" ")[0]}
                      </option>
                    ))}
                  </select>
                  <p className="text-[11px] text-(--color-muted-foreground)">{labels.dueMonthHint}</p>
                </div>
              ) : null}

              {/*
                The amount stays required either way. What this changes is
                what the amount MEANS: an expected figure rather than a
                settled one, so each month is born marked as an estimate
                until the real bill is confirmed against it.
              */}
              <label className="flex cursor-pointer items-start gap-2.5 rounded-xl border border-(--color-border) bg-(--color-muted)/40 px-3 py-2.5">
                <input
                  type="checkbox"
                  checked={amountVaries}
                  onChange={(e) => setAmountVaries(e.target.checked)}
                  className="mt-0.5 size-3.5 shrink-0 accent-(--color-brand-500)"
                />
                <span className="min-w-0">
                  <span className="block text-[12.5px] font-medium text-(--color-foreground)">
                    {labels.amountVaries}
                  </span>
                  <span className="block text-[11px] text-(--color-muted-foreground)">
                    {amountVaries ? labels.amountVariesOnHint : labels.amountVariesHint}
                  </span>
                </span>
              </label>

              {/*
                "Also update <month>". Offered only when the backend would
                accept it, and OFF by default — see the note beside
                `applyToPeriod` on why this form's full-replace submit makes
                a default-on checkbox a silent rewrite of a confirmed
                monthly figure.
              */}
              {pendingThisMonth ? (
                <label className="flex cursor-pointer items-start gap-2.5 rounded-xl border border-(--color-border) px-3 py-2.5">
                  <input
                    type="checkbox"
                    checked={applyToPeriod}
                    onChange={(e) => setApplyToPeriod(e.target.checked)}
                    className="mt-0.5 size-3.5 shrink-0 accent-(--color-brand-500)"
                  />
                  <span className="min-w-0">
                    <span className="block text-[12.5px] font-medium text-(--color-foreground)">
                      {labels.applyToPeriod.replace(
                        "{month}",
                        fmt.utcDate(monthKeyToDate(store.state.filters.month), "monthYear"),
                      )}
                    </span>
                    <span className="block text-[11px] text-(--color-muted-foreground)">
                      {labels.applyToPeriodHint}
                    </span>
                  </span>
                </label>
              ) : null}
              <div className="space-y-1.5">
                <Label className="text-[11.5px] text-(--color-muted-foreground)">{labels.notes}</Label>
                <textarea
                  value={notes}
                  onChange={(e) => setNotes(e.target.value)}
                  placeholder={labels.notesPlaceholder}
                  rows={2}
                  className="w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] text-(--color-foreground) placeholder:text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none resize-y focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
                />
              </div>
              {saveError ? (
                <p
                  role="alert"
                  className="rounded-xl border border-rose-300 bg-rose-50 px-3 py-2 text-[12px] text-rose-800 dark:border-rose-800 dark:bg-rose-500/10 dark:text-rose-200"
                >
                  {saveError}
                </p>
              ) : null}
              <div className="flex items-center justify-end gap-2 pt-1">
                <Button type="button" variant="ghost" onClick={onClose} disabled={saving}>
                  {t.app.modules.finance.common.cancel}
                </Button>
                <Button type="submit" disabled={saving}>
                  {saving
                    ? labels.saving
                    : editing
                      ? t.app.modules.finance.common.save
                      : t.app.modules.finance.common.create}
                </Button>
              </div>
            </form>
          </motion.div>
        </div>
  );
}

function CancelMenu({
  fx, store, onError,
}: {
  fx: RecurringEntry;
  store: FinanceStore;
  onError: (message: string | null) => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.recurring.cancel;
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Each of the three awaits and reports. Closing the menu is presentation
  // and happens either way; claiming the write landed is not, and only a
  // resolved promise does that.
  const report = (p: Promise<unknown>) => {
    onError(null);
    void p.catch((err) =>
      onError(err instanceof ApiError ? err.message : t.app.modules.finance.recurring.form.saveFailed));
  };
  const handleCancelNow = (e: React.MouseEvent) => {
    e.stopPropagation();
    // Store substitutes Date.now() when `when` is omitted.
    report(store.endRecurringEntry(fx.id));
    setOpen(false);
  };
  const handleCancelEndOfMonth = (e: React.MouseEvent) => {
    e.stopPropagation();
    report(store.endRecurringEntry(fx.id, endOfCurrentMonth()));
    setOpen(false);
  };
  const handleRetroactive = (e: React.MouseEvent) => {
    e.stopPropagation();
    report(store.removeRecurringEntry(fx.id));
    setOpen(false);
  };

  return (
    <div ref={rootRef} className="relative" onClick={(e) => e.stopPropagation()}>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          setOpen((s) => !s);
        }}
        aria-label={t.app.modules.finance.actions.cancelOptions}
        aria-expanded={open}
        className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) opacity-0 transition-opacity hover:bg-(--color-muted) hover:text-(--color-foreground) group-hover:opacity-100"
      >
        <MoreHorizontal className="size-3" />
      </button>
      {open ? (
        <div
          role="menu"
          className="absolute right-0 top-[calc(100%+4px)] z-30 w-64 overflow-hidden rounded-lg border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
        >
          <header className="border-b border-(--color-border) px-3 py-2">
            <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-foreground)">
              {labels.title}
            </p>
            <p className="mt-0.5 text-[11px] text-(--color-muted-foreground)">{labels.hint}</p>
          </header>
          <div className="p-1">
            <button
              type="button"
              role="menuitem"
              onClick={handleCancelNow}
              className="flex w-full flex-col items-start rounded-md px-2.5 py-1.5 text-left hover:bg-(--color-muted)"
            >
              <span className="text-[12px] font-medium text-(--color-foreground)">{labels.now.label}</span>
              <span className="font-mono text-[10px] text-(--color-muted-foreground)">{labels.now.hint}</span>
            </button>
            <button
              type="button"
              role="menuitem"
              onClick={handleCancelEndOfMonth}
              className="flex w-full flex-col items-start rounded-md px-2.5 py-1.5 text-left hover:bg-(--color-muted)"
            >
              <span className="text-[12px] font-medium text-(--color-foreground)">{labels.endOfMonth.label}</span>
              <span className="font-mono text-[10px] text-(--color-muted-foreground)">{labels.endOfMonth.hint}</span>
            </button>
            <button
              type="button"
              role="menuitem"
              onClick={handleRetroactive}
              className="flex w-full flex-col items-start rounded-md px-2.5 py-1.5 text-left hover:bg-rose-500/10"
            >
              <span className="text-[12px] font-medium text-rose-600 dark:text-rose-300">{labels.retroactive.label}</span>
              <span className="font-mono text-[10px] text-(--color-muted-foreground)">{labels.retroactive.hint}</span>
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function togglePill(active: boolean): string {
  return cn(
    "inline-flex flex-1 items-center justify-center rounded-lg border px-2 py-1.5 text-[12px] font-medium transition-colors",
    active
      ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
      : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:text-(--color-foreground)",
  );
}
