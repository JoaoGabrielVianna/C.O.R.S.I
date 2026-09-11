/**
 * Card visual identity — bank + network + variant detection.
 *
 * Used by `<CardPreview />` to render personalized credit-card surfaces from
 * the free-form `name` + optional `brand` fields. No official logos or
 * trademarked assets: bank labels are rendered as plain text, networks as
 * stylized text badges, premium variants as subtle gradient/border tweaks.
 *
 * Add a new bank by appending to `BANK_LIST` and `BANK_PATTERNS`. Detection
 * is substring-based on the lowercased card name; first match wins.
 */

export type BankToken = {
  key: string;
  label: string;
  /** Gradient stops for the card surface. */
  bgFrom: string;
  bgTo: string;
  /** Foreground colour for bank label + last4 + network. */
  fg: string;
  /** Accent for the limit usage bar + variant badge border. */
  accent: string;
};

export type NetworkKey =
  | "visa"
  | "mastercard"
  | "elo"
  | "amex"
  | "hipercard"
  | "diners"
  | "unknown";

export type NetworkToken = {
  key: NetworkKey;
  label: string;
};

export type VariantKey =
  | "infinite"
  | "black"
  | "platinum"
  | "gold"
  | "business"
  | "standard";

export type VariantToken = {
  key: VariantKey;
  label: string;
};

/* ── Bank palette ────────────────────────────────────────────────────── */

const BANK_LIST: BankToken[] = [
  { key: "nubank",    label: "Nubank",    bgFrom: "#1E0438", bgTo: "#820AD1", fg: "#FFFFFF", accent: "#FFD9F0" },
  { key: "inter",     label: "Inter",     bgFrom: "#A23F00", bgTo: "#FF7A1A", fg: "#FFFFFF", accent: "#FFD7B5" },
  { key: "itau",      label: "Itaú",      bgFrom: "#003A6B", bgTo: "#EC7000", fg: "#FFFFFF", accent: "#FFC580" },
  { key: "c6",        label: "C6",        bgFrom: "#0A0A0A", bgTo: "#2B2B2B", fg: "#FFD400", accent: "#FFD400" },
  { key: "bradesco",  label: "Bradesco",  bgFrom: "#660016", bgTo: "#CC092F", fg: "#FFFFFF", accent: "#FFCFCF" },
  { key: "santander", label: "Santander", bgFrom: "#7A0808", bgTo: "#EC0000", fg: "#FFFFFF", accent: "#FFC9C9" },
  { key: "btg",       label: "BTG",       bgFrom: "#040404", bgTo: "#1A1A1A", fg: "#0E80B5", accent: "#0E80B5" },
  { key: "xp",        label: "XP",        bgFrom: "#0A0A0A", bgTo: "#222222", fg: "#FFCD00", accent: "#FFCD00" },
  { key: "will",      label: "Will",      bgFrom: "#5B21B6", bgTo: "#06B6D4", fg: "#FFFFFF", accent: "#A7F3D0" },
  { key: "pagbank",   label: "PagBank",   bgFrom: "#06545A", bgTo: "#1AAEB5", fg: "#FFFFFF", accent: "#A6F4F9" },
  { key: "picpay",    label: "PicPay",    bgFrom: "#0E5B27", bgTo: "#21C25E", fg: "#FFFFFF", accent: "#BBF7D0" },
  { key: "caixa",     label: "Caixa",     bgFrom: "#062E5C", bgTo: "#1058A7", fg: "#FFFFFF", accent: "#FDE047" },
  { key: "bb",        label: "BB",        bgFrom: "#0A2F5C", bgTo: "#0E47A1", fg: "#FFE066", accent: "#FFE066" },
  { key: "mercpago",  label: "Mercado Pago", bgFrom: "#0B3A66", bgTo: "#00B0FF", fg: "#FFFFFF", accent: "#FFF59D" },
  { key: "neon",      label: "Neon",      bgFrom: "#0A2540", bgTo: "#22D3EE", fg: "#FFFFFF", accent: "#22D3EE" },
  { key: "other",     label: "Cartão",    bgFrom: "#1F2937", bgTo: "#374151", fg: "#FFFFFF", accent: "#9CA3AF" },
];

