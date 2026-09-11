import { LogOut, Plug, SlidersHorizontal, User as UserIcon } from "lucide-react";
import { NavLink, useNavigate } from "react-router-dom";
import { cn } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";

/**
 * The account menu — one definition, two places that draw it.
 *
 * ── What was wrong ─────────────────────────────────────────────────────
 * There were two of these. The header's avatar offered Account, Settings
 * and Sign out; the sidebar's user block offered Profile and Settings and
 * no way to sign out. The word "Settings" pointed at Profile in one and at
 * Preferences in the other, and "Profile" in the sidebar led to the same
 * page as "Settings" in the header. Two menus that disagree about what
 * their own words mean is worse than either menu alone, so the items now
 * live here and both surfaces render this component.
 *
 * ── Why Integrations is a top-level item ───────────────────────────────
 * Because "I want to connect my GitHub" is the question this menu exists
 * to answer, and before this batch the page had no entry point anywhere in
 * the product — not the sidebar, not either user menu. It was reachable
 * only by typing the URL or by already knowing the word to type into ⌘K,
 * which is not discovery. Every item here still lands inside the settings
 * shell, whose own navigation exposes the sections this list leaves out.
 */

type AccountMenuItem = {
  to: string;
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
};

/**
 * The route and the icon are structure; the label is copy and comes from
 * the dictionary. No item duplicates another's destination — a menu with
 * two words for one page is how the previous pair got confusing.
 */
function useAccountMenuItems(): AccountMenuItem[] {
  const t = useT();
  return [
    { to: "/app/settings/profile",      label: t.app.settings.profile.title,      icon: UserIcon },
    { to: "/app/settings/integrations", label: t.app.settings.integrations.title, icon: Plug },
    { to: "/app/settings/preferences",  label: t.app.settings.preferences.title,  icon: SlidersHorizontal },
  ];
}

type Props = {
  /** Placement classes. The header drops it below-right; the sidebar
   *  raises it above the user block, or sideways off a 64px rail. */
  className?: string;
  onNavigate: () => void;
};

export function AccountMenuPanel({ className, onNavigate }: Props) {
  const t = useT();
  const navigate = useNavigate();
  const { user, provider, signOut } = useAuth();
  const items = useAccountMenuItems();

  const handleSignOut = async () => {
    onNavigate();
    await signOut();
    navigate("/login", { replace: true });
  };

  return (
    <div
      role="menu"
      aria-label={t.app.user.account}
      className={cn(
        "z-50 overflow-hidden rounded-xl border border-(--color-border)",
        "bg-(--color-card) shadow-(--shadow-lift)",
        className,
      )}
    >
      <div className="border-b border-(--color-border) px-4 py-3">
        <p className="truncate text-[13px] font-semibold text-(--color-foreground)">
          {user?.name ?? "—"}
        </p>
        <p className="truncate text-[11.5px] text-(--color-muted-foreground)">
          {user?.email ?? "—"}
        </p>
        <p className="mt-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {provider === "keycloak" ? "Keycloak" : t.app.user.providerMock}
        </p>
      </div>

      <nav className="p-1" aria-label={t.app.user.settings}>
        {/* The group heading is what puts the word "Settings" in front of
            the reader without adding a fourth item that would land on the
            same page as the first one. */}
        <p className="px-3 pb-1 pt-1.5 font-mono text-[9.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {t.app.user.settings}
        </p>
        {items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            role="menuitem"
            onClick={onNavigate}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13.5px]",
                "transition-colors duration-150 hover:bg-(--color-muted)",
                isActive
                  ? "bg-(--color-muted) text-(--color-foreground)"
                  : "text-(--color-foreground)",
              )
            }
          >
            <item.icon className="size-4 text-(--color-muted-foreground)" />
            {item.label}
          </NavLink>
        ))}
      </nav>

      <div className="border-t border-(--color-border) p-1">
        <button
          type="button"
          role="menuitem"
          onClick={handleSignOut}
          className={cn(
            "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[13.5px]",
            "text-(--color-foreground) transition-colors duration-150",
            "hover:bg-(--color-muted)",
          )}
        >
          <LogOut className="size-4 text-(--color-muted-foreground)" />
          {t.app.user.signOut}
        </button>
      </div>
    </div>
  );
}
