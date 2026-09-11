import { createContext } from "react";
import type { Translations } from "./pt";
import type { LocaleFormat } from "./locale";

export type Lang = "pt" | "en";

/** The languages the product officially supports. Both must ship together. */
export const SUPPORTED_LANGS = ["en", "pt"] as const satisfies readonly Lang[];

export type I18nContextValue = {
  lang: Lang;
  setLang: (lang: Lang) => void;
  t: Translations;
  /** Locale-bound date/number/currency formatters. See `locale.ts`. */
  fmt: LocaleFormat;
};

export const I18nContext = createContext<I18nContextValue | null>(null);
