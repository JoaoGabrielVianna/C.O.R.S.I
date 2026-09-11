import { useMemo, useState } from "react";
import {
  ArrowDownLeft,
  ArrowUpRight,
  CalendarClock,
  PiggyBank,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import { LineChart } from "./FinanceCharts";
import { formatBRL, formatBRLCompact, formatShortDate } from "./format";
import { CategoryIcon } from "./CategoryIcon";
import type { FinanceStore } from "./store";
import type { Cents } from "./types";

type Props = { store: FinanceStore };

/**
 * Projection — what's coming, when.
 *
 * Daily cumulative balance for the next 30 / 60 / 90 days, derived from:
 *   · already-paid transactions (anchor)
 *   · scheduled transactions
 *   · active fixed expenses (projected at `dueDay`)
 *
 * No fabricated forecasts beyond simple arithmetic on records the user
 * already created.
 */
export function Projection({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.finance.projection;
  const [now] = useState(() => Date.now());

  const { points90, expectedIncome, expectedExpense, scheduledList, upcoming } = useMemo(
    () => buildProjection(store, now, 90),
    [store, now],
  );

  const points30 = points90.slice(0, 30);
  const points60 = points90.slice(0, 60);

  const tiles = [
    { key: "income",   label: labels.tiles.expectedIncome,  value: formatBRL(expectedIncome),  icon: ArrowDownLeft, tone: "emerald" },
    { key: "expense",  label: labels.tiles.expectedExpense, value: formatBRL(expectedExpense), icon: ArrowUpRight, tone: "rose" },
    { key: "fixed",    label: labels.tiles.recurring,           value: formatBRL(activeRecurringNet(store)),  icon: CalendarClock, tone: "amber" },
    { key: "eom",      label: labels.tiles.eom,             value: formatBRL(points30[points30.length - 1]?.y ?? 0), icon: PiggyBank, tone: (points30[points30.length - 1]?.y ?? 0) >= 0 ? "sky" : "rose" },
  ];

  return (
    <div className="flex flex-col gap-3 overflow-y-auto pb-2">
      <header>
        <h2 className="font-display text-base font-semibold tracking-tight text-(--color-foreground)">{labels.title}</h2>
        <p className="text-[12px] text-(--color-muted-foreground)">{labels.description}</p>
      </header>

      <section className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(200px,1fr))]">
        {tiles.map((tile) => (
          <div key={tile.key} className="rounded-2xl border border-(--color-border) bg-(--color-card) p-3.5 shadow-(--shadow-soft)">
            <div className="flex items-center justify-between">
              <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">{tile.label}</span>
              <span className={cn("flex size-6 items-center justify-center rounded-md", `bg-${tile.tone}-500/10`, `text-${tile.tone}-700 dark:text-${tile.tone}-300`)}>
                <tile.icon className="size-3" />
              </span>
            </div>
            <p className="mt-1.5 font-display text-lg font-semibold tracking-tight text-(--color-foreground)">{tile.value}</p>
          </div>
        ))}
      </section>

      <section className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-soft)">
        <header className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">{labels.cashflow.title}</p>
          <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{labels.cashflow.hint}</p>
        </header>
        <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(280px,1fr))]">
          <Window label="30d" points={points30} />
          <Window label="60d" points={points60} />
          <Window label="90d" points={points90} />
        </div>
      </section>

      <section className="rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
        <header className="flex items-baseline justify-between gap-3 border-b border-(--color-border) px-5 py-3">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">{labels.upcoming.title}</p>
          <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">{upcoming.length} {labels.upcoming.entries}</p>
        </header>
        {upcoming.length === 0 ? (
          <p className="px-5 py-8 text-center text-[12.5px] text-(--color-muted-foreground)">{labels.upcoming.empty}</p>
        ) : (
          <ul className="divide-y divide-(--color-border)">
            {upcoming.map((entry) => {
              const cat = store.categoriesById.get(entry.categoryId ?? "");
              const person = store.peopleById.get(entry.personId);
              return (
                <li key={entry.id} className="flex items-center gap-3 px-5 py-2.5">
                  <span className={cn("flex size-7 shrink-0 items-center justify-center rounded-md", `bg-${cat?.color ?? "slate"}-500/10`, `text-${cat?.color ?? "slate"}-700 dark:text-${cat?.color ?? "slate"}-300`)}>
                    <CategoryIcon name={cat?.icon ?? "tag"} className="size-3.5" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">{entry.description}</p>
                    <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                      {formatShortDate(entry.date)} · {person?.name ?? "—"} · {labels.kinds[entry.kind]}
                    </p>
                  </div>
                  <span className={cn("font-mono text-[12.5px]", entry.delta < 0 ? "text-rose-700 dark:text-rose-300" : "text-emerald-700 dark:text-emerald-300")}>
                    {entry.delta < 0 ? "−" : "+"}{formatBRL(Math.abs(entry.delta))}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {scheduledList.length === 0 ? null : (
        <p className="font-mono text-[10px] text-(--color-muted-foreground)/80">
          {labels.note.replace("{{n}}", String(scheduledList.length))}
        </p>
      )}
    </div>
  );
}

function Window({ label, points }: { label: string; points: { x: string; y: number }[] }) {
  return (
    <div className="rounded-xl border border-(--color-border) bg-(--color-background)/40 p-3">
      <div className="flex items-baseline justify-between gap-2">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-foreground)">{label}</p>
        <p className="font-mono text-[10.5px] text-(--color-foreground)">
          {formatBRL(points[points.length - 1]?.y ?? 0)}
        </p>
      </div>
      <div className="mt-2">
        <LineChart points={points} format={(v) => formatBRLCompact(v)} />
      </div>
    </div>
  );
}

// ── Projection math ─────────────────────────────────────────────────────

type UpcomingEntry = {
  id: string;
  description: string;
  categoryId?: string;
  personId: string;
  date: number;
  delta: Cents;        // signed: + income, − expense
  kind: "scheduled" | "recurring";
};

function buildProjection(store: FinanceStore, now: number, horizonDays: number) {
  const day = 24 * 60 * 60 * 1000;
  const today = atMidnight(now);

  // 1. Starting balance = sum of paid transactions
  let balance = 0;
  for (const tx of store.state.transactions) {
    if (tx.status !== "paid") continue;
    if (tx.type === "transfer") continue;
    balance += tx.type === "income" ? tx.amount : -tx.amount;
  }

  // 2. Build a per-day delta map from scheduled transactions and active fixed expenses.
  const deltas = new Map<number, Cents>();
  const upcoming: UpcomingEntry[] = [];

  const addDelta = (date: number, value: Cents) => {
    const key = atMidnight(date);
    deltas.set(key, (deltas.get(key) ?? 0) + value);
  };

  // Scheduled txs within horizon
  for (const tx of store.state.transactions) {
    if (tx.status !== "scheduled") continue;
    if (tx.type === "transfer") continue;
    if (tx.date < today || tx.date > today + horizonDays * day) continue;
    const delta = tx.type === "income" ? tx.amount : -tx.amount;
    addDelta(tx.date, delta);
    upcoming.push({
      id: tx.id,
      description: tx.description || "—",
      categoryId: tx.categoryId,
      personId: tx.personId,
      date: tx.date,
      delta,
      kind: "scheduled",
    });
  }

  // Fixed expenses across next N months (projected to dueDay each month).
  // Skip entirely only when the recurrence is paused with no explicit end —
  // a cancelled recurrence (`paused` + `endsAt`) must still project its
  // residual occurrences up to the cutoff, so "cancel end-of-month" keeps
  // a same-month charge whose dueDay is still ahead of today.
  const months = Math.ceil(horizonDays / 30) + 1;
  for (const fx of store.state.recurringEntries) {
    if (fx.status !== "active" && fx.endsAt === undefined) continue;
    // ── The direction comes from the category, never from a sign ────────
    // This loop used to subtract every recurrence unconditionally, which
    // was right while the concept could only hold expenses. It now holds a
    // salary just as readily, and subtracting one would project a person
    // into poverty every month they get paid.
    const direction = store.categoriesById.get(fx.categoryId)?.type ?? "expense";
    const signed = direction === "income" ? fx.amount : -fx.amount;
    for (let i = 0; i < months; i++) {
      const ref = new Date(today);
      ref.setMonth(ref.getMonth() + i, fx.dueDay);
      ref.setHours(0, 0, 0, 0);
      const t = ref.getTime();
      if (t < today || t > today + horizonDays * day) continue;
      if (fx.endsAt !== undefined && t > fx.endsAt) continue;
      addDelta(t, signed);
      upcoming.push({
        id: `${fx.id}_${i}`,
        description: fx.description,
        categoryId: fx.categoryId,
        personId: fx.personId,
        date: t,
        delta: signed,
        kind: "recurring",
      });
    }
  }

  upcoming.sort((a, b) => a.date - b.date);

  // 3. Walk forward computing running balance per day
  const points: { x: string; y: number }[] = [];
  let running = balance;
  for (let i = 0; i < horizonDays; i++) {
    const t = today + i * day;
    running += deltas.get(t) ?? 0;
    const d = new Date(t);
    points.push({ x: `${d.getDate()}/${d.getMonth() + 1}`, y: running });
  }

  const expectedIncome = upcoming.filter((u) => u.delta > 0).reduce((a, b) => a + b.delta, 0);
  const expectedExpense = upcoming.filter((u) => u.delta < 0).reduce((a, b) => a + Math.abs(b.delta), 0);
  const scheduledList = upcoming.filter((u) => u.kind === "scheduled");

  return {
    points90: points,
    expectedIncome,
    expectedExpense,
    upcoming: upcoming.slice(0, 12),
    scheduledList,
  };
}

/**
 * The net monthly effect of everything recurring: income minus expense.
 *
 * Signed by category, for the reason the projection loop gives. A raw sum
 * of `amount` was correct only while the concept was expense-only.
 */
function activeRecurringNet(store: FinanceStore): Cents {
  return store.state.recurringEntries
    .filter((fx) => fx.status === "active")
    .reduce((acc, fx) => {
      const direction = store.categoriesById.get(fx.categoryId)?.type ?? "expense";
      return acc + (direction === "income" ? fx.amount : -fx.amount);
    }, 0);
}

function atMidnight(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}
