/**
 * active — the current locale, reachable from code that is not a component.
 *
 * ── Why this exists, and why it is not the normal path ─────────────────
 * `useFormat()` is how a component formats: it reads the context, so a
 * language change re-renders it and the new formatting comes with that
 * render. Nothing here is needed for that case, and nothing here should be
 * used for it.
 *
 * The case it does answer is real, though. Several formatting modules are
 * plain functions imported by dozens of call sites — `finance/format.ts`,
 * `agents/format.ts`, `job-radar/time.ts` — and some of them built their
 * `Intl.*` objects as module constants, evaluated once at import. A module
 * constant cannot follow a language chosen later; those formatters were
 * frozen to whatever the tag said when the bundle loaded, which is exactly
 * why `pt-BR` sat hardcoded in twenty places and was never questioned.
 *
 * ── The caveat, stated plainly ─────────────────────────────────────────
 * Reading this registry does NOT subscribe a component to language changes.
 * A component that calls one of these helpers and nothing else from i18n
 * will keep its old text until something else re-renders it. In practice
 * every screen that formats also translates, so it re-renders through
 * `useT()` — but that is a property of the current code, not a guarantee.
 *
 * When a component can use `useFormat()`, it must.
 */
import type { Lang } from "./context";
import { makeLocaleFormat, type LocaleFormat } from "./locale";

const DEFAULT_LANG: Lang = "en";

let current: LocaleFormat = makeLocaleFormat(DEFAULT_LANG);

/** Called by `I18nProvider` whenever the language changes. */
export function setActiveLocale(lang: Lang) {
  current = makeLocaleFormat(lang);
}

/**
 * The formatters for the language currently on screen.
 *
 * For non-React modules only. In a component, use `useFormat()`.
 */
export function activeFormat(): LocaleFormat {
  return current;
}
