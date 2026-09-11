import { useCallback, useSyncExternalStore } from "react";

/**
 * Subscribes to a CSS media query.
 *
 * `useSyncExternalStore` rather than an effect: matchMedia is exactly the
 * external store this hook exists for, and reading it this way avoids the
 * setState-inside-an-effect pattern (and the extra render it costs on
 * every mount).
 *
 * The third argument is the server/prerender snapshot. `false` is the safe
 * default here — panes count up from one, so a wrong guess degrades to the
 * single-column layout rather than to a broken grid.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const mql = window.matchMedia(query);
      mql.addEventListener("change", onChange);
      return () => mql.removeEventListener("change", onChange);
    },
    [query],
  );

  const getSnapshot = useCallback(() => window.matchMedia(query).matches, [query]);

  return useSyncExternalStore(subscribe, getSnapshot, () => false);
}
