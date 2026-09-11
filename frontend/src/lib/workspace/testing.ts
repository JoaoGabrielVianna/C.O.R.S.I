/**
 * Viewport control for tests. Not imported by anything in the app build.
 *
 * ── Why this exists ────────────────────────────────────────────────────
 * jsdom 29 ships no `window.matchMedia` at all, so any component that asks
 * the viewport a question dies on `matchMedia is not a function` before it
 * can render. The usual workaround is a stub hardcoded to `matches: false`,
 * which quietly pins every test to "not desktop" — the exact state the
 * responsive bug lived in, and therefore the worst possible default.
 *
 * So this takes a width instead of a boolean and answers `min-width` and
 * `max-width` queries against it. A test says which viewport it means, and
 * the answer for 1440px and the answer for 390px come from the same place.
 *
 * ── What it does NOT do ────────────────────────────────────────────────
 * jsdom performs no layout: nothing here makes an element have a width, a
 * position or a visible box. These widths drive media queries and nothing
 * else, so a test can prove which branch rendered but never that a panel
 * fits on screen. Geometry stays unproven without a real browser.
 */

/** The viewports this project checks against. */
export const VIEWPORTS = {
  desktopWide: 1440,
  desktop: 1024,
  tablet: 768,
  phone: 390,
} as const;

type Listener = (event: MediaQueryListEvent) => void;

type LiveQuery = {
  query: string;
  matches: boolean;
  listeners: Set<Listener>;
};

let width: number = VIEWPORTS.desktopWide;
let live: LiveQuery[] = [];

/**
 * Handles the two forms the shell actually uses. An unrecognised query
 * throws rather than silently answering `false`, because a media query the
 * stub cannot parse is a test that proves nothing.
 */
function evaluate(query: string, px: number): boolean {
  const min = /\(min-width:\s*(\d+)px\)/.exec(query);
  if (min) return px >= Number(min[1]);
  const max = /\(max-width:\s*(\d+)px\)/.exec(query);
  if (max) return px <= Number(max[1]);
  throw new Error(`stubViewport: unsupported media query ${query}`);
}

/**
 * Installs the stub and sets the viewport width. Call in `beforeEach`.
 */
export function stubViewport(px: number) {
  width = px;
  live = [];

  Object.defineProperty(window, "innerWidth", {
    writable: true,
    configurable: true,
    value: px,
  });

  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => {
      const entry: LiveQuery = {
        query,
        matches: evaluate(query, width),
        listeners: new Set(),
      };
      live.push(entry);
      return {
        get matches() {
          return entry.matches;
        },
        media: query,
        onchange: null,
        addEventListener: (_: "change", fn: Listener) => entry.listeners.add(fn),
        removeEventListener: (_: "change", fn: Listener) => entry.listeners.delete(fn),
        // Deprecated pair, kept so a consumer that uses it does not crash.
        addListener: (fn: Listener) => entry.listeners.add(fn),
        removeListener: (fn: Listener) => entry.listeners.delete(fn),
        dispatchEvent: () => false,
      };
    },
  });
}

/**
 * Resizes an already-stubbed viewport and notifies every listener whose
 * answer changed, the way a real browser would.
 */
export function resizeViewport(px: number) {
  width = px;
  Object.defineProperty(window, "innerWidth", {
    writable: true,
    configurable: true,
    value: px,
  });
  for (const entry of live) {
    const next = evaluate(entry.query, px);
    if (next === entry.matches) continue;
    entry.matches = next;
    for (const fn of entry.listeners) {
      fn({ matches: next, media: entry.query } as MediaQueryListEvent);
    }
  }
}
