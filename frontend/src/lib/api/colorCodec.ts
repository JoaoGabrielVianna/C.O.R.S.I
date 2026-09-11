/**
 * Tailwind-token ↔ hex codec for category color.
 *
 * Frontend stores Tailwind tokens (e.g. "emerald") because className
 * templates rely on them (`bg-${token}-500/10`). The backend stores hex
 * (regex-validated `^#[0-9a-fA-F]{6}$`). This module is the only place
 * that knows the mapping.
 *
 * Tokens come from AVAILABLE_COLORS in `pages/app/modules/finance/format.ts`;
 * hex values are the canonical Tailwind v3/v4 `*-500` shades.
 */

const TOKEN_TO_HEX: Record<string, string> = {
  slate:   "#64748b",
  gray:    "#6b7280",
  rose:    "#f43f5e",
  amber:   "#f59e0b",
  yellow:  "#eab308",
  lime:    "#84cc16",
  emerald: "#10b981",
  teal:    "#14b8a6",
  cyan:    "#06b6d4",
  sky:     "#0ea5e9",
  blue:    "#3b82f6",
  indigo:  "#6366f1",
  violet:  "#8b5cf6",
  purple:  "#a855f7",
  fuchsia: "#d946ef",
  pink:    "#ec4899",
};

const HEX_TO_TOKEN: Record<string, string> = Object.fromEntries(
  Object.entries(TOKEN_TO_HEX).map(([t, h]) => [h, t]),
);

const DEFAULT_TOKEN = "slate";

export function tokenToHex(token: string): string {
  return TOKEN_TO_HEX[token] ?? TOKEN_TO_HEX[DEFAULT_TOKEN];
}

export function hexToToken(hex: string): string {
  return HEX_TO_TOKEN[hex.toLowerCase()] ?? DEFAULT_TOKEN;
}
