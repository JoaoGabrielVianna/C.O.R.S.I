import { BarChart3 } from "lucide-react";
import { useFormat, useT } from "@/lib/i18n";
import { useAgents } from "@/modules/agents/hooks/useAgents";
import { useAgentsUsage } from "@/modules/agents/hooks/useUsage";
import { useUsdToBrl } from "@/modules/agents/hooks/useFxRate";
import { FxFooter } from "@/modules/agents/components/Money";
import { ModelIcon } from "@/modules/agents/components/ModelIcon";
import { modelBrand, shortModelName } from "@/modules/agents/models";
import { formatBRL, formatTokens, formatUSD } from "@/modules/agents/format";
import type { UsageReport } from "@/modules/agents/api/usage";

/**
 * Custo por modelo.
 *
 * The third axis of the same data: Provedores answers "what did this key bill",
 * Agentes answers "what did this agent cost", and this answers "where is the
 * money going" — the one question neither row can, because a model is used
 * across agents and a single agent's cost hides which model burned it.
 *
 * Nothing here re-lists tokens or agents; it reads the per-agent reports
 * already in the cache (same query keys as the agent rows, so no extra
 * fetch) and regroups their lines by model.
 *
 * One series, so one color — bar length is the whole encoding, and the bar
 * hue is the same for every model. Identity rides on the brand glyph and the
 * label beside it, never on color, which is what keeps three different blue
 * brands from reading as the same thing.
 */

type ModelTotal = {
  model: string;
  cost: number;
  tokens: number;
  messages: number;
  /** How many of those turns recorded no cost at all. */
  unpricedMessages: number;
};

/**
 * Counted per turn, not per report.
 *
 * An earlier version marked a whole model unpriced when any report it
 * appeared in was, which turned one unknown turn into a dash over a figure
 * that was mostly known. The backend reports the count per line, so the
 * distinction between "none of this is known" and "some of this is
 * missing" survives all the way to the bar.
 */
function aggregate(reports: Array<UsageReport | undefined>): ModelTotal[] {
  const byModel = new Map<string, ModelTotal>();
  for (const report of reports) {
    if (!report) continue;
    for (const line of report.lines) {
      const entry = byModel.get(line.model) ?? {
        model: line.model,
        cost: 0,
        tokens: 0,
        messages: 0,
        unpricedMessages: 0,
      };
      entry.cost += line.cost;
      entry.tokens += line.total_tokens;
      entry.messages += line.messages;
      entry.unpricedMessages += line.unpriced_messages;
      byModel.set(line.model, entry);
    }
  }
  return [...byModel.values()].sort((a, b) => b.cost - a.cost || b.tokens - a.tokens);
}

/** Nothing costed at all — the figure is unknown, and unknown is not zero. */
const isUnknownCost = (t: ModelTotal) => t.unpricedMessages >= t.messages && t.messages > 0;

export function ModelCostsPanel() {
  const t = useT();
  const fmt = useFormat();
  const agentsQuery = useAgents();
  const agents = agentsQuery.data ?? [];
  const reports = useAgentsUsage(agents.map((a) => a.id));
  const fx = useUsdToBrl();
  const rate = fx.data?.rate ?? null;

  const loading = agentsQuery.isLoading || reports.some((r) => r.isLoading);
  const totals = aggregate(reports.map((r) => r.data));
  const grandCost = totals.reduce((acc, t) => acc + t.cost, 0);
  const grandTokens = totals.reduce((acc, t) => acc + t.tokens, 0);
  const max = Math.max(...totals.map((t) => t.cost), 0);
  // How much of the headline is missing, and whether any of it is known.
  const grandUnpriced = totals.reduce((acc, t) => acc + t.unpricedMessages, 0);
  const grandMessages = totals.reduce((acc, t) => acc + t.messages, 0);
  const grandUnknown = grandMessages > 0 && grandUnpriced >= grandMessages;

  return (
    <section className="overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)">
      <header className="border-b border-(--color-border) px-4 py-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-(--color-foreground)">
          <BarChart3 className="size-4 text-(--color-muted-foreground)" />
          {t.app.modules.agents.modelCosts.title}
        </h2>
        <p className="mt-0.5 text-xs leading-relaxed text-(--color-muted-foreground)">
          {t.app.modules.agents.modelCosts.description}
        </p>
      </header>

      {loading ? (
        <p className="px-4 py-8 text-center text-xs text-(--color-muted-foreground)">{t.app.modules.agents.common.loading}</p>
      ) : totals.length === 0 ? (
        <div className="px-4 py-8 text-center">
          <BarChart3 className="mx-auto size-5 text-(--color-muted-foreground)" />
          <p className="mt-2 text-sm text-(--color-foreground)">{t.app.modules.agents.modelCosts.empty.title}</p>
          <p className="mx-auto mt-1 max-w-xs text-xs leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.modelCosts.empty.body}
          </p>
        </div>
      ) : (
        <>
          {/* The headline these bars add up to. */}
          <div className="border-b border-(--color-border) bg-(--color-muted)/40 px-4 py-4">
            <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {t.app.modules.agents.modelCosts.totalEstimated}
            </p>
            <p className="mt-1 text-3xl font-semibold leading-none tabular-nums text-(--color-foreground)">
              {grandUnknown
                ? t.app.modules.agents.modelCosts.unknown
                : `${grandUnpriced > 0 ? "≥ " : ""}~${rate != null ? formatBRL(grandCost * rate) : formatUSD(grandCost)}`}
            </p>
            {grandUnpriced > 0 ? (
              <p className="mt-1 text-[10.5px] leading-relaxed text-(--color-muted-foreground)">
                {t.app.modules.agents.modelCosts.unpriced
                  .replace("{unpriced}", fmt.number(grandUnpriced))
                  .replace("{total}", fmt.number(grandMessages))}
              </p>
            ) : null}
            <div className="mt-2 grid grid-cols-3 gap-2 border-t border-(--color-border) pt-2">
              <Stat label={t.app.modules.agents.modelCosts.inUsd} value={rate != null ? `~${formatUSD(grandCost)}` : "—"} />
              <Stat label={t.app.modules.agents.modelCosts.tokens} value={formatTokens(grandTokens)} />
              <Stat
                label={fmt.plural(totals.length, t.app.modules.agents.modelCosts.modelCount)}
                value={fmt.number(totals.length)}
              />
            </div>
          </div>

          <ul className="space-y-3.5 px-4 py-4">
            {totals.map((t) => (
              <ModelBar
                key={t.model}
                total={t}
                max={max}
                grandCost={grandCost}
                rate={rate}
              />
            ))}
          </ul>

          <footer className="border-t border-(--color-border) px-4 py-2">
            <FxFooter fx={fx} />
          </footer>
        </>
      )}
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="truncate text-sm font-semibold tabular-nums text-(--color-foreground)">
        {value}
      </p>
      <p className="truncate font-mono text-[9.5px] uppercase tracking-[0.12em] text-(--color-muted-foreground)">
        {label}
      </p>
    </div>
  );
}

