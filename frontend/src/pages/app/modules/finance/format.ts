import type { ComponentType, SVGProps } from "react";
import type { Cents as CentsType, Transaction } from "./types";
import {
  Banknote,
  BookOpen,
  Briefcase,
  Building2,
  Car,
  Code2,
  CreditCard,
  Dumbbell,
  Gamepad2,
  Heart,
  Home,
  Landmark,
  Music,
  PawPrint,
  Plug,
  Receipt,
  ShoppingBag,
  Sparkles,
  Sprout,
  Tag,
  TrendingUp,
  Utensils,
  Wallet,
  Wifi,
  Wrench,
  Zap,
} from "lucide-react";
import { activeFormat } from "@/lib/i18n";
import type { Cents, PaymentMethod, TransactionStatus, TransactionSource } from "./types";

/* ── Currency ────────────────────────────────────────────────────────── */

/*
 * These were module constants — `new Intl.NumberFormat("pt-BR", …)`
 * evaluated once at import — so they could not follow a language chosen
 * afterwards, and the hardcoded tag was invisible for exactly that reason.
 * They are functions now, resolving the locale on each call.
 *
 * The currency does not move. An expense recorded in BRL is in BRL for an
 * English reader too; only the grouping and the symbol's placement change.
 */

/** R$ 1.234,56 (pt-BR) / R$ 1,234.56 (en-US), from 123456 cents. */
export function formatBRL(cents: Cents): string {
  return activeFormat().currency(cents / 100);
}

/** Compact, for chart axes. */
export function formatBRLCompact(cents: Cents): string {
  return activeFormat().number(cents / 100, {
    style: "currency",
    currency: "BRL",
    maximumFractionDigits: 0,
    notation: "compact",
  });
}

/** Parse "1234,56" / "1234.56" / "R$ 1.234,56" → 123456 cents. */
export function parseBRL(input: string): Cents {
  const cleaned = input.replace(/[^\d,.-]/g, "");
  if (!cleaned) return 0;
  // Treat last separator as decimal
  const lastDot = cleaned.lastIndexOf(".");
  const lastComma = cleaned.lastIndexOf(",");
  const decimalIdx = Math.max(lastDot, lastComma);
  if (decimalIdx === -1) {
    const n = parseInt(cleaned, 10);
    return Number.isNaN(n) ? 0 : n * 100;
  }
  const intPart = cleaned.slice(0, decimalIdx).replace(/[.,]/g, "");
  const decPart = cleaned.slice(decimalIdx + 1).replace(/\D/g, "").padEnd(2, "0").slice(0, 2);
  const cents = parseInt(intPart || "0", 10) * 100 + parseInt(decPart || "0", 10);
  return Number.isNaN(cents) ? 0 : cents;
}

/* ── Dates ───────────────────────────────────────────────────────────── */

/*
 * `undefined` as the locale does not mean "the app's language"; it means
 * the operating system's. A Brazilian machine reading the UI in English
 * rendered English copy around Portuguese dates, and nothing said so.
 */
export const formatShortDate = (ms: number) => activeFormat().date(ms, "short");
export const formatLongDate  = (ms: number) => activeFormat().date(ms, "long");
export const formatMonthLabel = (monthKey: string) => {
  const [y, m] = monthKey.split("-").map(Number);
  return activeFormat().date(new Date(y, m - 1, 1), "monthYear");
};

export function dateToInputValue(ms: number): string {
  const d = new Date(ms);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

export function inputValueToDate(value: string): number {
  // Treat as local-day midnight to avoid TZ drift.
  const [y, m, d] = value.split("-").map(Number);
  return new Date(y, (m ?? 1) - 1, d ?? 1).getTime();
}

export function startOfMonth(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth(), 1).getTime();
}

export function endOfMonth(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth() + 1, 0, 23, 59, 59, 999).getTime();
}

/** End-of-current-month timestamp. Pure helper so callers aren't tagged by
 *  React's render-purity lint when invoking from event handlers. */
export function endOfCurrentMonth(): number {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth() + 1, 0, 23, 59, 59, 999).getTime();
}

/* ── Icons ───────────────────────────────────────────────────────────── */

const ICONS: Record<string, ComponentType<SVGProps<SVGSVGElement>>> = {
  utensils: Utensils,
  home: Home,
  car: Car,
  heart: Heart,
  gamepad: Gamepad2,
  book: BookOpen,
  sparkles: Sparkles,
  creditcard: CreditCard,
  shopping: ShoppingBag,
  pet: PawPrint,
  briefcase: Briefcase,
  code: Code2,
  trending: TrendingUp,
  music: Music,
  wifi: Wifi,
  zap: Zap,
  wallet: Wallet,
  bank: Landmark,
  building: Building2,
  receipt: Receipt,
  banknote: Banknote,
  tag: Tag,
  plug: Plug,
  dumbbell: Dumbbell,
  sprout: Sprout,
  wrench: Wrench,
};

export function resolveIcon(key: string): ComponentType<SVGProps<SVGSVGElement>> {
  return ICONS[key] ?? Tag;
}

export const AVAILABLE_ICONS: ReadonlyArray<string> = Object.keys(ICONS);

/* ── Colour swatches for categories / cards ──────────────────────────── */

