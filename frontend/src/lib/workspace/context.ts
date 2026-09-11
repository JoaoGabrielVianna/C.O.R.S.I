import { createContext } from "react";

export type WorkspaceContextValue = {
  /**
   * Desktop (lg+) rail presentation preference — narrow icon rail vs full
   * 240px. Persisted only when the user toggles it.
   *
   * This is NOT "hide text". Below `lg` the sidebar is a drawer with room
   * for labels, and it ignores this flag — see `showExpandedContent`.
   */
  collapsed: boolean;
  toggleCollapsed: () => void;
  setCollapsed: (next: boolean) => void;

  /** Whether the viewport is at `lg+`, where the rail exists at all. */
  isDesktop: boolean;

  /**
   * Whether the sidebar should render labels, wordmark, section headings
   * and the user block. False only for a desktop rail the user collapsed
   * on purpose; the mobile drawer is always expanded content.
   */
  showExpandedContent: boolean;

  /** Mobile (<lg) drawer open/closed. */
  mobileOpen: boolean;
  openMobile: () => void;
  closeMobile: () => void;
};

export const WorkspaceContext = createContext<WorkspaceContextValue | null>(null);
