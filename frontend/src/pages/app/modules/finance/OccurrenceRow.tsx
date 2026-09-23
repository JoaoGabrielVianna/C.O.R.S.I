import { useEffect, useRef, useState, type FormEvent } from "react";
import { Check, CircleAlert, Loader2, Pencil, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useFormat, useT } from "@/lib/i18n";
import { Input } from "@/components/ui/Input";
import { CategoryIcon } from "./CategoryIcon";
import { formatBRL, parseBRL } from "./format";
import type { ApiOccurrence } from "@/modules/finance/api/monthlyCommitment";

/**
 * One obligation, in one month.
 *
 * ── What a reader must be able to tell without clicking ────────────────
 * Settled or not, which bill it is, when it falls due, how much, whether
 * that amount is confirmed, and whether it is late. All six are text or
 * icon, never colour alone: an amber tint saying "overdue" is invisible to
 * a reader who cannot see amber, and a greyed row saying "paid" is
 * ambiguous with "disabled".
 *
 * ── The control is a button, not a checkbox input ──────────────────────
 * Because the two network operations are MARK PAID and MARK PENDING, not
 * a toggle, and a `<input type=checkbox>` whose checked state is owned by
 * the server round-trips badly: it flips locally on click and then has to
 * be flipped back. A button with `aria-pressed` describes the same binary
 * state, is keyboard-operable for free, and never holds a value of its own.
 *
 * ── Why the amount edit is a form ──────────────────────────────────────
 * So Enter commits and Escape cancels without either being wired by hand,
 * and so the commit and cancel paths are real focusable controls rather
 * than a blur handler somebody has to discover.
 */

type Props = {
  occurrence: ApiOccurrence;
  /** Colour/icon token from the category, when the workspace has one. */
  categoryIcon?: string;
  categoryColor?: string;
  /** False on a projected month: nothing there is a row yet. */
  writable: boolean;
  /** True while this row's own request is in flight. */
  busy: boolean;
  onMarkPaid: () => void;
  onMarkPending: () => void;
  onSetAmount: (cents: number) => void;
};

export function OccurrenceRow({
  occurrence: o,
  categoryIcon,
  categoryColor,
  writable,
  busy,
  onMarkPaid,
  onMarkPending,
  onSetAmount,
}: Props) {
  const t = useT();
  const fmt = useFormat();
  const labels = t.app.modules.finance.recurring.month;
  const [editing, setEditing] = useState(false);

  const paid = o.status === "paid";
  // Both come from the payload. `overdue` is derived server-side against
  // the Finance clock, and recomputing it from Date.now() here would give a
  // different answer in the reader's zone on the day a bill falls due.
  const overdue = o.overdue;
  const color = categoryColor ?? "slate";

  // Editing this month's figure is a write, so it is offered under exactly
  // the conditions a write is allowed: a real row, and not already settled.
  // A settled month is refused by the backend on purpose — see the hint
  // below — and offering the control anyway would be inviting a 409.
  const canEditAmount = writable && !paid;

  const toggleLabel = (paid ? labels.markPending : labels.markPaid)
    .replace("{name}", o.description);

  return (
    <li
      className={cn(
        "flex items-center gap-3 px-3 py-2.5 transition-colors sm:px-4",
        paid ? "opacity-70" : "",
      )}
    >
      <button
        type="button"
        aria-pressed={paid}
        aria-label={toggleLabel}
        title={toggleLabel}
        disabled={!writable || busy}
        onClick={paid ? onMarkPending : onMarkPaid}
        className={cn(
          "flex size-6 shrink-0 items-center justify-center rounded-full border transition-colors",
          "focus:outline-none focus-visible:ring-2 focus-visible:ring-(--color-brand-500)/40",
          paid
            ? "border-(--color-brand-500) bg-(--color-brand-500) text-white"
            : "border-(--color-border) bg-(--color-card) text-transparent hover:border-(--color-brand-500)",
          !writable || busy ? "cursor-not-allowed opacity-60" : "",
        )}
      >
        {busy ? (
          <Loader2 className="size-3 animate-spin text-(--color-muted-foreground)" aria-hidden />
        ) : (
          <Check className="size-3.5" aria-hidden />
        )}
      </button>

      <span
        aria-hidden
        className={cn(
          "hidden size-7 shrink-0 items-center justify-center rounded-md sm:flex",
          `bg-${color}-500/10`,
          `text-${color}-700 dark:text-${color}-300`,
        )}
      >
        <CategoryIcon name={categoryIcon ?? "tag"} className="size-3.5" />
      </span>

      <div className="min-w-0 flex-1">
        <p className="truncate text-[13px] font-medium text-(--color-foreground)">
          {o.description}
        </p>
        <p className="flex flex-wrap items-center gap-x-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-(--color-muted-foreground)">
          {paid ? (
            <span>
              {o.paid_on
                ? labels.paidOn.replace("{date}", fmt.utcDate(o.paid_on, "short"))
                : labels.status.paid}
            </span>
          ) : (
            <span className={overdue ? "font-semibold text-amber-700 dark:text-amber-300" : ""}>
              {overdue ? (
                <CircleAlert className="mr-1 inline size-3 align-[-2px]" aria-hidden />
              ) : null}
              {overdue
                ? labels.status.overdue
                : labels.due.replace("{date}", fmt.utcDate(o.due_on, "short"))}
            </span>
          )}
          {o.category ? <span className="text-(--color-muted-foreground)/70">· {o.category}</span> : null}
        </p>
      </div>

      {editing ? (
        <AmountEditor
          initialCents={o.amount_cents}
          description={o.description}
          onCancel={() => setEditing(false)}
          onCommit={(cents) => {
            setEditing(false);
            onSetAmount(cents);
          }}
        />
      ) : (
        <div className="flex shrink-0 items-center gap-1.5">
          <span className="text-right">
            <span className="block font-mono text-[13px] text-(--color-foreground) tabular-nums">
              {formatBRL(o.amount_cents)}
            </span>
            {/* Said in words, not by a dashed border: "estimated" is a fact
                about the number and has to survive being read aloud. */}
            {o.amount_estimated ? (
              <span className="block font-mono text-[9.5px] uppercase tracking-[0.12em] text-(--color-muted-foreground)">
                {labels.status.estimated}
              </span>
            ) : null}
          </span>
          {canEditAmount ? (
            <button
              type="button"
              onClick={() => setEditing(true)}
              disabled={busy}
              aria-label={labels.amount.editLabel.replace("{name}", o.description)}
              className="flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground) focus:outline-none focus-visible:ring-2 focus-visible:ring-(--color-brand-500)/40"
            >
              <Pencil className="size-3" aria-hidden />
            </button>
          ) : (
            // Keeps the row's right edge aligned whether or not the control
            // is offered, so a settled row does not visibly jump.
            <span className="size-6" aria-hidden />
          )}
        </div>
      )}
    </li>
  );
}

