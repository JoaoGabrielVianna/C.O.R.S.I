import type { ReactNode } from "react";
import { I18nProvider } from "./I18nProvider";
import { LANG_STORAGE_KEY } from "./detect";
import type { Lang } from "./context";

/**
 * Render helper for component tests: pins the language, then provides it.
 *
 * ── Why pinning matters ────────────────────────────────────────────────
 * Without it, the language a test renders in comes from `navigator.language`,
 * which jsdom reports as `en-US`. A test asserting on Portuguese copy would
 * pass or fail depending on an environment default nobody set deliberately,
 * and would break the day the default moved.
 *
 * Tests that predate i18n queried by the Portuguese strings that were in the
 * JSX. Their subject is behaviour, not language, so they keep those queries
 * and say `lang="pt"` out loud rather than relying on an accident.
 *
 * A test whose subject IS the language should render both and compare — see
 * `i18n.test.tsx`.
 */
export function I18nFixture({
  lang = "pt",
  children,
}: {
  lang?: Lang;
  children: ReactNode;
}) {
  // Written before the provider mounts: `detectInitialLang` reads storage in
  // a `useState` initialiser, so anything set afterwards arrives too late.
  window.localStorage.setItem(LANG_STORAGE_KEY, lang);
  return <I18nProvider>{children}</I18nProvider>;
}
