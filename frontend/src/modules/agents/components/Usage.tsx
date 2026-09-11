import { cn } from "@/lib/utils";
import { formatBRL, formatTokens, formatUSD } from "@/modules/agents/format";
import type { UsageReport } from "@/modules/agents/api/usage";
import { costStateOf } from "@/modules/agents/cost";
import { useT } from "@/lib/i18n";

/**
 * How money and tokens read across the module.
 *
 * ── The one rule ───────────────────────────────────────────────────────
 * A cost nobody knows must never render as zero. The backend stopped
 * conflating the two: a turn whose rate card could not be read
 * carries no cost at all and is counted in `unpriced_messages`, and
 * `priced` is false exactly when that count is above zero.
 *
 * That gives three states, and they look different on purpose:
 *
 *   priced, some turns          →  ~R$ 0,84        a complete figure
 *   priced, no turns at all     →  ~R$ 0,00        a real zero
 *   unpriced, nothing costed    →  desconhecido    NOT zero
 *   unpriced, partly costed     →  ≥ ~R$ 0,84      a floor, with the gap named
 *
 * The tilde is the estimate marker the module has always used, and it is
 * kept off the billed figures shown on the Providers screen.
 */

function money(usd: number, rate: number | null): string {
  return rate != null ? formatBRL(usd * rate) : formatUSD(usd);
}

/**
 * The cost of a report, in one line, honest about what it does not know.
 *
 * `rate` is the USD→BRL rate when the browser managed to fetch one; null
 * falls back to dollars rather than guessing a conversion.
 */
export function Cost({
  report,
  rate,
  loading,
  className,
}: {
  report: UsageReport | undefined;
  rate: number | null;
  loading?: boolean;
  className?: string;
}) {
  const t = useT();
  const base = cn("tabular-nums", className);

  if (loading) {
    return <span className={cn(base, "text-(--color-muted-foreground)")}>…</span>;
  }
  if (!report) {
    return (
      <span className={cn(base, "text-(--color-muted-foreground)")} title={t.app.modules.agents.fx.noUsageData}>
        —
      </span>
    );
  }

  const state = costStateOf(report);

  if (state === "unknown") {
    return (
      <span
        className={cn(base, "text-(--color-muted-foreground)")}
        title={
          report.messages === 0
            ? "Nenhum turno neste período."
            : `${report.unpriced_messages} turno(s) sem preço registrado. Custo desconhecido — não é zero.`
        }
      >
        {report.messages === 0 ? `~${money(0, rate)}` : "desconhecido"}
      </span>
    );
  }

  return (
    <span
      className={base}
      title={
        state === "partial"
          ? `Piso: ${report.unpriced_messages} de ${report.messages} turnos não têm custo registrado, então o total real é maior.`
          : `${report.messages} turno(s), todos com custo registrado.`
      }
    >
      {state === "partial" ? "≥ " : ""}~{money(report.estimated_cost, rate)}
    </span>
  );
}

/**
 * The line that says how much of a figure is missing. Renders nothing when
 * nothing is missing, so a complete report carries no apology.
 */
export function UnpricedNote({
  report,
  className,
}: {
  report: UsageReport | undefined;
  className?: string;
}) {
  if (!report || report.unpriced_messages === 0) return null;
  return (
    <p className={cn("text-[10.5px] leading-relaxed text-(--color-muted-foreground)", className)}>
      {report.unpriced_messages} de {report.messages}{" "}
      {report.messages === 1 ? "turno" : "turnos"} sem custo conhecido.
    </p>
  );
}

/**
 * Tokens and turn count. Always exact — these come from the counts each
 * turn recorded, and a turn the gateway never reported contributes zero
 * tokens *and* is counted in `unknown_usage_messages`, which the note below
 * surfaces rather than hiding inside the total.
 */
export function TokenFigures({
  report,
  loading,
  className,
}: {
  report: UsageReport | undefined;
  loading?: boolean;
  className?: string;
}) {
  if (loading) {
    return <span className={cn("text-(--color-muted-foreground)", className)}>…</span>;
  }
  if (!report) return <span className={cn("text-(--color-muted-foreground)", className)}>—</span>;
  return (
    <span className={cn("tabular-nums", className)}>
      {formatTokens(report.total_tokens)} tokens · {report.messages}{" "}
      {report.messages === 1 ? "resposta" : "respostas"}
    </span>
  );
}

/**
 * The note for turns whose consumption is not merely unpriced but unknown:
 * the call may never have reached the model, so its tokens are absent from
 * the total rather than counted as zero.
 */
export function UnknownUsageNote({
  report,
  className,
}: {
  report: UsageReport | undefined;
  className?: string;
}) {
  if (!report || report.unknown_usage_messages === 0) return null;
  return (
    <p className={cn("text-[10.5px] leading-relaxed text-(--color-muted-foreground)", className)}>
      {report.unknown_usage_messages}{" "}
      {report.unknown_usage_messages === 1 ? "turno não reportou" : "turnos não reportaram"} consumo.
      Os tokens deles não entram na soma.
    </p>
  );
}

/**
 * A headline number with its label, the shape the Home and the agent's
 * usage page both use.
 */
export function Figure({
  label,
  value,
  hint,
  className,
}: {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <p className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
        {label}
      </p>
      <p className="mt-1 truncate text-xl font-semibold leading-none tabular-nums text-(--color-foreground)">
        {value}
      </p>
      {hint ? <div className="mt-1">{hint}</div> : null}
    </div>
  );
}
