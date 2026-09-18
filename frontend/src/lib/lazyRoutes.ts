/**
 * The lazy module pages, in ONE place, with the preloader that warms them.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE ROUTE AND THE PREFETCH MUST NAME THE SAME `import()`
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── Why this file exists ───────────────────────────────────────────────
 * A prefetch is only a prefetch if it downloads the chunk the route will
 * actually ask for. Written twice — once beside the route, once beside the
 * link — the two specifiers drift, and the failure is silent: everything
 * keeps working, the hover downloads a module nobody is about to need, and
 * the click still waits for its own. So the `import()` is written once and
 * both consumers read it from here.
 *
 * This is deliberately NOT a router abstraction. It is a list of six
 * modules and a function; `App.tsx` still declares every route, and the
 * shape of the routing tree is unchanged.
 *
 * ── What a prefetch is allowed to be ───────────────────────────────────
 * CODE, and nothing else. Hovering a link is a guess about intent, and a
 * guess may spend bandwidth on a chunk that is cacheable, versioned and
 * identical for everyone. It may NOT read the operator's data: that would
 * turn a mouse passing over a word into a query against the Palace, and
 * the request would be indistinguishable — in the log, in the audit, in
 * the metrics — from one the operator asked for.
 *
 * Mounting nothing follows from the same rule. `import()` evaluates the
 * module; it does not render a component, so no `useQuery` runs, no effect
 * fires, and no URL changes.
 *
 * ── Why the sidebar's destinations and not every route ─────────────────
 * Because a prefetch is a bet, and the bet is only good where intent is
 * legible. A person pointing at "Palace" in the rail is probably going to
 * Palace. Nobody hovers their way into `/app/people/:id`, so that one stays
 * out of the table below while keeping its lazy chunk.
 */

import { lazy } from "react";

/* ── the six dynamic imports, written once ──────────────────────────── */

const loadJobRadar = () => import("@/pages/app/modules/job-radar");
const loadFinance = () => import("@/pages/app/modules/finance");
const loadAgents = () => import("@/pages/app/modules/agents");
const loadPalace = () => import("@/pages/app/modules/palace");
const loadReleases = () => import("@/pages/app/releases");
const loadPersonDetail = () => import("@/pages/app/PersonDetail");

export const JobRadarPage = lazy(() =>
  loadJobRadar().then((m) => ({ default: m.JobRadarPage })),
);
export const FinancePage = lazy(() => loadFinance().then((m) => ({ default: m.FinancePage })));
// Agents and Palace declare their own nested routes: each module has an
// internal hierarchy, and that shape belongs with the module. One lazy
// chunk still covers all of it.
export const AgentsPage = lazy(() => loadAgents().then((m) => ({ default: m.AgentsPage })));
export const PalacePage = lazy(() => loadPalace().then((m) => ({ default: m.PalacePage })));
export const ReleasesPage = lazy(() => loadReleases().then((m) => ({ default: m.ReleasesPage })));
export const PersonDetailPage = lazy(() =>
  loadPersonDetail().then((m) => ({ default: m.PersonDetailPage })),
);

/* ── what a hover or a focus is allowed to warm ─────────────────────── */

/**
 * The sidebar's destinations, longest-lived first in nobody's order: the
 * lookup below matches on prefix, and no two of these are prefixes of each
 * other.
 */
const PRELOADABLE: readonly (readonly [string, () => Promise<unknown>])[] = [
  ["/app/modules/job-radar", loadJobRadar],
  ["/app/modules/finance", loadFinance],
  ["/app/modules/agents", loadAgents],
  ["/app/modules/palace", loadPalace],
  ["/app/releases", loadReleases],
];

/**
 * Which chunks this session has already asked for.
 *
 * The module registry would deduplicate anyway — a second `import()` of the
 * same specifier resolves the same module without a second request — but a
 * rail is hovered dozens of times a minute and each of those would otherwise
 * allocate a promise and a microtask to discover it had nothing to do.
 *
 * A FAILED load is removed, so an offline hover does not poison the route
 * for the rest of the session.
 */
const started = new Set<string>();

/**
 * Warm the code for a navigable path. Safe to call on every pointer event.
 *
 * Unknown paths, hidden modules and anything that is not a sidebar
 * destination are silently ignored: the caller passes whatever it is
 * linking to, and deciding what is preloadable is this file's job.
 */
export function preloadRoute(path: string): void {
  const entry = PRELOADABLE.find(([base]) => path === base || path.startsWith(`${base}/`));
  if (!entry) return;
  const [base, load] = entry;
  if (started.has(base)) return;
  started.add(base);
  void load().catch(() => {
    started.delete(base);
  });
}
