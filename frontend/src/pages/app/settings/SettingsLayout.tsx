import { NavLink, Outlet } from "react-router-dom";
import { Plug, ShieldCheck, SlidersHorizontal, SquareStack, User as UserIcon } from "lucide-react";

import { PageHeader } from "@/components/workspace";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/**
 * Settings — the area's shell.
 *
 * ── What this replaced ─────────────────────────────────────────────────
 * Five pages that shared a URL prefix and nothing else. Each drew its own
 * `PageHeader` with the eyebrow "Settings", and none of them linked to any
 * of the others, so landing on one was a dead end: no way sideways, no way
 * up, and nothing on screen saying the other four existed. Integrations
 * was the casualty — a working connector page that the product mentioned
 * nowhere, reachable only by typing its URL.
 *
 * ── Why a horizontal nav rather than a rail ────────────────────────────
 * A second vertical rail beside the workspace's own is the obvious
 * settings pattern, and at 1024px it is also the wrong one: 240px of
 * sidebar plus ~200px of settings rail leaves the panels under 500px, and
 * a rail that only earns its keep above 1280px means two layouts to keep
 * honest. This codebase has already been bitten once by a desktop
 * presentation leaking into a phone, so the nav here is one code path at
 * every width — the same scrolling tab row `AgentShell` uses, which makes
 * it the product's idiom rather than a new invention.
 *
 * ── Why Integrations sits second ───────────────────────────────────────
 * Ordering is the cheapest discoverability there is, and "connect my
 * GitHub" is the most common reason to open this area at all. Identity
 * still leads, because a settings area that opens on a connector list
 * reads as a different product.
 */

type Section = {
  to: string;
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
};

export function SettingsLayout() {
  const t = useT();
  const s = t.app.settings;

  const sections: Section[] = [
    { to: "profile",      label: s.profile.title,      icon: UserIcon },
    { to: "integrations", label: s.integrations.title, icon: Plug },
    { to: "preferences",  label: s.preferences.title,  icon: SlidersHorizontal },
    { to: "modules",      label: s.modules.title,      icon: SquareStack },
    { to: "security",     label: s.security.title,     icon: ShieldCheck },
  ];

  return (
    // One measure for the header, the nav and the panels, so the header's
    // rule ends where the content ends. The old pages capped the body at
    // `max-w-3xl` but let the header inherit the shell's 1600px, which is
    // why the rule floated past the panels below it.
    <div className="w-full max-w-4xl space-y-6">
      <PageHeader
        eyebrow={t.app.user.account}
        title={s.title}
        description={s.description}
      />

      <nav
        aria-label={s.navLabel}
        // `-mx-1 px-1` so a focus ring on the first item is not clipped by
        // the scroll container.
        className="-mx-1 flex items-center gap-1 overflow-x-auto px-1 pb-0.5"
      >
        {sections.map((section) => (
          <NavLink
            key={section.to}
            to={section.to}
            className={({ isActive }) =>
              cn(
                "inline-flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-2 text-[12.5px] font-medium",
                "transition-colors duration-[250ms] [transition-timing-function:var(--ease-premium)]",
                isActive
                  ? "bg-(--color-muted) text-(--color-foreground)"
                  : "text-(--color-muted-foreground) hover:bg-(--color-muted)/60 hover:text-(--color-foreground)",
              )
            }
          >
            {/* The icon is decoration: the label is always rendered, at
                every width, so no destination is identified by glyph
                alone. NavLink sets `aria-current="page"` on the active
                one, which is what announces the current section. */}
            <section.icon className="size-3.5 shrink-0" aria-hidden />
            {section.label}
          </NavLink>
        ))}
      </nav>

      <Outlet />
    </div>
  );
}
