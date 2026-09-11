import { Link } from "react-router-dom";
import { cn } from "@/lib/utils";
import { formatTokens, formatUSD } from "@/modules/agents/format";
import { useAgentBudget } from "@/modules/agents/hooks/useBudget";
import { useT } from "@/lib/i18n";

/**
 * One line in the agent header: how much of today's limit is gone.
 *
 * Renders nothing at all when the agent has no limit configured. That is
 * deliberate — a header that permanently carried "0 / no limit" would spend
 * attention on a number nobody set, and the same figures are already on the
 * agent's usage page for anyone who wants them.
 *
 * At 80% it starts saying so without stopping anything; at 100% it says the
 * turns are being refused. Neither is a rule — the rule lives in the
 * backend and this only reports what it decided.
 */
export function AgentBudgetLine({ agentId }: { agentId: string }) {
  const t = useT();
  const query = useAgentBudget(agentId);
  const data = query.data;
  if (!data?.status.enabled) return null;

  const { tokens, cost, blocked } = data.status;
  const worst = [tokens, cost].filter(Boolean).reduce<number>(
    (max, d) => Math.max(max, d!.percent),
    0,
  );

  return (
    <Link
      to={`/app/modules/agents/${agentId}/settings`}
      className={cn(
        "mt-1.5 inline-flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg px-1.5 py-0.5",
        "font-mono text-[10.5px] tabular-nums transition-colors",
        blocked
          ? "bg-(--color-destructive)/10 text-(--color-destructive)"
          : worst >= 0.8
            ? "bg-(--color-muted) text-(--color-foreground)"
            : "text-(--color-muted-foreground) hover:bg-(--color-muted)",
      )}
      title={t.app.modules.agents.budget.lineTitle}
    >
      <span className="uppercase tracking-wider opacity-70">{t.app.modules.agents.budget.todayShort}</span>
      {tokens ? (
        <span>
          {formatTokens(data.usage.tokens)}/{formatTokens(tokens.limit)} tok
        </span>
      ) : null}
      {cost ? (
        <span>
          {formatUSD(data.usage.known_cost_usd)}/{formatUSD(cost.limit)}
        </span>
      ) : null}
      {data.usage.unpriced_turns > 0 ? (
        <span className="opacity-70">
          {t.app.modules.agents.interp.unpricedTurns.replace("{count}", String(data.usage.unpriced_turns))}
        </span>
      ) : null}
      {blocked ? <span className="font-semibold">{t.app.modules.agents.budget.blocked}</span> : null}
    </Link>
  );
}