function ModelBar({
  total,
  max,
  grandCost,
  rate,
}: {
  total: ModelTotal;
  max: number;
  grandCost: number;
  rate: number | null;
}) {
  const t = useT();
  const fmt = useFormat();
  // A model with recorded tokens but no price still deserves a visible row,
  // so an unpriced/zero-cost bar keeps a minimum stub.
  const pct = max > 0 ? Math.max(3, (total.cost / max) * 100) : 3;
  const share = grandCost > 0 ? Math.round((total.cost / grandCost) * 100) : 0;
  const brand = modelBrand(total.model);
  const money = rate != null ? formatBRL(total.cost * rate) : formatUSD(total.cost);
  const unknown = isUnknownCost(total);
  // Partly known: the figure is a floor, and the "≥" says so rather than
  // presenting an understatement as the total.
  const floor = !unknown && total.unpricedMessages > 0;

  return (
    <li className="group">
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-3">
        <span className="flex min-w-0 items-center gap-1.5">
          <ModelIcon model={total.model} className="size-4" />
          <span
            className="truncate text-[12.5px] font-medium text-(--color-foreground)"
            title={`${total.model} · ${brand.label}`}
          >
            {shortModelName(total.model)}
          </span>
        </span>
        <span
          className={
            unknown
              ? "text-sm font-semibold tabular-nums text-(--color-muted-foreground)"
              : "text-sm font-semibold tabular-nums text-(--color-foreground)"
          }
          title={
            total.unpricedMessages > 0
              ? t.app.modules.agents.modelCosts.unpriced
                  .replace("{unpriced}", fmt.number(total.unpricedMessages))
                  .replace("{total}", fmt.number(total.messages))
              : undefined
          }
        >
          {unknown ? t.app.modules.agents.modelCosts.unknown : `${floor ? "≥ " : ""}~${money}`}
        </span>
      </div>

      <div
        className="mt-1.5 h-2.5 w-full overflow-hidden rounded-full bg-(--color-muted)"
        title={t.app.modules.agents.modelCosts.barTitle
          .replace("{model}", total.model)
          .replace(
            "{cost}",
            unknown ? t.app.modules.agents.modelCosts.unknownCost : `${floor ? "≥ " : ""}~${money}`,
          )
          .replace("{tokens}", formatTokens(total.tokens))
          .replace("{messages}", fmt.number(total.messages))}
      >
        <div
          className="h-full rounded-full bg-(--color-brand-500) transition-[width] duration-500 [transition-timing-function:var(--ease-premium)] dark:bg-(--color-brand-600)"
          style={{ width: `${pct}%` }}
        />
      </div>

      <div className="mt-1 flex items-baseline justify-between gap-2 font-mono text-[10px] tabular-nums text-(--color-muted-foreground)">
        <span>
          {formatTokens(total.tokens)} tokens · {total.messages} msgs
        </span>
        <span>{unknown ? t.app.modules.agents.modelCosts.noPrice : `${share}%`}</span>
      </div>
    </li>
  );
}
