import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { pt, type Translations } from "./pt";
import { en } from "./en";
import { I18nContext, type Lang } from "./context";
import { LANG_STORAGE_KEY, detectInitialLang, isLang } from "./detect";
import { LOCALE_TAG, makeLocaleFormat } from "./locale";
import { resolveDictionary } from "./fallback";
import { setActiveLocale } from "./active";

/**
 * The provider composes the capability and owns nothing else: the language
 * decision lives in `detect.ts`, the dictionaries in `pt.ts` / `en.ts`, the
 * formatters in `locale.ts`, and the missing-key behaviour in `fallback.ts`.
 */
const dictionaries: Record<Lang, Translations> = { pt, en };

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(() => {
    const initial = detectInitialLang();
    // Published before the first render, not in an effect: the formatting
    // modules that read it are called *during* that render, and an effect
    // would leave the first paint formatted for the wrong language.
    setActiveLocale(initial);
    return initial;
  });

  useEffect(() => {
    try {
      window.localStorage.setItem(LANG_STORAGE_KEY, lang);
    } catch {
      /* ignore */
    }
    if (typeof document !== "undefined") {
      document.documentElement.lang = LOCALE_TAG[lang];
    }
  }, [lang]);

  const setLang = useCallback((next: Lang) => {
    if (!isLang(next)) return;
    setActiveLocale(next);
    setLangState(next);
  }, []);

  const value = useMemo(
    () => ({
      lang,
      setLang,
      // PT is the canonical schema, so it is also the fallback source: a
      // key present only in PT renders Portuguese rather than vanishing.
      t: resolveDictionary(pt, dictionaries[lang], import.meta.env?.DEV ?? false),
      fmt: makeLocaleFormat(lang),
    }),
    [lang, setLang],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}
