import { Suspense, useEffect } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { motion, useAnimationControls, useReducedMotion } from "framer-motion";
import { CommandPalette } from "./CommandPalette";
import { Header } from "./Header";
import { RegisterWorkspaceCommands } from "./RegisterWorkspaceCommands";
import { RouteFallback } from "./RouteFallback";
import { Sidebar } from "./Sidebar";
import { cn } from "@/lib/utils";
import { CommandProvider } from "@/lib/command";
import { NotificationsProvider } from "@/lib/notifications";
import { WorkspaceProvider } from "@/lib/workspace";

/**
 * WorkspaceLayout — top-level shell for every authenticated route.
 *
 * Provider tree (workspace-scoped — none of this exists on `/` or `/login`):
 *   WorkspaceProvider     → sidebar collapse / mobile drawer state
 *   CommandProvider       → palette registry + ⌘K toggle listener
 *   NotificationsProvider → notification feed + unread state
 *
 * `RegisterWorkspaceCommands` populates the palette with baseline navigation,
 * settings, theme, language and account actions. Modules will register their
 * own commands when mounted by calling `useRegisterCommands(...)`.
 *
 * Page transitions: a ~110ms slide on route change, driven imperatively on
 * one node that never unmounts. No fade, and no key — see `PageTransition`.
 *
 * ── Height chain ───────────────────────────────────────────────────────
 * From `h-dvh` down to `<Outlet />` every level is a flex column with
 * `min-h-0`, so a page can claim the exact remaining height by asking for
 * `flex-1` and scroll *inside* itself. Without the unbroken chain, a
 * `min-h-0`-less ancestor lets its child grow past the viewport and the
 * whole page scrolls instead — which is what a chat must never do, since
 * its composer and thread list have to stay put.
 *
 * Ordinary pages are unaffected: their content simply grows past the
 * remaining height and `main` scrolls it, exactly as before.
 */

const ease = [0.16, 1, 0.3, 1] as const;

/**
 * The page transition, on ONE node that never unmounts.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A ROUTE SUBTREE MUST MOUNT ONCE PER NAVIGATION. THIS USED TO MOUNT TWICE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── The bug this shape exists to prevent ───────────────────────────────
 * It was `<AnimatePresence mode="wait">` around a `motion.div` keyed by
 * `location.pathname`, wrapping `<Outlet />`. On navigation, AnimatePresence
 * keeps the OUTGOING wrapper mounted while it animates away — but that
 * wrapper's children are re-evaluated every render, so `<Outlet />` inside
 * it was already rendering the NEW route. The incoming page therefore
 * mounted under the outgoing key, and then again, ~180ms later, under the
 * incoming one.
 *
 * The second mount discards everything the first one had: component state,
 * measured sizes, an open panel, and the focused element. Press a key in
 * that window and the interaction is simply lost — which is exactly how
 * Enter on a Palace object opened the inspector and then closed it, while
 * the mouse, always arriving later, worked fine.
 *
 * ── Why the animation is driven instead of keyed ───────────────────────
 * A key IS a remount; that is what a key means. So the node is stable and
 * unkeyed, and the entrance is re-run imperatively when the path changes.
 * The visual result is the same fade-and-rise, and the route subtree mounts
 * once.
 *
 * The exit animation is gone, and it has to be: animating a page OUT means
 * keeping it mounted after it stopped being the page, which is the stale
 * subtree that caused all of this.
 *
 * ── Why the entrance no longer starts invisible ────────────────────────
 * It used to be `opacity: 0 → 1` over 180ms. A warm navigation delivers the
 * destination in 4 to 23ms, and then we hid it for 180: nine tenths of the
 * perceived wait was ours, not the product's. Measured the same way as the
 * blank frames above — the content was in the DOM and painted at zero
 * opacity.
 *
 * So the movement stayed and the hiding went. Opacity is never written, at
 * any point in the sequence; the page enters 4px low and settles in ~110ms,
 * which is enough to read as arrival and short enough that nobody waits for
 * it. A transition may communicate movement. It may not withhold
 * information.
 *
 * `prefers-reduced-motion` skips the movement entirely rather than
 * shortening it: the setting is a request for no motion, and there is
 * nothing to fade in its place because nothing was ever hidden.
 */
export function PageTransition({ children }: { children: React.ReactNode }) {
  const { pathname } = useLocation();
  const controls = useAnimationControls();
  const reducedMotion = useReducedMotion();

  useEffect(() => {
    if (reducedMotion) return;
    controls.set({ y: 4 });
    void controls.start({ y: 0, transition: { duration: 0.11, ease } });
  }, [pathname, controls, reducedMotion]);

  return (
    <motion.div animate={controls} className="flex flex-1 flex-col">
      {children}
    </motion.div>
  );
}

/**
 * Routes that manage their own width instead of sitting inside the shell's
 * 1600px reading measure.
 *
 * That cap is right for dashboards and forms — content that reads as a
 * document. The chat is not one: it has its own focus/wide control, and on
 * an ultrawide the cap would leave dead bands on both sides even with
 * "wide" selected, making the control look broken.
 */
// Palace draws a scene that scales into whatever it is given, so the
// shell's reading-width cap would only shrink the room. A6/Q5.
const FULL_WIDTH_ROUTES = ["/app/modules/agents", "/app/modules/palace"];

export function WorkspaceLayout() {
  return (
    <WorkspaceProvider>
      <CommandProvider>
        <NotificationsProvider>
          <RegisterWorkspaceCommands />
          <Shell />
          <CommandPalette />
        </NotificationsProvider>
      </CommandProvider>
    </WorkspaceProvider>
  );
}

function Shell() {
  const location = useLocation();
  const fullWidth = FULL_WIDTH_ROUTES.some((r) => location.pathname.startsWith(r));
  return (
    <div className="flex h-dvh bg-(--color-background) text-(--color-foreground)">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <Header />
        <main className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {/* `flex-1` without `min-h-0` on purpose. The default
              `min-height: auto` is what keeps both kinds of page working:
              a short one stretches to fill the viewport (so a chat can ask
              for the rest of the height), while a long one refuses to
              shrink below its content and `main` scrolls it — with the
              bottom padding still at the bottom, where it belongs. */}
          <div
            className={cn(
              "flex w-full flex-1 flex-col px-4 py-4 sm:px-5 sm:py-5 lg:px-8 lg:py-6",
              !fullWidth && "max-w-[1600px]",
            )}
          >
            {/* ════════════════════════════════════════════════════════
                THE ONLY SUSPENSE BOUNDARY IN THE AUTHENTICATED AREA

                It is here, and not in `App.tsx` beside each lazy route,
                because a boundary only keeps the previous page on screen
                if React RECONCILES it across the navigation. Per-route
                boundaries sit at positions that move with the route, so
                React mounted a new one and drew the fallback — 277 to
                284ms of loading bar in place of the page, measured in
                Chrome with zero emulated latency.

                Below `PageTransition`, so the fallback (when it is the
                honest answer — a cold entrance into the app) still enters
                with the page rather than jumping, and so the transition
                node itself never unmounts.

                Do not add a second one per module or per group: two
                boundaries are the same defect with a smaller blast
                radius. `App.suspense.test.tsx` pins the count. */}
            <PageTransition>
              <Suspense fallback={<RouteFallback />}>
                <Outlet />
              </Suspense>
            </PageTransition>
          </div>
        </main>
      </div>
    </div>
  );
}
