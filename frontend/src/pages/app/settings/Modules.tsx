import { Bot, Radar, Wallet } from "lucide-react";

import { SettingsPanel, SettingsRow } from "@/components/workspace";
import { Badge } from "@/components/ui/Badge";
import { isRouteHidden } from "@/lib/navVisibility";
import { useT } from "@/lib/i18n";

/**
 * Modules — what the navigation is showing, and nothing more.
 *
 * ── What this page used to be ──────────────────────────────────────────
 * Four switches backed by a `useState` initialised from a constant. They
 * flipped, they looked enabled, they persisted nothing and controlled
 * nothing, and two of the four rows named things that are not modules at
 * all: "Market Intelligence" and "Content Engine" were never implemented
 * and are not in the architecture. The page's own note admitted the
 * controls did nothing, which made it a screen that contradicted itself
 * in writing.
 *
 * ── Why it was not made real instead ───────────────────────────────────
 * Because there is nothing to make it control. Module composition is a
 * build-time fact in this platform: routes are mounted in `App.tsx` and
 * navigation entries are filtered through `HIDDEN_ROUTES`, a constant a
 * person edits and ships. A runtime toggle would need a backend, a
 * per-workspace store, and a decision about what "disabled" means for a
 * route already open in another tab — none of which this sprint was asked
 * for, and none of which should appear by accident behind a switch.
 *
 * So the switches are gone and the page reports the one thing it can know
 * for certain and read from the real source: which of the modules that
 * have a screen currently have an entry point.
 *
 * ── Why Threads is a note and not a row ────────────────────────────────
 * It is a module, and it has no screen and no route — it is operated
 * through Agents/Chat by decision. Listing it in a table whose column is
 * "is it in the navigation" would answer a question that does not apply
 * to it, and inventing a third state to accommodate one row would make
 * the other three harder to read. The note says it plainly instead.
 */

type ModuleRow = {
  route: string;
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
};

export function ModulesSettingsPage() {
  const t = useT();
  const m = t.app.settings.modules;

  // The same routes `App.tsx` mounts and the sidebar filters, named with
  // the same dictionary keys the sidebar uses. A label that disagreed with
  // the rail would be a fifth thing to keep in sync.
  const rows: ModuleRow[] = [
    { route: "/app/modules/agents",    label: t.app.sidebar.items.agents,   icon: Bot },
    { route: "/app/modules/job-radar", label: t.app.sidebar.items.jobRadar, icon: Radar },
    { route: "/app/modules/finance",   label: t.app.sidebar.items.finance,  icon: Wallet },
  ];

  return (
    <div className="space-y-4">
      <SettingsPanel title={m.title} description={m.description}>
        {rows.map((row) => {
          const hidden = isRouteHidden(row.route);
          return (
            <SettingsRow
              key={row.route}
              label={row.label}
              hint={hidden ? m.hiddenHint : m.visibleHint}
            >
              <div className="flex items-center gap-2">
                <row.icon
                  className="size-4 shrink-0 text-(--color-muted-foreground)"
                  aria-hidden
                />
                {/* Read-only state, not a control: there is no button, no
                    switch and no handler, because there is nothing on the
                    other end of one. */}
                <Badge variant={hidden ? "neutral" : "success"} size="sm">
                  {hidden ? m.state.hidden : m.state.visible}
                </Badge>
                <code className="truncate font-mono text-[11px] text-(--color-muted-foreground)">
                  {row.route}
                </code>
              </div>
            </SettingsRow>
          );
        })}
      </SettingsPanel>

      <div className="space-y-2 rounded-2xl border border-(--color-border) bg-(--color-muted)/40 px-5 py-4">
        <p className="text-[12.5px] leading-relaxed text-(--color-muted-foreground)">
          {m.note}
        </p>
        <p className="text-[12.5px] leading-relaxed text-(--color-muted-foreground)">
          {m.threadsNote}
        </p>
      </div>
    </div>
  );
}
