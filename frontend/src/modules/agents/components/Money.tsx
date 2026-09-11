import { cn } from "@/lib/utils";
import { formatBRL, formatUSD } from "@/modules/agents/format";
import type { useUsdToBrl } from "@/modules/agents/hooks/useFxRate";
import { useT } from "@/lib/i18n";

/**
 * The money cell: reais on top, dollars underneath — or dollars alone when
 * the exchange rate is unavailable. `approx` prefixes an estimate with ~.
 *
 * Shared by the token rows (real billed spend) and the agent rows (estimate),
 * so both read the same way and only the tilde tells them apart.
 */
export function Money({
  usd,
  rate,
  approx,
  className,
}: {
  usd: number;
  rate: number | null;
  approx?: boolean;
  className?: string;
}) {
  const tilde = approx ? "~" : "";
  if (rate == null) {
    return (
      <p className={cn("text-sm font-semibold tabular-nums text-(--color-foreground)", className)}>
        {tilde}
        {formatUSD(usd)}
      </p>
    );
  }
  return (
    <>
      <p className={cn("text-sm font-semibold tabular-nums text-(--color-foreground)", className)}>
        {tilde}
        {formatBRL(usd * rate)}
      </p>
      <p className="text-[11px] tabular-nums text-(--color-muted-foreground)">
        {tilde}
        {formatUSD(usd)}
      </p>
    </>
  );
}

/**
 * Shows which rate the reais figures used, so the number is never a black
 * box — and says plainly when it fell back to dollars.
 */
export function FxFooter({ fx }: { fx: ReturnType<typeof useUsdToBrl> }) {
  const t = useT();
  if (fx.isLoading) {
    return <p className="text-[11px] text-(--color-muted-foreground)">{t.app.modules.agents.fx.loading}</p>;
  }
  if (fx.data) {
    return (
      <p className="text-[11px] text-(--color-muted-foreground)">
        Câmbio: US$ 1 = {formatBRL(fx.data.rate)}
        {fx.data.asOf ? ` · ${fx.data.asOf}` : ""} · fonte {fx.data.source}
      </p>
    );
  }
  return (
    <p className="text-[11px] text-(--color-muted-foreground)">
      {t.app.modules.agents.fx.unavailable}
    </p>
  );
}
