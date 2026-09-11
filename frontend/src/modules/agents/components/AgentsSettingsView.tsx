/*
 * LEGACY — not mounted by the Agents v1 information architecture.
 *
 * The three-panel "Configuração dos Agents" screen. Its parts survive in
 * their own places: providers and the per-model cost panel on the Provedores
 * page, the agent form on each agent's Settings page.
 *
 * Kept rather than deleted for the same reason as AgentsPanel: no version
 * control in this tree. Nothing imports it.
 */
import { BarChart3, Bot, KeyRound } from "lucide-react";
import { cn } from "@/lib/utils";
import { AgentsPanel } from "@/modules/agents/components/AgentsPanel";
import { ProvidersPanel } from "@/modules/agents/components/ProvidersPanel";
import { ModelCostsPanel } from "@/modules/agents/components/ModelCostsPanel";
import type { NavPlacement } from "@/modules/agents/hooks/useNavPlacement";

/**
 * The Agents settings screen.
 *
 * Three stops, laid out as a two-column grid on wide windows:
 *
 *   Tokens   → the LiteLLM connections (endpoint + key) + their real spend
 *   Agentes  → each bound to one token, editable in place + its estimate
 *   Modelos  → the same estimate regrouped by model
 *
 * Cost is not a stop of its own: a number belongs beside the thing it
 * describes, so it rides along in the token and agent rows. Only the
 * per-model view earns a panel, because neither of those rows can answer
 * which model is burning the money.
 *
 * ── Layout ─────────────────────────────────────────────────────────────
 * The records are tables and want width; the cost panel is a narrow, dense
 * summary that should stay in view while you scan them — so it takes a
 * fixed side column that sticks, and the tables take the rest. Below `xl`
 * the columns stack with the cost panel FIRST: on a narrow window it is the
 * thing you glance at, not the thing you scroll past.
 *
 * Nothing bleeds outside its container. An earlier version pulled the
 * sticky bar out with negative margins to compensate a padding its scroll
 * container never had, which is what produced a horizontal scrollbar on the
 * whole page. Wide content now scrolls inside its own table wrapper.
 */

const SECTIONS = [
  { id: "sec-tokens", label: "Tokens", icon: KeyRound },
  { id: "sec-agentes", label: "Agentes", icon: Bot },
  { id: "sec-modelos", label: "Modelos", icon: BarChart3 },
] as const;

/**
 * `placement` is optional: the switcher preference only makes sense when the
 * page that owns it hands down a setter. Without one, the card is simply not
 * rendered — the screen still works standalone.
 */
export function AgentsSettingsView({
  placement,
  onPlacementChange,
}: {
  placement?: NavPlacement;
  onPlacementChange?: (p: NavPlacement) => void;
} = {}) {
  const goTo = (id: string) => {
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <div className="space-y-4">
      <div className="sticky top-0 z-20 flex flex-wrap items-center justify-between gap-2 border-b border-(--color-border) bg-(--color-background)/95 py-3 backdrop-blur">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-(--color-foreground)">
            Configuração dos Agents
          </h2>
          <p className="truncate text-xs text-(--color-muted-foreground)">
            Tokens (conexões LiteLLM), os agentes que os usam e o que cada modelo custou.
          </p>
        </div>
        <nav className="flex items-center gap-1">
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => goTo(s.id)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium",
                "text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
                "transition-colors duration-200",
              )}
            >
              <s.icon className="size-3.5" />
              {s.label}
            </button>
          ))}
        </nav>
      </div>

      <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(300px,340px)]">
        {/* Summary column. First in the DOM so a stacked layout leads with
            it; sent to the right on wide windows, where it sticks. */}
        <div className="min-w-0 space-y-4 xl:sticky xl:top-18 xl:order-2">
          <section id="sec-modelos" className="scroll-mt-24">
            <ModelCostsPanel />
          </section>
          {placement && onPlacementChange ? (
            <NavigationPreference placement={placement} onChange={onPlacementChange} />
          ) : null}
        </div>

        {/* Records column — tables, which want the width. */}
        <div className="min-w-0 space-y-4 xl:order-1">
          <section id="sec-tokens" className="scroll-mt-24">
            <ProvidersPanel />
          </section>
          <section id="sec-agentes" className="scroll-mt-24">
            <AgentsPanel />
          </section>
        </div>
      </div>
    </div>
  );
}

/** Where the Chat / Configuração switcher lives — a per-browser preference. */
function NavigationPreference({
  placement,
  onChange,
}: {
  placement: NavPlacement;
  onChange: (p: NavPlacement) => void;
}) {
  return (
    <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card)">
      <h2 className="text-sm font-semibold text-(--color-foreground)">Navegação</h2>
      <p className="mt-0.5 text-xs leading-relaxed text-(--color-muted-foreground)">
        Onde mostrar o seletor <strong>Chat / Configuração</strong> — no header ou numa coluna
        lateral.
      </p>
      <div className="mt-2 grid grid-cols-2 gap-1 rounded-xl border border-(--color-border) bg-(--color-background) p-1">
        {(["header", "sidebar"] as const).map((opt) => (
          <button
            key={opt}
            type="button"
            aria-pressed={placement === opt}
            onClick={() => onChange(opt)}
            className={cn(
              "rounded-lg px-3 py-1.5 text-xs font-medium transition-colors duration-200",
              placement === opt
                ? "bg-(--color-muted) text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            {opt === "header" ? "Header" : "Sidebar"}
          </button>
        ))}
      </div>
    </div>
  );
}
