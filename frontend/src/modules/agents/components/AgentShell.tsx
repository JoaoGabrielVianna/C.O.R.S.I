import { NavLink, Outlet, useParams } from "react-router-dom";
import {
  ArrowLeft,
  BookText,
  Bot,
  MessagesSquare,
  Brain,
  Settings2,
  Wrench,
  BarChart3,
} from "lucide-react";
import { Button } from "@/components/ui/Button";
import { useT } from "@/lib/i18n";
import type { Translations } from "@/lib/i18n/pt";
import { cn } from "@/lib/utils";
import type { ApiAgent } from "@/modules/agents/api/agents";
import { ModelBadge } from "@/modules/agents/components/ModelBadge";
import { useAgents } from "@/modules/agents/hooks/useAgents";
import { useProviders } from "@/modules/agents/hooks/useProviders";
import { AgentBudgetLine } from "@/modules/agents/components/budget/AgentBudgetLine";

/**
 * Everything inside an agent hangs off this shell: it is what makes the
 * agent a place rather than a row in a table.
 *
 * ── Why no Overview page ───────────────────────────────────────────────
 * Between "I picked my agent" and "I am talking to it" an overview screen
 * inserts a stop whose only useful control is "open a conversation". Ten
 * times a day that is a toll, not information. The header carries the
 * numbers an overview would have carried — name, purpose, which model — and
 * `/:agentId` redirects straight to the conversations. If a summary screen
 * ever earns its place, it is one line: stop redirecting.
 *
 * ── Why a horizontal subnav ────────────────────────────────────────────
 * Both a rail and tabs were live options. Tabs won on the surface that
 * matters most: the conversations view already spends horizontal space on a
 * thread column beside the transcript, and a permanent left rail would take
 * a third bite out of the same axis. The subnav scrolls sideways below `sm`
 * instead of collapsing, so no destination is ever unreachable.
 */

/**
 * One destination inside an agent.
 *
 * There used to be a `planned` flag here, for destinations that existed in
 * the navigation and not in the system. Tools was the last one carrying it,
 * and it was built — so the flag went with the placeholder it labelled.
 * Every tab in this list now leads somewhere real.
 */
type SectionKey = keyof Translations["app"]["modules"]["agents"]["shell"]["sections"];

type Section = {
  to: string;
  /** Key into the dictionary, not the copy itself. */
  label: SectionKey;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
};

/**
 * The route and the icon are structure and stay here; the label is copy and
 * lives in the dictionary. Before this, the six tabs were written in two
 * languages at once — "Conversas" beside "Memory" beside "Uso" — which is
 * what a nav looks like when nobody owns its words.
 */
const SECTIONS: Section[] = [
  { to: "conversations", label: "conversations", icon: MessagesSquare },
  { to: "memory", label: "memory", icon: Brain },
  { to: "sources", label: "sources", icon: BookText },
  { to: "tools", label: "tools", icon: Wrench },
  { to: "usage", label: "usage", icon: BarChart3 },
  { to: "settings", label: "settings", icon: Settings2 },
];

export function AgentShell() {
  const t = useT();
  const { agentId } = useParams<{ agentId: string }>();
  const agentsQuery = useAgents();
  const providersQuery = useProviders();

  const agent = agentsQuery.data?.find((a) => a.id === agentId);

  if (agentsQuery.isLoading) {
    return <ShellMessage title={t.app.modules.agents.common.loading} />;
  }
  if (agentsQuery.isError) {
    return (
      <ShellMessage
        title={t.app.modules.agents.shell.loadFailed}
        description={
          agentsQuery.error instanceof Error
            ? agentsQuery.error.message
            : t.app.modules.agents.common.retry
        }
      />
    );
  }
  // A URL can name an agent that was deleted, or one that belongs to
  // somebody else's workspace — the server answers both the same way, and
  // so does this. An id in a URL is never authorisation.
  if (!agent) {
    return (
      <ShellMessage
        title={t.app.modules.agents.shell.notFound.title}
        description={t.app.modules.agents.shell.notFound.description}
      />
    );
  }

  const provider = providersQuery.data?.find((p) => p.id === agent.provider_id);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <AgentHeader agent={agent} providerName={provider?.name ?? null} />
      <nav
        aria-label={t.app.modules.agents.shell.navLabel}
        className="-mx-1 flex shrink-0 items-center gap-1 overflow-x-auto px-1 pb-0.5"
      >
        {SECTIONS.map((s) => (
          <NavLink
            key={s.to}
            to={s.to}
            className={({ isActive }) =>
              cn(
                "inline-flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium",
                "transition-colors duration-[250ms] [transition-timing-function:var(--ease-premium)]",
                isActive
                  ? "bg-(--color-muted) text-(--color-foreground)"
                  : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
              )
            }
          >
            <s.icon className="size-3.5" />
            {t.app.modules.agents.shell.sections[s.label]}
          </NavLink>
        ))}
      </nav>

      <Outlet context={{ agent } satisfies AgentContext} />
    </div>
  );
}

/** What every section under the shell receives, via `useOutletContext`. */
export interface AgentContext {
  agent: ApiAgent;
}

function AgentHeader({ agent, providerName }: { agent: ApiAgent; providerName: string | null }) {
  const t = useT();
  return (
    <header className="flex shrink-0 flex-wrap items-start justify-between gap-3 border-b border-(--color-border) pb-3">
      <div className="flex min-w-0 items-start gap-3">
        <span className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl bg-(--color-brand-50) text-(--color-brand-700)">
          <Bot className="size-4.5" />
        </span>
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <NavLink
              to="/app/modules/agents"
              aria-label={t.app.modules.agents.shell.backLabel}
              className="rounded-md p-0.5 text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
            >
              <ArrowLeft className="size-3.5" />
            </NavLink>
            {/* break-words, not truncate: a long agent name is the page's
                own title and hiding half of it helps nobody. */}
            <h1 className="min-w-0 break-words font-display text-xl font-semibold tracking-tight text-(--color-foreground)">
              {agent.name}
            </h1>
          </div>
          {agent.description ? (
            <p className="mt-0.5 line-clamp-2 max-w-2xl text-[12.5px] leading-relaxed text-(--color-muted-foreground)">
              {agent.description}
            </p>
          ) : null}
          <div className="mt-1.5">
            <ModelBadge model={agent.model} provider={providerName} />
          </div>
          {/* Where the day stands, on every screen of the agent — because
              "can I keep going today?" is asked while working, not while
              visiting Settings. Absent entirely when no limit is set: a
              meter with no ceiling is decoration. */}
          <AgentBudgetLine agentId={agent.id} />
        </div>
      </div>

      <Button size="sm" variant="outline" asChild>
        <NavLink to="conversations">
          <MessagesSquare />
          {t.app.modules.agents.shell.sections.conversations}
        </NavLink>
      </Button>
    </header>
  );
}

function ShellMessage({ title, description }: { title: string; description?: string }) {
  const t = useT();
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center">
      <span className="flex size-11 items-center justify-center rounded-xl bg-(--color-muted)">
        <Bot className="size-5 text-(--color-muted-foreground)" />
      </span>
      <p className="text-sm font-medium text-(--color-foreground)">{title}</p>
      {description ? (
        <p className="max-w-sm text-xs leading-relaxed text-(--color-muted-foreground)">
          {description}
        </p>
      ) : null}
      <Button size="sm" variant="outline" asChild>
        <NavLink to="/app/modules/agents">{t.app.modules.agents.common.backToAgents}</NavLink>
      </Button>
    </div>
  );
}
