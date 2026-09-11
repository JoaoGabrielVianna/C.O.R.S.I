import { useContext } from "react";
import type { Translations } from "./pt";
import type { LocaleFormat } from "./locale";
import { I18nContext, type Lang } from "./context";

function useI18n() {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error(
      "useI18n / useT / useLang / useFormat must be used inside <I18nProvider>",
    );
  }
  return ctx;
}

/** The full translation tree, so every key is checked at the call site. */
export function useT(): Translations {
  return useI18n().t;
}

export function useLang(): [Lang, (lang: Lang) => void] {
  const { lang, setLang } = useI18n();
  return [lang, setLang];
}

/**
 * Locale-bound formatters for dates, numbers, percentages and money.
 *
 * Use this instead of `Intl.*` or `toLocaleString` directly. A call site
 * that names its own locale tag either ignores the reader's choice or
 * follows the operating system, and both were real bugs here.
 */
export function useFormat(): LocaleFormat {
  return useI18n().fmt;
}
