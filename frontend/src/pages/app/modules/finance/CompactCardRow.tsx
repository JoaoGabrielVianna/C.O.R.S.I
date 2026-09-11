import { useMemo } from "react";
import { ArrowDownRight, ArrowUpRight, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { detectBank, detectNetwork, detectVariant, mockLast4 } from "./cards";
import { formatBRL } from "./format";
import type { Cents, CreditCard, Person, Transaction } from "./types";
import { useT } from "@/lib/i18n";

type Props = {
  card: CreditCard;
  transactions: Transaction[];
  owner: Person | undefined;
  onOpen: () => void;
  onDelete: () => void;
};

/**
 * CompactCardRow — operational scan row, multi-person aware.
 *
 *   ┃ [JC] Nubank Black            [DUE 4d]    R$ 1.245  ↑R$ 145
 *   ┃ ━━━━━━━━━━━━░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ 8%
 *   ┃ •••• 4271 · MASTERCARD                  Fecha 20 · Vence 27
 *
 * Status chip only renders when the card needs attention (OVERDUE, DUE Xd).
 * OPEN / CLOSED states are intentionally silent — no chip noise.
 *
 * Trend shows the absolute Δ in BRL with a colour-coded arrow; hover for the
 * % comparison + cycle range.
 */
export function CompactCardRow({ card, transactions, owner, onOpen, onDelete }: Props) {
  const t = useT();
  const bank = detectBank(card.name);
  const network = detectNetwork(card.name, card.brand);
  const variant = detectVariant(card.name);
  const digits = card.last4 ?? mockLast4(card.id);

  const analysis = useMemo(() => analyseCard(card, transactions), [card, transactions]);
  const { used, cur, prev, status, daysUntilDue } = analysis;

  const usedPct = card.limit > 0 ? Math.min(100, Math.round((used / card.limit) * 100)) : 0;
  const trend = computeTrend(cur, prev);

  return (
    <article className="group relative overflow-hidden rounded-xl border border-(--color-border) bg-(--color-card) transition-colors hover:border-(--color-border-strong) hover:bg-(--color-muted)/40">
      {/* Bank colour stripe */}
      <span
        aria-hidden
        className="absolute inset-y-0 left-0 w-1"
        style={{ backgroundImage: `linear-gradient(180deg, ${bank.bgFrom}, ${bank.bgTo})` }}
      />

      <button
        type="button"
        onClick={onOpen}
        className="block w-full cursor-pointer pl-4 pr-3 pt-3 pb-2.5 text-left"
      >
        {/* Top line · owner · bank · status · invoice + trend */}
        <div className="flex items-center gap-2">
          {owner ? (
            <span
              className="flex size-5 shrink-0 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) font-mono text-[9.5px] font-semibold text-(--color-foreground)"
              title={owner.name}
            >
              {owner.initials}
            </span>
          ) : null}
          <p className="truncate text-[13.5px] font-semibold text-(--color-foreground)">
            {bank.label}
            {variant.label ? (
              <span className="ml-1.5 font-mono text-[9.5px] font-normal uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                {variant.label}
              </span>
            ) : null}
          </p>

          {/* Status chip — only renders when there's something to act on */}
          {status === "overdue" || status === "due-critical" || status === "due-soon" ? (
            <StatusChip status={status} daysUntilDue={daysUntilDue} />
          ) : null}

          <div className="ml-auto flex shrink-0 items-baseline gap-2">
            <span className="font-display text-[14px] font-semibold tracking-tight text-(--color-foreground)">
              {formatBRL(used)}
            </span>
            {trend ? (
              <span
                className={cn(
                  "inline-flex items-center gap-0.5 font-mono text-[10.5px]",
                  trend.direction === "up"
                    ? "text-rose-600 dark:text-rose-300"
                    : "text-emerald-600 dark:text-emerald-300",
                )}
                title={trend.title}
              >
                {trend.direction === "up" ? <ArrowUpRight className="size-2.5" /> : <ArrowDownRight className="size-2.5" />}
                {trend.label}
              </span>
            ) : null}
          </div>
        </div>

        {/* Usage bar */}
        <div className="mt-2 flex items-center gap-2">
          <div className="h-1 flex-1 overflow-hidden rounded-full bg-(--color-muted)">
            <div
              className="h-full rounded-full transition-[width] duration-300"
              style={{
                width: `${usedPct}%`,
                backgroundImage: `linear-gradient(90deg, ${bank.bgFrom}, ${bank.bgTo})`,
              }}
            />
          </div>
          <span className="shrink-0 font-mono text-[10px] text-(--color-muted-foreground)">
            {usedPct}%
          </span>
        </div>

        {/* Bottom line · supporting context */}
        <div className="mt-1.5 flex items-center gap-2 font-mono text-[10.5px] text-(--color-muted-foreground)">
          <span className="truncate">
            •••• {digits}
            {network.label ? (
              <>
                <span className="mx-1.5 text-(--color-muted-foreground)/60">·</span>
                <span className="uppercase tracking-wide">{network.label}</span>
              </>
            ) : null}
          </span>
          <span className="ml-auto shrink-0">
            {t.app.modules.finance.plans.closesDue
              .replace("{closing}", String(card.closingDay))
              .replace("{due}", String(card.dueDay))}
          </span>
        </div>
      </button>

      {/* Hover delete affordance */}
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onDelete();
        }}
        aria-label={t.app.modules.finance.actions.delete}
        className="absolute right-2 top-2 flex size-6 items-center justify-center rounded-md text-(--color-muted-foreground) opacity-0 transition-opacity hover:bg-rose-500/10 hover:text-rose-500 group-hover:opacity-100"
      >
        <Trash2 className="size-3" />
      </button>
    </article>
  );
}

