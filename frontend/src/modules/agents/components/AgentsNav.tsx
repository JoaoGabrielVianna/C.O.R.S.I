/*
 * LEGACY — not mounted by the Agents v1 information architecture.
 *
 * The two-item Chat / Configuração switcher. The module no longer has two
 * top-level views: it has routes, and the agent's own subnav lives in
 * `components/AgentShell.tsx`. Nothing imports it.
 */
import { MessagesSquare, SlidersHorizontal } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * The Chat / Configuração switcher — the module's two top-level views.
 *
 * One component, two orientations, so the same control renders in the page
 * header (horizontal) or in a left rail (vertical) without diverging.
 */

export type AgentsTab = "chat" | "settings";

const ITEMS = [
  { key: "chat", label: "Chat", icon: MessagesSquare },
  { key: "settings", label: "Configuração", icon: SlidersHorizontal },
] as const;

export function AgentsNav({
  tab,
  onTab,
  orientation = "horizontal",
}: {
  tab: AgentsTab;
  onTab: (t: AgentsTab) => void;
  orientation?: "horizontal" | "vertical";
}) {
  const vertical = orientation === "vertical";
  return (
    <div
      className={cn(
        "gap-1 rounded-xl border border-(--color-border) bg-(--color-card) p-1",
        vertical ? "flex flex-col" : "inline-flex items-center",
      )}
      role="tablist"
      aria-orientation={vertical ? "vertical" : "horizontal"}
    >
      {ITEMS.map((it) => {
        const active = tab === it.key;
        return (
          <button
            key={it.key}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onTab(it.key)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium",
              "transition-colors duration-[250ms] [transition-timing-function:var(--ease-premium)]",
              vertical ? "w-full justify-start" : "",
              active
                ? "bg-(--color-muted) text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            <it.icon className="size-3.5" />
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
