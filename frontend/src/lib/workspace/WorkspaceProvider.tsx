import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { WorkspaceContext } from "./context";

/**
 * WorkspaceProvider — shell UI state.
 *
 * ── The three states the shell actually has ────────────────────────────
 *   desktop expanded   → 240px rail, icon + label
 *   desktop collapsed  → 64px rail, icon only (an explicit user choice)
 *   small screen       → off-canvas drawer, ~260px, always icon + label
 *
 * `collapsed` describes ONLY the first two. It is the desktop rail's
 * presentation preference and nothing else. What the sidebar renders is
 * `showExpandedContent`, derived below, because the drawer is 260px wide
 * and has room for text no matter what the desktop rail is set to.
 *
 * ── Why that distinction is a provider concern ─────────────────────────
 * The width classes were always scoped (`lg:w-[64px]`), but the content
 * was gated on the raw boolean, so a collapsed desktop rail stripped the
 * labels out of the mobile drawer too. Deriving the answer once here is
 * what keeps the fix from becoming a viewport check sprinkled over every
 * row, heading and footer block in `Sidebar.tsx`.
 */

const COLLAPSED_KEY = "corsi.workspace.sidebar.collapsed";
const DESKTOP_QUERY = "(min-width: 1024px)";

/**
 * The stored preference, or `null` when the user has never expressed one.
 *
 * The three-way return matters: "no preference" and "prefers expanded" used
 * to collapse into the same `false`, which is what let a viewport-derived
 * guess be written back as though somebody had chosen it.
 */
function readStoredCollapsed(): boolean | null {
  if (typeof window === "undefined") return null;
  try {
    const stored = window.localStorage.getItem(COLLAPSED_KEY);
    if (stored === "1") return true;
    if (stored === "0") return false;
  } catch {
    /* storage unavailable — treat as "no preference" */
  }
  return null;
}

function writeStoredCollapsed(next: boolean) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(COLLAPSED_KEY, next ? "1" : "0");
  } catch {
    /* ignore */
  }
}

/**
 * jsdom 29 ships no `matchMedia`, and this runs during `useState`
 * initialisation, so the guard is load-bearing rather than defensive.
 * Assuming desktop is the safe answer: it only decides whether a rail the
 * test environment never lays out counts as collapsed.
 */
function readIsDesktop(): boolean {
  if (typeof window === "undefined") return true;
  if (typeof window.matchMedia !== "function") return true;
  return window.matchMedia(DESKTOP_QUERY).matches;
}

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  // No stored preference means expanded. The old code guessed from the
  // viewport instead, and collapsing "on tablet" stopped meaning anything
  // once below `lg` became a drawer rather than a narrow rail — there is no
  // rail down there to dominate anything.
  const [collapsed, setCollapsedState] = useState<boolean>(
    () => readStoredCollapsed() ?? false,
  );
  const [isDesktop, setIsDesktop] = useState<boolean>(() => readIsDesktop());
  const [mobileOpen, setMobileOpen] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(DESKTOP_QUERY);
    const onChange = (e: MediaQueryListEvent) => {
      setIsDesktop(e.matches);
      // Crossing the breakpoint closes the drawer so it can't leak into the
      // desktop layout. Unchanged behaviour, moved into the same listener.
      setMobileOpen(false);
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  /**
   * Persistence happens here, in the two entry points a person can reach,
   * and NOT in an effect keyed on `collapsed`.
   *
   * That effect was the whole bug in the persistence half: it fired on
   * mount and wrote the initial value back, so a first visit on a phone
   * recorded `collapsed=1` as a deliberate choice. The same desktop later
   * read it back and came up collapsed for a decision nobody made.
   */
  const setCollapsed = useCallback((next: boolean) => {
    setCollapsedState(next);
    writeStoredCollapsed(next);
  }, []);

  const toggleCollapsed = useCallback(
    () => setCollapsed(!collapsed),
    [collapsed, setCollapsed],
  );

  const openMobile = useCallback(() => setMobileOpen(true), []);
  const closeMobile = useCallback(() => setMobileOpen(false), []);

  /**
   * Labels, wordmark, section headings and the user block hang off this.
   *
   * Icon-only is reachable exactly one way: a desktop rail the user chose
   * to collapse. The drawer never inherits it.
   */
  const showExpandedContent = !(collapsed && isDesktop);

  const value = useMemo(
    () => ({
      collapsed,
      toggleCollapsed,
      setCollapsed,
      isDesktop,
      showExpandedContent,
      mobileOpen,
      openMobile,
      closeMobile,
    }),
    [
      collapsed,
      toggleCollapsed,
      setCollapsed,
      isDesktop,
      showExpandedContent,
      mobileOpen,
      openMobile,
      closeMobile,
    ],
  );

  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>;
}