/* ── Status chip ─────────────────────────────────────────────────────── */

type CardStatus = "overdue" | "due-critical" | "due-soon" | "closed" | "open";

function StatusChip({ status, daysUntilDue }: { status: CardStatus; daysUntilDue: number }) {
  const tone =
    status === "overdue" || status === "due-critical"
      ? "border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-700 dark:bg-rose-500/10 dark:text-rose-300"
      : "border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-500/10 dark:text-amber-300";

  const label =
    status === "overdue" ? `OVERDUE ${Math.abs(daysUntilDue)}d`
    : `DUE ${daysUntilDue}d`;

  return (
    <span className={cn("rounded-full border px-1.5 py-px font-mono text-[9px] uppercase tracking-[0.16em]", tone)}>
      {label}
    </span>
  );
}

/* ── Analysis ────────────────────────────────────────────────────────── */

function analyseCard(card: CreditCard, txs: Transaction[]) {
  const today = startOfDay(new Date());
  const todayMs = today.getTime();

  // Current cycle: last close → next close
  const curStart = new Date(today.getFullYear(), today.getMonth() - 1, card.closingDay).getTime();
  const curEnd   = new Date(today.getFullYear(), today.getMonth(),     card.closingDay - 1, 23, 59, 59, 999).getTime();
  // Previous cycle (for trend)
  const prevStart = new Date(today.getFullYear(), today.getMonth() - 2, card.closingDay).getTime();
  const prevEnd   = new Date(today.getFullYear(), today.getMonth() - 1, card.closingDay - 1, 23, 59, 59, 999).getTime();

  // Limit consumption = sum of unpaid credit transactions on this card.
  // Cycle-bound totals (`cur`, `prev`) are kept only to derive the period-
  // over-period trend chip; they are not what the issuer has locked against
  // the limit.
  let used: Cents = 0;
  let cur: Cents = 0;
  let prev: Cents = 0;
  for (const tx of txs) {
    if (tx.paymentMethod !== "credit" || tx.accountId !== card.id) continue;
    if (tx.status !== "paid") used += tx.amount;
    if (tx.date >= curStart && tx.date <= curEnd) cur += tx.amount;
    else if (tx.date >= prevStart && tx.date <= prevEnd) prev += tx.amount;
  }

  // Next due date
  const thisMonthDue = new Date(today.getFullYear(), today.getMonth(), card.dueDay).getTime();
  const nextDue = thisMonthDue >= todayMs
    ? thisMonthDue
    : new Date(today.getFullYear(), today.getMonth() + 1, card.dueDay).getTime();
  const daysUntilDue = Math.round((nextDue - todayMs) / 86_400_000);

  // Status derivation
  const todayDay = today.getDate();
  let status: CardStatus;
  if (daysUntilDue < 0)         status = "overdue";
  else if (daysUntilDue <= 3)   status = "due-critical";
  else if (daysUntilDue <= 7)   status = "due-soon";
  else if (todayDay > card.closingDay) status = "closed";
  else                          status = "open";

  return { used, cur, prev, status, daysUntilDue };
}

function computeTrend(current: Cents, prev: Cents) {
  if (prev <= 0) return null;
  const deltaCents = current - prev;
  const pct = (deltaCents / prev) * 100;
  if (Math.abs(pct) < 1) return null;
  const direction = deltaCents >= 0 ? "up" : "down";
  return {
    direction,
    label: formatBRL(Math.abs(deltaCents)),
    title: `${direction === "up" ? "+" : "−"}${Math.abs(Math.round(pct))}% · ${formatBRL(prev)} → ${formatBRL(current)}`,
  } as const;
}

function startOfDay(d: Date): Date {
  const c = new Date(d);
  c.setHours(0, 0, 0, 0);
  return c;
}
