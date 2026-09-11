import { Outlet, useLocation } from "react-router-dom";
import { AnimatePresence, motion } from "framer-motion";
import { CommandPalette } from "./CommandPalette";
import { Header } from "./Header";
import { RegisterWorkspaceCommands } from "./RegisterWorkspaceCommands";
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
 * Page transitions: a 120ms fade+slide on route change keyed by pathname so
 * switching tabs feels native instead of teleport-y.
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
 * Routes that manage their own width instead of sitting inside the shell's
 * 1600px reading measure.
 *
 * That cap is right for dashboards and forms — content that reads as a
 * document. The chat is not one: it has its own focus/wide control, and on
 * an ultrawide the cap would leave dead bands on both sides even with
 * "wide" selected, making the control look broken.
 */
const FULL_WIDTH_ROUTES = ["/app/modules/agents"];

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
            <AnimatePresence mode="wait">
              <motion.div
                key={location.pathname}
                initial={{ opacity: 0, y: 6 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -4 }}
                transition={{ duration: 0.18, ease }}
                className="flex flex-1 flex-col"
              >
                <Outlet />
              </motion.div>
            </AnimatePresence>
          </div>
        </main>
      </div>
    </div>
  );
}
