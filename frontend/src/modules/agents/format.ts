import { activeFormat } from "@/lib/i18n";

/**
 * Money and token formatting for the Agents module.
 *
 * Costs here run from cents to fractions of a cent, where a fixed 2-decimal
 * currency format collapses everything to "$0.00". Below a cent we widen to
 * significant digits so a real number still shows.
 */

export function formatUSD(value: number): string {
  if (value === 0) return "$0.00";
  if (value < 0.01) return `$${value.toPrecision(2)}`;
  return `$${value.toFixed(2)}`;
}

/**
 * The amount is in BRL in both languages — switching the UI to English does
 * not convert anyone's spend to dollars. Only the grouping and the symbol's
 * placement follow the reader: `R$ 1.234,56` against `R$ 1,234.56`.
 */
export function formatBRL(value: number): string {
  const max = value !== 0 && value < 1 ? 4 : 2;
  return activeFormat().number(value, {
    style: "currency",
    currency: "BRL",
    minimumFractionDigits: 2,
    maximumFractionDigits: max,
  });
}

/**
 * Token counts stay in code units (`k`, `M`) rather than becoming words:
 * they are a technical magnitude, read the same way in both languages.
 */
export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}