const BANK_PATTERNS: ReadonlyArray<{ pattern: RegExp; key: string }> = [
  { pattern: /nubank|\bnu\b|\brox(a|inho)\b/i, key: "nubank" },
  { pattern: /\binter\b/i,                     key: "inter" },
  { pattern: /ita(u|ú)|personnalit/i,          key: "itau" },
  { pattern: /\bc6\b|c6 ?bank/i,               key: "c6" },
  { pattern: /bradesco/i,                      key: "bradesco" },
  { pattern: /santander/i,                     key: "santander" },
  { pattern: /\bbtg\b/i,                       key: "btg" },
  { pattern: /\bxp\b/i,                        key: "xp" },
  { pattern: /will ?bank|\bwill\b/i,           key: "will" },
  { pattern: /pag ?bank|pagseguro/i,           key: "pagbank" },
  { pattern: /pic ?pay/i,                      key: "picpay" },
  { pattern: /caixa/i,                         key: "caixa" },
  { pattern: /banco do brasil|\bbb\b/i,        key: "bb" },
  { pattern: /mercado ?pago/i,                 key: "mercpago" },
  { pattern: /\bneon\b/i,                      key: "neon" },
];

const OTHER = BANK_LIST[BANK_LIST.length - 1];

export function detectBank(name: string): BankToken {
  const match = BANK_PATTERNS.find((p) => p.pattern.test(name));
  if (!match) return OTHER;
  return BANK_LIST.find((b) => b.key === match.key) ?? OTHER;
}

/* ── Network ─────────────────────────────────────────────────────────── */

export function detectNetwork(name: string, brand?: string): NetworkToken {
  const probe = `${name} ${brand ?? ""}`.toLowerCase();
  if (/mastercard|master\b/.test(probe))            return { key: "mastercard", label: "MASTERCARD" };
  if (/\bvisa\b/.test(probe))                       return { key: "visa",       label: "VISA" };
  if (/amex|american express/.test(probe))          return { key: "amex",       label: "AMEX" };
  if (/\belo\b/.test(probe))                        return { key: "elo",        label: "ELO" };
  if (/hipercard/.test(probe))                      return { key: "hipercard",  label: "HIPERCARD" };
  if (/diners/.test(probe))                         return { key: "diners",     label: "DINERS" };
  return { key: "unknown", label: "" };
}

/* ── Variant ─────────────────────────────────────────────────────────── */

export function detectVariant(name: string): VariantToken {
  const probe = name.toLowerCase();
  if (/infinite/.test(probe))   return { key: "infinite", label: "INFINITE" };
  if (/black/.test(probe))      return { key: "black",    label: "BLACK" };
  if (/platinum/.test(probe))   return { key: "platinum", label: "PLATINUM" };
  if (/personnalit/.test(probe))return { key: "platinum", label: "PERSONNALITÉ" };
  if (/gold/.test(probe))       return { key: "gold",     label: "GOLD" };
  if (/business|empresa/.test(probe)) return { key: "business", label: "BUSINESS" };
  return { key: "standard", label: "" };
}

/* ── Stable mock last-4 ──────────────────────────────────────────────── */

/**
 * Deterministic 4-digit number derived from the card id. Used when a real
 * `last4` isn't recorded yet — keeps the preview stable across reloads.
 */
export function mockLast4(id: string): string {
  let hash = 0;
  for (let i = 0; i < id.length; i++) {
    hash = (hash * 31 + id.charCodeAt(i)) | 0;
  }
  return String(Math.abs(hash) % 10_000).padStart(4, "0");
}
