import { useMemo } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Bot, KeyRound, MessagesSquare, Plus } from "lucide-react";
import { PageHeader } from "@/components/workspace";
import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { ApiAgent } from "@/modules/agents/api/agents";
import type { ApiConversation } from "@/modules/agents/api/conversations";
import { ModelBadge } from "@/modules/agents/components/ModelBadge";
import { Cost, Figure, UnpricedNote } from "@/modules/agents/components/Usage";
import { formatTokens } from "@/modules/agents/format";
import { useAgents } from "@/modules/agents/hooks/useAgents";
import {
  useConversationCounts,
  useCreateConversation,
  useRecentConversations,
} from "@/modules/agents/hooks/useConversations";
import { useUsdToBrl } from "@/modules/agents/hooks/useFxRate";
import { useProviders } from "@/modules/agents/hooks/useProviders";
import { useAgentsUsage, useWorkspaceUsage, usageWindow } from "@/modules/agents/hooks/useUsage";

/**
 * The module's front door.
 *
 * It answers five questions and stops: which agents do I have, which one am
 * I opening, what was I last working on, what has today cost, and how do I
 * start something new. Everything on it is either a way in or a number that
 * changes daily — no charts, no history, no panel that has to be studied.
 *
 * ── It does not open anything for you ──────────────────────────────────
 * Arriving used to drop you straight into whichever conversation had moved
 * last. Convenient for resuming, wrong for starting, and it made the module
 * feel like it had no beginning. The recent list keeps the resuming; the
 * choice is now yours to make.
 *
 * ── Where the numbers come from ────────────────────────────────────────
 * All of it is server-side aggregation over figures each turn froze when it
 * ran. "Hoje" is `GET /chat/usage` over a window starting at this
 * browser's local midnight; the per-agent figure is the same call scoped to
 * an agent; the conversation counts are the `total` of a filtered list.
 * Nothing is summed in the browser over a page of rows, because a total
 * computed from a truncated list is a total that lies quietly.
 */
export function AgentsHomePage() {
  const t = useT();
  const navigate = useNavigate();
  const agentsQuery = useAgents();
  const providersQuery = useProviders();
  const recentQuery = useRecentConversations();
  const createConversation = useCreateConversation();
  const fx = useUsdToBrl();
  const rate = fx.data?.rate ?? null;

  // Recomputed once per mount, not per render: a fresh Date in the query
  // key would make the query key change on every render.
  const today = useMemo(() => usageWindow("today"), []);
  const workspaceUsage = useWorkspaceUsage(today);

  const agents = useMemo(() => agentsQuery.data ?? [], [agentsQuery.data]);
  const agentIds = useMemo(() => agents.map((a) => a.id), [agents]);
  const perAgentUsage = useAgentsUsage(agentIds, today);
  const counts = useConversationCounts(agentIds);

  const providers = providersQuery.data ?? [];
  const agentsById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const recent = recentQuery.data?.items ?? [];

  const loading = agentsQuery.isLoading || providersQuery.isLoading;

  /**
   * Starting a conversation from the Home needs an agent. With exactly one
   * there is nothing to ask, so it opens straight into a new thread; with
   * several, choosing the agent IS the question, and the cards below are
   * already that choice.
   */
  const startConversation = async () => {
    if (agents.length !== 1) return;
    try {
      const created = await createConversation.mutateAsync({ agentId: agents[0].id });
      navigate(`/app/modules/agents/${agents[0].id}/c/${created.id}`);
    } catch {
      // The mutation carries the error; surfaced under the actions below
      // rather than thrown into an unhandled rejection, which is what the
      // previous version did.
    }
  };

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-1 flex-col gap-6">
      <PageHeader
        className="shrink-0"
        eyebrow={t.app.modules.agents.home.eyebrow}
        title={t.app.modules.agents.home.title}
        description={t.app.modules.agents.home.description}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" asChild>
              <Link to="providers">
                <KeyRound />
                {t.app.modules.agents.home.providersCta}
              </Link>
            </Button>
            {agents.length === 1 ? (
              <Button size="sm" variant="outline" onClick={() => void startConversation()}>
                <MessagesSquare />
                {t.app.modules.agents.home.newConversation}
              </Button>
            ) : null}
            {/* Not a disabled link — an anchor cannot be disabled, and one
                styled to look it is a dead end. Without a provider the page
                below explains what to do first, so the button simply is not
                offered yet. */}
            {providers.length > 0 ? (
              <Button size="sm" asChild>
                <Link to="new">
                  <Plus />
                  {t.app.modules.agents.home.newAgent}
                </Link>
              </Button>
            ) : null}
          </div>
        }
      />

      {createConversation.isError ? (
        <p className="text-xs text-(--color-destructive)">
          {t.app.modules.agents.home.createFailed} {createConversation.error.message}
        </p>
      ) : null}

      <TodayStrip
        report={workspaceUsage.data}
        loading={workspaceUsage.isLoading}
        failed={workspaceUsage.isError}
        rate={rate}
      />

      <section className="space-y-3">
        <h2 className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {t.app.modules.agents.home.yourAgents}
        </h2>

        {loading ? (
          <p className="text-xs text-(--color-muted-foreground)">{t.app.modules.agents.common.loading}</p>
        ) : providers.length === 0 ? (
          <FirstRun />
        ) : agents.length === 0 ? (
          <NoAgents />
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {agents.map((agent, i) => (
              <AgentCard
                key={agent.id}
                agent={agent}
                providerName={
                  providers.find((p) => p.id === agent.provider_id)?.name ?? null
                }
                conversationCount={counts.get(agent.id)}
                usage={perAgentUsage[i]?.data}
                usageLoading={perAgentUsage[i]?.isLoading ?? true}
                rate={rate}
              />
            ))}
          </div>
        )}
      </section>

      {recent.length > 0 ? (
        <RecentConversations conversations={recent} agentsById={agentsById} />
      ) : null}
    </div>
  );
}

