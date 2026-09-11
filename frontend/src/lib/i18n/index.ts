/**
 * i18n — the platform's single translation and locale-formatting capability.
 *
 * Two official languages: EN-US (default for an unknown reader) and PT-BR
 * (the canonical dictionary — new keys are added there first, and `en.ts`
 * is typed against it, so the compiler refuses a half-translated change).
 *
 * Persistence: `localStorage` under `corsi.lang`, resolved synchronously
 * before first paint. Detection: `navigator.language`, PT locales get PT,
 * everything else gets the default.
 *
 * Copy:       `const t = useT();     <p>{t.app.sidebar.modules}</p>`
 * Formatting: `const fmt = useFormat(); <td>{fmt.currency(cents / 100)}</td>`
 *
 * Never format with `Intl.*` or `toLocaleString` at a call site, and never
 * name a locale tag outside `locale.ts`. Two official languages, and a
 * surface is not done while it exists in only one of them.
 */

export { I18nProvider } from "./I18nProvider";
// Test-only render helper. Tree-shaken from the app build: nothing in
// `src/` outside a `*.test.tsx` imports it.
export { I18nFixture } from "./testing";
export { detectInitialLang, DEFAULT_LANG, LANG_STORAGE_KEY } from "./detect";
export { useT, useLang, useFormat } from "./hooks";
export { LOCALE_TAG, BASE_CURRENCY, makeLocaleFormat } from "./locale";
export { deepMerge, resolveDictionary, resetMissingKeyWarnings } from "./fallback";
export { SUPPORTED_LANGS } from "./context";
export { activeFormat, setActiveLocale } from "./active";
export type { Lang } from "./context";
export type { LocaleFormat, DateStyle, Plural } from "./locale";
