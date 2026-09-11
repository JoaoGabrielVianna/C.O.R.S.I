import { useMemo, useState } from "react";
import { useOutletContext } from "react-router-dom";
import { BarChart3 } from "lucide-react";
import { cn } from "@/lib/utils";
import type { UsageLine } from "@/modules/agents/api/usage";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { FxFooter } from "@/modules/agents/components/Money";
import { ModelIcon } from "@/modules/agents/components/ModelIcon";
import { Cost, Figure, UnknownUsageNote, UnpricedNote } from "@/modules/agents/components/Usage";
import { costStateOf } from "@/modules/agents/cost";
import { formatBRL, formatTokens, formatUSD } from "@/modules/agents/format";
import { shortModelName } from "@/modules/agents/models";
import { useUsdToBrl } from "@/modules/agents/hooks/useFxRate";
import { useT } from "@/lib/i18n";
import {
  USAGE_PERIOD_LABEL,
  useAgentUsage,
  usageWindow,
  type UsagePeriod,
} from "@/modules/agents/hooks/useUsage";

/**
 * What this agent consumed, over a period.
 *
 * ── Only what the backend can answer ───────────────────────────────────
 * Every figure here is `GET /chat/agents/{id}/usage` with a window. Cost is
 * the sum of what each turn froze when it ran, not a re-pricing against
 * today's rate card, so reading last week next month returns the same
 * number. There is no daily series and no chart, because the API exposes
 * totals over a window and inventing a curve out of them would be drawing,
 * not measuring.
 *
 * ── Limits are not here ────────────────────────────────────────────────
 * Budgets do not exist: no schema, no enforcement, nothing to configure.
 * The page says so once, plainly, rather than showing a disabled control
 * that implies the feature is a toggle away.
 */

const PERIODS: UsagePeriod[] = ["today", "7d", "30d", "all"];

export function AgentUsagePage() {
  const t = useT();
  const { agent } = useOutletContext<AgentContext>();
  const [period, setPeriod] = useState<UsagePeriod>("today");

  // The window is derived from the period, and the period only changes on
  // a click — so the query key is stable between renders.
  const window = useMemo(() => usageWindow(period), [period]);
  const usage = useAgentUsage(agent.id, window);
  const fx = useUsdToBrl();
  const rate = fx.data?.rate ?? null;

  const report = usage.data;
  const lines = [...(report?.lines ?? [])].sort((a, b) => b.cost - a.cost || b.total_tokens - a.total_tokens);
  const maxCost = Math.max(...lines.map((l) => l.cost), 0);

  return (
    <section className="mx-auto min-h-0 w-full max-w-3xl space-y-4 overflow-y-auto pb-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-(--color-foreground)">{t.app.modules.agents.usage.title}</h2>
        <div className="flex items-center gap-1 rounded-xl border border-(--color-border) bg-(--color-card) p-1">
          {PERIODS.map((p) => (
            <button
              key={p}
              type="button"
              aria-pressed={period === p}
              onClick={() => setPeriod(p)}
              className={cn(
                "rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors duration-200",
                period === p
                  ? "bg-(--color-muted) text-(--color-foreground)"
                  : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
              )}
            >
              {USAGE_PERIOD_LABEL[p]}
            </button>
          ))}
        </div>
      </div>

      <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card)">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Figure
            label={t.app.modules.agents.usage.cost}
            value={<Cost report={report} rate={rate} loading={usage.isLoading} />}
          />
          <Figure
            label={t.app.modules.agents.usage.tokens}
            value={usage.isLoading ? "…" : report ? formatTokens(report.total_tokens) : "—"}
          />
          <Figure
            label={t.app.modules.agents.usage.input}
            value={usage.isLoading ? "…" : report ? formatTokens(report.total_prompt_tokens) : "—"}
          />
          <Figure
            label={t.app.modules.agents.usage.replies}
            value={usage.isLoading ? "…" : report ? String(report.messages) : "—"}
          />
        </div>

        <div className="mt-3 space-y-1 border-t border-(--color-border) pt-2.5">
          <UnpricedNote report={report} />
          <UnknownUsageNote report={report} />
          {report && costStateOf(report) === "known" && report.messages > 0 ? (
            <p className="text-[10.5px] leading-relaxed text-(--color-muted-foreground)">
              {t.app.modules.agents.usage.description}
            </p>
          ) : null}
        </div>
      </div>

      {usage.isError ? (
        <p className="text-xs text-(--color-destructive)">
          {t.app.modules.agents.interp.usageReadFailed}{" "}
          {usage.error instanceof Error ? usage.error.message : t.app.modules.agents.interp.unknownError}
        </p>
      ) : null}

      <div className="overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)">
        <header className="border-b border-(--color-border) px-4 py-3">
          <h3 className="flex items-center gap-2 text-[13px] font-semibold text-(--color-foreground)">
            <BarChart3 className="size-4 text-(--color-muted-foreground)" />
            {t.app.modules.agents.usage.perModel}
          </h3>
          <p className="mt-0.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.usage.perModelNote}
          </p>
        </header>

        {usage.isLoading ? (
          <p className="px-4 py-8 text-center text-xs text-(--color-muted-foreground)">
            {t.app.modules.agents.common.loading}
          </p>
        ) : lines.length === 0 ? (
          <p className="px-4 py-8 text-center text-xs leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.usage.emptyPeriod}
          </p>
        ) : (
          <ul className="space-y-3.5 px-4 py-4">
            {lines.map((line) => (
              <ModelLine key={line.model} line={line} max={maxCost} rate={rate} />
            ))}
          </ul>
        )}

        {lines.length > 0 ? (
          <footer className="border-t border-(--color-border) px-4 py-2">
            <FxFooter fx={fx} />
          </footer>
        ) : null}
      </div>

      {/* Stated once, where someone would look for it. Not a disabled
          control: there is nothing behind it to enable. */}
      <p className="rounded-xl border border-dashed border-(--color-border) px-4 py-3 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
        <strong className="text-(--color-foreground)">{t.app.modules.agents.usage.limitsTitle}</strong>{" "}
        {t.app.modules.agents.usage.limitsLead} <span className="font-mono">max_budget</span>{" "}
        {t.app.modules.agents.usage.limitsTail} {t.app.modules.agents.usage.limitsProviders}
      </p>
    </section>
  );
}

