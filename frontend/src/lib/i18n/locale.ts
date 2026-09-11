/**
 * locale — the single mapping from an app language to a BCP-47 tag, and the
 * formatters built on it.
 *
 * ── Why this file exists ───────────────────────────────────────────────
 * Before it, the same `lang === "pt" ? "pt-BR" : "en-US"` ternary was
 * written in three places, twenty call sites passed a hardcoded `"pt-BR"`
 * that ignored the user's choice entirely, and a further dozen passed
 * `undefined` — which does not mean "the app locale", it means "whatever
 * the operating system is set to". A Brazilian machine running the UI in
 * English rendered English copy around Portuguese dates, and nothing in
 * the code said so.
 *
 * Presentation is the only thing that changes here. The value does not:
 * a timestamp is the same instant in both languages, and an amount in BRL
 * is the same amount. Only its rendering moves.
 */
import type { Lang } from "./context";

/** BCP-47 tag per supported language. The only place this mapping lives. */
export const LOCALE_TAG: Record<Lang, string> = {
  pt: "pt-BR",
  en: "en-US",
};

/**
 * The currency the platform's money is denominated in.
 *
 * This is a property of the VALUE, not of the reader: switching the UI to
 * English does not convert anyone's expenses to dollars. Only grouping and
 * symbol placement follow the locale — `R$ 1.234,56` vs `R$ 1,234.56`.
 */
export const BASE_CURRENCY = "BRL";

const pluralCache = new Map<string, Intl.PluralRules>();
function memoPlural(tag: string): Intl.PluralRules {
  const hit = pluralCache.get(tag);
  if (hit) return hit;
  const made = new Intl.PluralRules(tag);
  pluralCache.set(tag, made);
  return made;
}

/** Formatters are memoised: `Intl.*` constructors are not cheap in a list. */
const cache = new Map<string, Intl.DateTimeFormat | Intl.NumberFormat>();

function memo<T extends Intl.DateTimeFormat | Intl.NumberFormat>(
  key: string,
  make: () => T,
): T {
  const hit = cache.get(key);
  if (hit) return hit as T;
  const made = make();
  cache.set(key, made);
  return made;
}

/**
 * A count-dependent string. PT and EN happen to share the same two-form
 * shape, but the categories are resolved by `Intl.PluralRules` rather than
 * by `count === 1`, so a language with different rules does not silently
 * get the wrong branch if one is ever added.
 */
export type Plural = { one: string; other: string };

export type DateStyle = "short" | "long" | "monthYear" | "dayMonth" | "dateTime";

const DATE_OPTIONS: Record<DateStyle, Intl.DateTimeFormatOptions> = {
  short:     { day: "2-digit", month: "short" },
  long:      { day: "2-digit", month: "short", year: "numeric" },
  monthYear: { month: "long", year: "numeric" },
  dayMonth:  { day: "2-digit", month: "2-digit", year: "2-digit" },
  dateTime:  { day: "2-digit", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" },
};

/**
 * Formatters bound to one language. Built once per language change by the
 * provider, so components never reach for `Intl` directly and can never
 * drift back to a hardcoded tag.
 */
export type LocaleFormat = {
  lang: Lang;
  /** The BCP-47 tag, for the rare call that needs to pass it onward. */
  tag: string;
  /** `null`/invalid input renders the em dash rather than "Invalid Date". */
  date: (value: string | number | Date | null | undefined, style?: DateStyle) => string;
  /** Same as `date`, forced to UTC — for facts that are a day, not a moment. */
  utcDate: (value: string | number | Date | null | undefined, style?: DateStyle) => string;
  number: (value: number | null | undefined, opts?: Intl.NumberFormatOptions) => string;
  currency: (value: number | null | undefined, currency?: string) => string;
  percent: (value: number | null | undefined, fractionDigits?: number) => string;
  /**
   * Pick the plural form for `count` and substitute it for `{count}`.
   * `fmt.plural(3, t.app.modules.agents.common.conversationCount)`
   */
  plural: (count: number, forms: Plural) => string;
};

const EMPTY = "—";

function toDate(value: string | number | Date | null | undefined): Date | null {
  if (value === null || value === undefined || value === "") return null;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

export function makeLocaleFormat(lang: Lang): LocaleFormat {
  const tag = LOCALE_TAG[lang];

  const date = (value: Parameters<LocaleFormat["date"]>[0], style: DateStyle = "long") => {
    const d = toDate(value);
    if (!d) return EMPTY;
    return memo(`d:${tag}:${style}`, () =>
      new Intl.DateTimeFormat(tag, DATE_OPTIONS[style]),
    ).format(d);
  };

  const utcDate = (value: Parameters<LocaleFormat["date"]>[0], style: DateStyle = "long") => {
    const d = toDate(value);
    if (!d) return EMPTY;
    return memo(`u:${tag}:${style}`, () =>
      new Intl.DateTimeFormat(tag, { ...DATE_OPTIONS[style], timeZone: "UTC" }),
    ).format(d);
  };

  const number = (value: number | null | undefined, opts?: Intl.NumberFormatOptions) => {
    if (value === null || value === undefined || Number.isNaN(value)) return EMPTY;
    const key = `n:${tag}:${opts ? JSON.stringify(opts) : ""}`;
    return memo(key, () => new Intl.NumberFormat(tag, opts)).format(value);
  };

  const currency = (value: number | null | undefined, code: string = BASE_CURRENCY) => {
    if (value === null || value === undefined || Number.isNaN(value)) return EMPTY;
    return memo(`c:${tag}:${code}`, () =>
      new Intl.NumberFormat(tag, {
        style: "currency",
        currency: code,
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      }),
    ).format(value);
  };

  const percent = (value: number | null | undefined, fractionDigits = 0) => {
    if (value === null || value === undefined || Number.isNaN(value)) return EMPTY;
    return memo(`p:${tag}:${fractionDigits}`, () =>
      new Intl.NumberFormat(tag, {
        style: "percent",
        minimumFractionDigits: fractionDigits,
        maximumFractionDigits: fractionDigits,
      }),
    ).format(value);
  };

  const plural = (count: number, forms: Plural) => {
    const rules = memoPlural(tag);
    const form = rules.select(count) === "one" ? forms.one : forms.other;
    return form.replace("{count}", number(count));
  };

  return { lang, tag, date, utcDate, number, currency, percent, plural };
}
