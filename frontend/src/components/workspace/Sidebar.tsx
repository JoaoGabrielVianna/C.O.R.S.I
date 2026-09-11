import { useEffect, useRef, useState } from "react";
import { NavLink } from "react-router-dom";
import {
  Activity,
  BarChart3,
  Bot,
  ChevronsLeft,
  ChevronsRight,
  ChevronsUpDown,
  History,
  LayoutDashboard,
  Radar,
  Sparkles,
  Wallet,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { LogoMark, Wordmark } from "@/components/ui/Logo";
import { useT } from "@/lib/i18n";
import { useAuth } from "@/lib/auth";
import { isRouteHidden } from "@/lib/navVisibility";
import { useWorkspace } from "@/lib/workspace";
import { AccountMenuPanel } from "./AccountMenu";

/**
 * Sidebar — workspace primary navigation.
 *
 * Three responsive states:
 *   · lg+, expanded  : 240px rail, icon + label
 *   · lg+, collapsed : 64px rail, icon only — an explicit user choice
 *   · <lg            : off-canvas drawer (~260px), always icon + label
 *
 * ── Why `collapsed` is not what the content reads ──────────────────────
 * `collapsed` is the desktop rail's preference. The drawer is 260px wide
 * and has room for text regardless of it, so everything textual here hangs
 * off `showExpandedContent` (see `lib/workspace`), which is false only for
 * a desktop rail somebody deliberately narrowed. Gating the labels on the
 * raw boolean is what used to strip them out of the drawer, leaving a
 * column of unlabelled icons on a phone.
 *
 * Widths stay on `collapsed` because they are already `lg:`-scoped and
 * below `lg` there is no rail to size.
 *
 * Sections: Overview · Modules. Items listed in `HIDDEN_ROUTES` (see
 * `lib/navVisibility`) are filtered out and a section that ends up empty is
 * not rendered at all. Bottom user block holds identity, the current auth
 * provider (Keycloak insertion point — flips label) and the account menu.
 *
 * ── Why configuration is not a nav section ─────────────────────────────
 * The rail is for work: the modules the operator opens to get something
 * done. Settings, Profile and Integrations are account surfaces, and they
 * hang off the user block here and off the header's avatar — both of which
 * render the same `AccountMenuPanel`, so the two can no longer disagree
 * about what they offer. Integrations in particular is not a module and
 * putting it beside Job Radar would say it was one.
 */

type NavItem = {
  to: string;
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  /** When true the item is rendered as muted; the route still works. */
  upcoming?: boolean;
};

export function Sidebar() {
  const { collapsed, showExpandedContent, toggleCollapsed, mobileOpen, closeMobile } =
    useWorkspace();

  useEffect(() => {
    if (!mobileOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeMobile();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [mobileOpen, closeMobile]);

  return (
    <>
      {/* Mobile backdrop */}
      {mobileOpen ? (
        <div
          aria-hidden
          onClick={closeMobile}
          className="fixed inset-0 z-40 bg-black/40 backdrop-blur-sm lg:hidden"
        />
      ) : null}

      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-50 flex flex-col border-r border-(--color-border) bg-(--color-card)",
          "transition-[transform,width] duration-[300ms] [transition-timing-function:var(--ease-premium)]",
          // Desktop visibility + width
          "lg:static lg:translate-x-0",
          collapsed ? "lg:w-[64px]" : "lg:w-[240px]",
          // Mobile drawer slide
          "w-[260px]",
          mobileOpen ? "translate-x-0" : "-translate-x-full lg:translate-x-0",
        )}
      >
        <SidebarHeader expanded={showExpandedContent} onCloseMobile={closeMobile} />

        <nav className="flex-1 overflow-y-auto px-2 py-3">
          <Sections expanded={showExpandedContent} onNavigate={closeMobile} />
        </nav>

        <SidebarFooter expanded={showExpandedContent} onToggle={toggleCollapsed} />
      </aside>
    </>
  );
}

function SidebarHeader({
  expanded,
  onCloseMobile,
}: {
  expanded: boolean;
  onCloseMobile: () => void;
}) {
  const t = useT();
  return (
    <header
      className={cn(
        "flex h-14 shrink-0 items-center border-b border-(--color-border)",
        expanded ? "px-4" : "lg:justify-center lg:px-0",
        "justify-between px-4",
      )}
    >
      <div className="flex items-center gap-2.5">
        <LogoMark />
        {expanded ? <Wordmark /> : null}
      </div>
      <button
        type="button"
        onClick={onCloseMobile}
        aria-label={t.app.sidebar.closeSidebar}
        className="flex size-8 items-center justify-center rounded-lg text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) lg:hidden"
      >
        <X className="size-4" />
      </button>
    </header>
  );
}

function Sections({
  expanded,
  onNavigate,
}: {
  expanded: boolean;
  onNavigate: () => void;
}) {
  const t = useT();

  const overview: NavItem[] = [
    { to: "/app/dashboard", label: t.app.sidebar.items.dashboard, icon: LayoutDashboard },
    // Release history describes the installation rather than a domain, so
    // it belongs in Overview and not in Modules.
    { to: "/app/releases", label: t.app.sidebar.items.releases, icon: History },
  ];
  const modules: NavItem[] = [
    { to: "/app/modules/job-radar", label: t.app.sidebar.items.jobRadar, icon: Radar },
    { to: "/app/modules/finance",   label: t.app.sidebar.items.finance,  icon: Wallet },
    { to: "/app/modules/agents",    label: t.app.sidebar.items.agents,   icon: Bot },
    { to: "/app/modules/news",      label: t.app.sidebar.items.intelligence, icon: BarChart3, upcoming: true },
    { to: "/app/modules/content",   label: t.app.sidebar.items.content,  icon: Sparkles, upcoming: true },
  ];
  // Settings no longer lives here — it hangs off the user block in the footer.

  const groups = [
    { label: t.app.sidebar.overview, items: overview },
    { label: t.app.sidebar.modules,  items: modules },
  ]
    .map((group) => ({ ...group, items: group.items.filter((i) => !isRouteHidden(i.to)) }))
    .filter((group) => group.items.length > 0);

  return (
    <div className="flex flex-col gap-5">
      {groups.map((group) => (
        <NavGroup
          key={group.label}
          label={group.label}
          items={group.items}
          expanded={expanded}
          onNavigate={onNavigate}
        />
      ))}
    </div>
  );
}

function NavGroup({
  label,
  items,
  expanded,
  onNavigate,
}: {
  label: string;
  items: NavItem[];
  expanded: boolean;
  onNavigate: () => void;
}) {
  return (
    <div>
      {expanded ? (
        <p className="px-3 pb-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {label}
        </p>
      ) : (
        <div className="mx-3 mb-1.5 h-px bg-(--color-border) lg:block" aria-hidden />
      )}
      <ul className="space-y-0.5">
        {items.map((item) => (
          <li key={item.to}>
            <NavRow item={item} expanded={expanded} onNavigate={onNavigate} />
          </li>
        ))}
      </ul>
    </div>
  );
}

function NavRow({
  item,
  expanded,
  onNavigate,
}: {
  item: NavItem;
  expanded: boolean;
  onNavigate: () => void;
}) {
  const t = useT();
  const Icon = item.icon;
  return (
    <NavLink
      to={item.to}
      onClick={onNavigate}
      // Icon-only must still say what it is. `title` draws the tooltip;
      // `aria-label` is what actually names the link, rather than leaning
      // on the browser's `title` fallback in the accessibility tree.
      title={expanded ? undefined : item.label}
      aria-label={expanded ? undefined : item.label}
      className={({ isActive }) =>
        cn(
          "group relative flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13.5px] font-medium",
          "transition-colors duration-150",
          isActive
            ? "bg-(--color-muted) text-(--color-foreground)"
            : "text-(--color-muted-foreground) hover:bg-(--color-muted)/60 hover:text-(--color-foreground)",
          expanded ? "" : "lg:justify-center lg:px-0",
        )
      }
    >
      {({ isActive }) => (
        <>
          {isActive ? (
            <span
              aria-hidden
              className={cn(
                "absolute left-0 top-1/2 h-5 w-[2px] -translate-y-1/2 rounded-r-full bg-(--color-brand-500)",
                expanded ? "" : "lg:hidden",
              )}
            />
          ) : null}
          <Icon
            className={cn(
              "size-4 shrink-0",
              isActive ? "text-(--color-brand-600) dark:text-(--color-brand-400)" : "",
              item.upcoming && !isActive ? "opacity-60" : "",
            )}
          />
          {expanded ? (
            <span className={cn("truncate", item.upcoming && !isActive ? "opacity-70" : "")}>
              {item.label}
            </span>
          ) : null}
          {expanded && item.upcoming ? (
            <span className="ml-auto font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {t.app.sidebar.soon}
            </span>
          ) : null}
        </>
      )}
    </NavLink>
  );
}

function SidebarFooter({
  expanded,
  onToggle,
}: {
  expanded: boolean;
  onToggle: () => void;
}) {
  const t = useT();
  const { user, provider } = useAuth();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!menuOpen) return;
    const onClick = (e: MouseEvent) => {
      if (!menuRef.current?.contains(e.target as Node)) setMenuOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [menuOpen]);

  const initials = (user?.name ?? "?")
    .split(" ")
    .map((p) => p[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();

  return (
    <footer className="border-t border-(--color-border) p-2">
      <div ref={menuRef} className="relative">
        {menuOpen ? (
          <AccountMenuPanel
            onNavigate={() => setMenuOpen(false)}
            className={cn(
              "absolute",
              // Sideways only for the 64px desktop rail, which has no room
              // above the button. In the drawer `left-full` put the menu
              // past the panel's right edge and off a 390px screen.
              expanded
                ? "bottom-[calc(100%+6px)] left-0 right-0"
                : "bottom-0 left-full ml-2 w-56",
            )}
          />
        ) : null}

        <button
          type="button"
          onClick={() => setMenuOpen((s) => !s)}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          aria-label={t.app.user.account}
          className={cn(
            "flex w-full items-center gap-3 rounded-lg px-2 py-2 text-left",
            "transition-colors duration-150 hover:bg-(--color-muted)",
            expanded ? "" : "lg:justify-center",
          )}
        >
          <span className="flex size-8 shrink-0 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted) font-mono text-[11px] font-semibold text-(--color-foreground)">
            {initials}
          </span>
          {expanded ? (
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13px] font-medium text-(--color-foreground)">
                {user?.name ?? "—"}
              </p>
              <p className="truncate font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                {t.app.user.role} · {t.app.user.status}
              </p>
            </div>
          ) : null}
          {expanded ? (
            <ChevronsUpDown className="size-3.5 shrink-0 text-(--color-muted-foreground)" />
          ) : null}
        </button>
      </div>
      {expanded ? (
        <div className="mt-1 flex items-center justify-between gap-2 px-2 pb-1 pt-1">
          <span className="inline-flex items-center gap-1.5">
            <span
              aria-hidden
              className={cn(
                "size-1.5 rounded-full",
                provider === "keycloak" ? "bg-emerald-400" : "bg-amber-400",
              )}
            />
            <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {provider === "keycloak" ? "Keycloak" : t.app.user.providerMock}
            </span>
          </span>
          <button
            type="button"
            onClick={onToggle}
            aria-label={t.app.sidebar.collapseSidebar}
            className="hidden size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) lg:flex"
          >
            <ChevronsLeft className="size-3.5" />
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={onToggle}
          aria-label={t.app.sidebar.expandSidebar}
          className="mt-1 hidden w-full items-center justify-center rounded-md py-1.5 text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) lg:flex"
        >
          <ChevronsRight className="size-3.5" />
        </button>
      )}
      {expanded ? (
        <p className="px-2 pt-1 font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)/70">
          <Activity className="mr-1 inline size-2.5 align-[-1px]" />
          {t.app.sidebar.sessionNote}
        </p>
      ) : null}
    </footer>
  );
}