function ModelLine({ line, max, rate }: { line: UsageLine; max: number; rate: number | null }) {
  // A model with tokens but no known cost still deserves a visible row, so
  // the bar keeps a minimum stub instead of vanishing.
  const pct = max > 0 ? Math.max(3, (line.cost / max) * 100) : 3;
  const unknownCost = line.unpriced_messages >= line.messages;
  const money = rate != null ? formatBRL(line.cost * rate) : formatUSD(line.cost);

  return (
    <li>
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-3">
        <span className="flex min-w-0 items-center gap-1.5">
          <ModelIcon model={line.model} className="size-4" />
          <span
            className="truncate text-[12.5px] font-medium text-(--color-foreground)"
            title={line.model}
          >
            {shortModelName(line.model)}
          </span>
        </span>
        <span
          className={cn(
            "text-sm font-semibold tabular-nums",
            unknownCost ? "text-(--color-muted-foreground)" : "text-(--color-foreground)",
          )}
          title={
            line.unpriced_messages > 0
              ? `${line.unpriced_messages} de ${line.messages} turnos sem custo registrado.`
              : undefined
          }
        >
          {unknownCost
            ? "desconhecido"
            : `${line.unpriced_messages > 0 ? "≥ " : ""}~${money}`}
        </span>
      </div>

      <div className="mt-1.5 h-2 w-full overflow-hidden rounded-full bg-(--color-muted)">
        <div
          className="h-full rounded-full bg-(--color-brand-500) transition-[width] duration-500 [transition-timing-function:var(--ease-premium)] dark:bg-(--color-brand-600)"
          style={{ width: `${pct}%` }}
        />
      </div>

      <p className="mt-1 font-mono text-[10px] tabular-nums text-(--color-muted-foreground)">
        {formatTokens(line.total_tokens)} tokens · {line.messages}{" "}
        {line.messages === 1 ? "resposta" : "respostas"}
        {line.estimated_messages > 0 ? ` · ${line.estimated_messages} estimada(s)` : ""}
      </p>
    </li>
  );
}