export const AVAILABLE_COLORS: ReadonlyArray<string> = [
  "slate", "gray", "rose", "amber", "yellow", "lime",
  "emerald", "teal", "cyan", "sky", "blue", "indigo",
  "violet", "purple", "fuchsia", "pink",
];

export type ColorToken = string;

/** Tailwind class fragment, e.g. `bg-emerald-500/10`. */
export function colorClasses(token: ColorToken): {
  bg: string;
  fg: string;
  ring: string;
  dot: string;
} {
  return {
    bg:   `bg-${token}-500/10`,
    fg:   `text-${token}-700 dark:text-${token}-300`,
    ring: `ring-${token}-300 dark:ring-${token}-700`,
    dot:  `bg-${token}-500`,
  };
}

/* ── Labels ──────────────────────────────────────────────────────────── */

export const PAYMENT_METHOD_LABEL: Record<PaymentMethod, string> = {
  debit:    "Débito",
  credit:   "Crédito",
  pix:      "Pix",
  cash:     "Dinheiro",
  transfer: "TED",
};

export const PAYMENT_METHOD_LABEL_EN: Record<PaymentMethod, string> = {
  debit:    "Debit",
  credit:   "Credit",
  pix:      "Pix",
  cash:     "Cash",
  transfer: "Transfer",
};

export const STATUS_LABEL: Record<TransactionStatus, string> = {
  paid:      "Pago",
  pending:   "Pendente",
  scheduled: "Agendado",
};

export const STATUS_LABEL_EN: Record<TransactionStatus, string> = {
  paid:      "Paid",
  pending:   "Pending",
  scheduled: "Scheduled",
};

export const SOURCE_LABEL: Record<TransactionSource, string> = {
  manual:   "Manual",
  whatsapp: "WhatsApp",
  import:   "Importação",
  ai:       "Agente",
};

export const SOURCE_LABEL_EN: Record<TransactionSource, string> = {
  manual:   "Manual",
  whatsapp: "WhatsApp",
  import:   "Import",
  ai:       "Agent",
};

/* ── Shared expense math ─────────────────────────────────────────────── */

/**
 * Per-person share of a transaction's amount.
 *
 * Returns:
 *   0 when the person isn't on the hook
 *   amount / splitAmong.length when the transaction is split and the person
 *     appears in `splitAmong`
 *   amount when the person is `personId` and the transaction isn't split
 *
 * Use this in every per-person aggregation so shared expenses are accounted
 * for consistently.
 */
export function personShare(tx: Transaction, personId: string): CentsType {
  const split = tx.splitAmong;
  if (split && split.length >= 2) {
    if (!split.includes(personId)) return 0;
    return Math.round(tx.amount / split.length);
  }
  return tx.personId === personId ? tx.amount : 0;
}

export function isShared(tx: Transaction): boolean {
  return Array.isArray(tx.splitAmong) && tx.splitAmong.length >= 2;
}

/* ── Initials ────────────────────────────────────────────────────────── */

export function initialsFromName(name: string): string {
  return name
    .trim()
    .split(/\s+/)
    .map((p) => p[0] ?? "")
    .slice(0, 2)
    .join("")
    .toUpperCase() || "?";
}

/* ── Tailwind safelist hint ──────────────────────────────────────────────
 *
 * Tailwind's content-scan can miss dynamically composed class names like
 * `bg-${token}-500`. The list below is referenced from the seed so the
 * compiler sees the static literals at build time. Add new colours here
 * when expanding AVAILABLE_COLORS.
 *
 * eslint-disable-next-line @typescript-eslint/no-unused-vars
 */
const TAILWIND_SAFELIST = [
  "bg-slate-500/10",   "bg-gray-500/10",    "bg-rose-500/10",    "bg-amber-500/10",
  "bg-yellow-500/10",  "bg-lime-500/10",    "bg-emerald-500/10", "bg-teal-500/10",
  "bg-cyan-500/10",    "bg-sky-500/10",     "bg-blue-500/10",    "bg-indigo-500/10",
  "bg-violet-500/10",  "bg-purple-500/10",  "bg-fuchsia-500/10", "bg-pink-500/10",
  "text-slate-700",   "text-gray-700",   "text-rose-700",   "text-amber-700",
  "text-yellow-700", "text-lime-700",   "text-emerald-700","text-teal-700",
  "text-cyan-700",   "text-sky-700",    "text-blue-700",   "text-indigo-700",
  "text-violet-700", "text-purple-700", "text-fuchsia-700","text-pink-700",
  "dark:text-slate-300",   "dark:text-gray-300",   "dark:text-rose-300",
  "dark:text-amber-300",   "dark:text-yellow-300", "dark:text-lime-300",
  "dark:text-emerald-300", "dark:text-teal-300",   "dark:text-cyan-300",
  "dark:text-sky-300",     "dark:text-blue-300",   "dark:text-indigo-300",
  "dark:text-violet-300",  "dark:text-purple-300", "dark:text-fuchsia-300",
  "dark:text-pink-300",
  "bg-slate-500", "bg-gray-500", "bg-rose-500", "bg-amber-500",
  "bg-yellow-500", "bg-lime-500", "bg-emerald-500", "bg-teal-500",
  "bg-cyan-500", "bg-sky-500", "bg-blue-500", "bg-indigo-500",
  "bg-violet-500", "bg-purple-500", "bg-fuchsia-500", "bg-pink-500",
];
void TAILWIND_SAFELIST;