/**
 * Today, across the workspace.
 *
 * No progress bars: a bar without a ceiling is decoration, and limits do
 * not exist yet. When they do, this is where they attach.
 */
function TodayStrip({
  report,
  loading,
  failed,
  rate,
}: {
  report: ReturnType<typeof useWorkspaceUsage>["data"];
  loading: boolean;
  failed: boolean;
  rate: number | null;
}) {
  const t = useT();
  return (
    <section className="rounded-2xl border border-(--color-border) bg-(--color-card) px-4 py-3.5 shadow-(--shadow-card)">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="grid flex-1 grid-cols-2 gap-4 sm:grid-cols-3">
          <Figure
            label={t.app.modules.agents.home.today.spend}
            value={<Cost report={report} rate={rate} loading={loading} />}
            hint={<UnpricedNote report={report} />}
          />
          <Figure
            label={t.app.modules.agents.home.today.tokens}
            value={loading ? "…" : report ? formatTokens(report.total_tokens) : "—"}
          />
          <Figure
            label={t.app.modules.agents.home.today.messages}
            value={loading ? "…" : report ? String(report.messages) : "—"}
          />
        </div>
      </div>
      {failed ? (
        <p className="mt-2 text-[11px] text-(--color-muted-foreground)">
          {t.app.modules.agents.home.today.failed}
        </p>
      ) : null}
    </section>
  );
}

function AgentCard({
  agent,
  providerName,
  conversationCount,
  usage,
  usageLoading,
  rate,
}: {
  agent: ApiAgent;
  providerName: string | null;
  conversationCount: number | undefined;
  usage: ReturnType<typeof useAgentsUsage>[number]["data"];
  usageLoading: boolean;
  rate: number | null;
}) {
  const t = useT();
  const fmt = useFormat();
  return (
    <Link
      to={agent.id}
      className={cn(
        "group flex min-w-0 flex-col gap-2.5 rounded-2xl border border-(--color-border) bg-(--color-card) p-4",
        "shadow-(--shadow-card) transition-[transform,border-color] duration-[250ms]",
        "[transition-timing-function:var(--ease-premium)]",
        "hover:-translate-y-px hover:border-(--color-brand-500)",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
      )}
    >
      <div className="flex min-w-0 items-start gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-(--color-brand-50) text-(--color-brand-700)">
          <Bot className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          {/* Two lines, then clipped: a card is a handle, not the record. */}
          <p className="line-clamp-2 text-[13.5px] font-medium leading-snug text-(--color-foreground)">
            {agent.name}
          </p>
          {agent.description ? (
            <p className="mt-0.5 line-clamp-2 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {agent.description}
            </p>
          ) : null}
        </div>
      </div>

      <ModelBadge model={agent.model} provider={providerName} className="self-start" />

      <div className="mt-auto flex items-baseline justify-between gap-2 border-t border-(--color-border) pt-2.5 text-[11px]">
        <span className="text-(--color-muted-foreground)">
          {conversationCount === undefined
            ? "—"
            : fmt.plural(conversationCount, t.app.modules.agents.common.conversationCount)}
        </span>
        <span className="text-right">
          <Cost
            report={usage}
            rate={rate}
            loading={usageLoading}
            className="font-medium text-(--color-foreground)"
          />
          <span className="ml-1 text-(--color-muted-foreground)">{t.app.modules.agents.common.today}</span>
        </span>
      </div>
    </Link>
  );
}

