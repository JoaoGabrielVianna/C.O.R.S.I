import { AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ApiBudgetStatus } from "@/modules/agents/api/agents";
import { formatTokens, formatUSD } from "@/modules/agents/format";
import { useT } from "@/lib/i18n";

/**
 * Today's consumption against today's limits.
 *
 * ── Why a bar only when there is a limit ───────────────────────────────
 * A progress bar with no ceiling is decoration: it either fills to an
 * arbitrary maximum or never moves. With no limit configured this shows the
 * number and says so in words, which is the honest version of the same
 * information.
 *
 * ── Why money can refuse to show a total ───────────────────────────────
 * When the day contains turns nobody could price, the figure is a floor and
 * not a total. It is labelled "conhecido" and the gap is counted beside it.
 * Rendering `$0` for "we do not know" is the one thing this component must
 * never do — the whole accounting chain exists to keep those
 * two apart.
 */
export function BudgetMeter({
  status,
  className,
}: {
  status: ApiBudgetStatus;
  className?: string;
}) {
  const t = useT();
  const { usage, status: state } = status;

  return (
    <div className={cn("space-y-2", className)}>
      <Row
        label={t.app.modules.agents.budget.tokens}
        value={formatTokens(usage.tokens)}
        limit={state.tokens ? formatTokens(state.tokens.limit) : null}
        percent={state.tokens?.percent ?? null}
        tone={state.tokens?.state}
      />
      <Row
        label={t.app.modules.agents.budget.cost}
        value={usage.unpriced_turns > 0 ? `${formatUSD(usage.known_cost_usd)} conhecido` : formatUSD(usage.known_cost_usd)}
        limit={state.cost ? formatUSD(state.cost.limit) : null}
        percent={state.cost?.percent ?? null}
        tone={state.cost?.state}
        note={
          usage.unpriced_turns > 0
            ? `${usage.unpriced_turns} ${usage.unpriced_turns === 1 ? "turno sem preço" : "turnos sem preço"}`
            : undefined
        }
      />

      {state.blocked ? (
        <p className="flex items-start gap-1.5 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-2.5 py-1.5 text-[11px] leading-relaxed text-(--color-destructive)">
          <AlertTriangle className="mt-0.5 size-3 shrink-0" />
          {t.app.modules.agents.budget.reached}
        </p>
      ) : null}
    </div>
  );
}

function Row({
  label,
  value,
  limit,
  percent,
  tone,
  note,
}: {
  label: string;
  value: string;
  /** Null when nothing is being enforced on this dimension. */
  limit: string | null;
  percent: number | null;
  tone?: "disabled" | "ok" | "warning" | "blocked";
  note?: string;
}) {
  const t = useT();
  return (
    <div>
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[11.5px] text-(--color-foreground)">{label}</span>
        <span className="font-mono text-[11px] tabular-nums text-(--color-foreground)">
          {value}
          {limit ? (
            <span className="text-(--color-muted-foreground)"> / {limit}</span>
          ) : (
            <span className="ml-1.5 text-[10px] text-(--color-muted-foreground)">
              {t.app.modules.agents.budget.noLimit}
            </span>
          )}
        </span>
      </div>

      {limit && percent !== null ? (
        <div className="mt-1 h-1 overflow-hidden rounded-full bg-(--color-muted)">
          <div
            className={cn(
              "h-full rounded-full transition-[width] duration-300",
              tone === "blocked"
                ? "bg-(--color-destructive)"
                : tone === "warning"
                  ? "bg-(--color-brand-600)"
                  : "bg-(--color-brand-500)",
            )}
            // Clamped at the top only: an overshoot fills the bar rather
            // than running past it, and the number beside it still says
            // what really happened.
            style={{ width: `${Math.min(Math.max(percent, 0), 1) * 100}%` }}
          />
        </div>
      ) : null}

      {note ? (
        <p className="mt-0.5 text-[10px] text-(--color-muted-foreground)">{note}</p>
      ) : null}
    </div>
  );
}
