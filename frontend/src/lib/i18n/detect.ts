/**
 * Which language a reader starts in.
 *
 * Its own file rather than sitting in the provider: a module that exports
 * both a component and a function loses fast refresh, and this is also the
 * piece the tests want to exercise on its own — the stored choice, the
 * browser's preference and the default are three distinct decisions.
 *
 * The order is deliberate. A stored choice is the reader having already
 * answered the question, and nothing should overrule it. Only when they
 * have not answered does the browser get a say, and only when the browser
 * says nothing recognisable does the default apply.
 */
import { SUPPORTED_LANGS, type Lang } from "./context";

export const LANG_STORAGE_KEY = "corsi.lang";

/**
 * English for a reader we know nothing about. Portuguese is not a lesser
 * language here — it is the canonical dictionary and the one the operator
 * actually reads — but the product is built in public, and an unidentified
 * visitor is more likely to read English than not.
 *
 * A visitor whose browser says Portuguese still gets Portuguese: detection
 * runs before this default is reached.
 */
export const DEFAULT_LANG: Lang = "en";

export function isLang(value: unknown): value is Lang {
  return typeof value === "string" && (SUPPORTED_LANGS as readonly string[]).includes(value);
}

/**
 * Called from `useState`'s initialiser — synchronously, before the first
 * paint — so the UI never renders one language and then swaps. A flash of
 * the wrong language is not cosmetic: it is the reader watching the product
 * forget who they are.
 */
export function detectInitialLang(): Lang {
  if (typeof window === "undefined") return DEFAULT_LANG;
  try {
    const stored = window.localStorage.getItem(LANG_STORAGE_KEY);
    if (isLang(stored)) return stored;
  } catch {
    /* localStorage can be unavailable: private mode, blocked storage. */
  }
  const browser = window.navigator.language?.toLowerCase() ?? "";
  if (browser.startsWith("pt")) return "pt";
  return DEFAULT_LANG;
}