/**
 * Cross-agent, read-only, and short. It exists for one move — getting back
 * into what you were doing — and deliberately carries no rename or delete:
 * managing a thread happens inside the agent that owns it, where the rest
 * of that agent's threads are visible for comparison.
 */
function RecentConversations({
  conversations,
  agentsById,
}: {
  conversations: ApiConversation[];
  agentsById: Map<string, ApiAgent>;
}) {
  const t = useT();
  return (
    <section className="space-y-2">
      <h2 className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {t.app.modules.agents.home.recent}
      </h2>
      <ul className="overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)">
        {conversations.map((c) => {
          const agent = agentsById.get(c.agent_id);
          return (
            <li key={c.id} className="border-b border-(--color-border) last:border-b-0">
              {agent ? (
                <Link
                  to={`${agent.id}/c/${c.id}`}
                  className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-(--color-muted)/50"
                >
                  <MessagesSquare className="size-3.5 shrink-0 text-(--color-muted-foreground)" />
                  <span className="min-w-0 flex-1 truncate text-[12.5px] text-(--color-foreground)">
                    {c.title || t.app.modules.agents.common.untitled}
                  </span>
                  <span className="shrink-0 truncate text-[11px] text-(--color-muted-foreground)">
                    {agent.name}
                  </span>
                </Link>
              ) : (
                // The agent is gone; the thread has nowhere to open into,
                // and saying so beats a link that lands on an error.
                <div className="flex items-center gap-3 px-4 py-2.5 opacity-60">
                  <MessagesSquare className="size-3.5 shrink-0 text-(--color-muted-foreground)" />
                  <span className="min-w-0 flex-1 truncate text-[12.5px] text-(--color-foreground)">
                    {c.title || t.app.modules.agents.common.untitled}
                  </span>
                  <span className="shrink-0 text-[11px] text-(--color-muted-foreground)">
                    {t.app.modules.agents.common.agentRemoved}
                  </span>
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** Day zero: a provider has to exist before an agent can have one. */
function FirstRun() {
  const t = useT();
  return (
    <div className="rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-10 text-center">
      <span className="mx-auto flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
        <KeyRound className="size-4" />
      </span>
      <p className="mt-3 text-sm font-medium text-(--color-foreground)">
        {t.app.modules.agents.home.firstRun.title}
      </p>
      <p className="mx-auto mt-1 max-w-sm text-xs leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.home.firstRun.body}
      </p>
      <div className="mt-4">
        <Button size="sm" asChild>
          <Link to="providers">{t.app.modules.agents.home.firstRun.cta}</Link>
        </Button>
      </div>
    </div>
  );
}

function NoAgents() {
  const t = useT();
  return (
    <div className="rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-10 text-center">
      <span className="mx-auto flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
        <Bot className="size-4" />
      </span>
      <p className="mt-3 text-sm font-medium text-(--color-foreground)">{t.app.modules.agents.home.noAgents.title}</p>
      <p className="mx-auto mt-1 max-w-sm text-xs leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.home.noAgents.body}
      </p>
      <div className="mt-4">
        <Button size="sm" asChild>
          <Link to="new">{t.app.modules.agents.home.noAgents.cta}</Link>
        </Button>
      </div>
    </div>
  );
}
