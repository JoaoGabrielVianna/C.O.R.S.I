/**
 * RouteFallback — what the route area shows while a lazy chunk is in flight.
 *
 * ── Why it lives beside the shell and not in `App.tsx` ─────────────────
 * Because there is exactly ONE Suspense boundary for the whole authenticated
 * area and it sits in `WorkspaceLayout`, around the `<Outlet />`. The
 * fallback belongs with the boundary that uses it; leaving it in the router
 * file would invite a second boundary to be written there, which is the
 * topology this slice removed.
 *
 * ── Why it carries a data attribute ────────────────────────────────────
 * `data-route-fallback` is the hook the browser regression uses to assert
 * the thing that actually matters to a person: that switching modules never
 * replaces known content with this. A class name would work until Tailwind
 * changed, and this bar has no text to look for.
 *
 * It should now be RARE. With a stable boundary it is drawn on a cold
 * entrance into the app, not on a navigation between two modules.
 */
export function RouteFallback() {
  return (
    <div
      data-route-fallback
      className="flex h-full min-h-[200px] items-center justify-center"
    >
      <div className="h-1 w-32 overflow-hidden rounded-full bg-(--color-muted)">
        <div className="h-full w-1/3 animate-pulse rounded-full bg-(--color-brand-500)" />
      </div>
    </div>
  );
}