/**
 * The inline amount field.
 *
 * Enter commits and Escape cancels because it is a real form; both paths
 * also have visible, focusable buttons, so the interaction is discoverable
 * for a pointer user and operable for a keyboard one without either having
 * to know the other's convention.
 */
function AmountEditor({
  initialCents,
  description,
  onCommit,
  onCancel,
}: {
  initialCents: number;
  description: string;
  onCommit: (cents: number) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.finance.recurring.month.amount;
  const [text, setText] = useState(() => (initialCents / 100).toFixed(2).replace(".", ","));
  const ref = useRef<HTMLInputElement | null>(null);

  useEffect(() => { ref.current?.focus(); ref.current?.select(); }, []);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const cents = parseBRL(text);
    // A zero or unparseable figure is not a correction, so it cancels
    // rather than sending something the backend would refuse.
    if (cents <= 0) { onCancel(); return; }
    onCommit(cents);
  };

  return (
    <form
      onSubmit={submit}
      onKeyDown={(e) => { if (e.key === "Escape") { e.stopPropagation(); onCancel(); } }}
      className="flex shrink-0 items-center gap-1"
    >
      <Input
        ref={ref}
        value={text}
        onChange={(e) => setText(e.target.value)}
        inputMode="decimal"
        aria-label={labels.editLabel.replace("{name}", description)}
        className="h-8 w-24 text-right font-mono text-[12.5px]"
      />
      <button
        type="submit"
        aria-label={labels.save}
        title={labels.save}
        className="flex size-7 items-center justify-center rounded-md text-(--color-brand-600) hover:bg-(--color-muted) focus:outline-none focus-visible:ring-2 focus-visible:ring-(--color-brand-500)/40 dark:text-(--color-brand-400)"
      >
        <Check className="size-3.5" aria-hidden />
      </button>
      <button
        type="button"
        onClick={onCancel}
        aria-label={labels.cancel}
        title={labels.cancel}
        className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) hover:bg-(--color-muted) focus:outline-none focus-visible:ring-2 focus-visible:ring-(--color-brand-500)/40"
      >
        <X className="size-3.5" aria-hidden />
      </button>
    </form>
  );
}
