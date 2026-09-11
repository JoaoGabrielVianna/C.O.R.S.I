/**
 * Navigation visibility — single source of truth for what the workspace
 * advertises in its navigation surfaces (sidebar + command palette).
 *
 * Half-built modules stay out of the navigation while the platform is being
 * recorded for content. This hides *entry points only*: every route below is
 * still mounted in `App.tsx` and reachable by typing the URL, so nothing is
 * disabled and no page code changes.
 *
 * To bring an item back, delete its line. Empty groups collapse on their own.
 */
export const HIDDEN_ROUTES: readonly string[] = [
  "/app/dashboard",
  // Finance came back on 2026-08-26. It was hidden while two of its domain
  // entities still lived in the browser and the module had no way to be
  // operated; both of those changed — it is backend-backed, it is a
  // capability an agent can be granted, and the screens read the same
  // application layer the tools do. The line is deleted rather than
  // commented out: the list is what the navigation believes, and an entry
  // nobody can see is one nobody can reason about.
  "/app/modules/news",
  "/app/modules/content",
];

export function isRouteHidden(route: string): boolean {
  return HIDDEN_ROUTES.includes(route);
}

/**
 * Where `/app` lands. Must point at a route that is *not* hidden — otherwise
 * signing in drops the user straight into a page the navigation denies exists.
 */
export const DEFAULT_APP_ROUTE = "modules/agents";
