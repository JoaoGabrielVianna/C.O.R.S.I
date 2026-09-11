import { Link } from "react-router-dom";
import { Gauge } from "lucide-react";
import { Button } from "@/components/ui/Button";
import type { ApiError } from "@/lib/api/client";
import type { BudgetBlockReason } from "@/modules/agents/api/agents";
import { useAgentBudget } from "@/modules/agents/hooks/useBudget";
import { BudgetMeter } from "./BudgetMeter";
import { useT } from "@/lib/i18n";

/**
 * A turn the system refused under a limit its own user set.
 *
 * ── Why it does not look like an error ─────────────────────────────────
 * Nothing failed. The provider was never called, no request was lost, and
 * the question is still in the composer. Dressing this in the red of an
 * outage would teach the reader that their own budget is a malfunction.
 *
 * ── What it has to contain ─────────────────────────────────────────────
 * The reason, where the day stands, and the way out. A refusal that only
 * says "blocked" leaves someone hunting through Settings for a number they
 * cannot see.
 */

export function BudgetRefusal({
  error,
  agentId,
  onDismiss,
}: {
  error: ApiError;
  agentId: string;
  onDismiss: () => void;
}) {
  const t = useT();
  // The standing is fetched rather than parsed out of the message: the
  // numbers belong to the API, and a client that re-derived them would be a
  // second opinion about the same budget.
  const budget = useAgentBudget(agentId);
  const reason = error.code as BudgetBlockReason;

  return (
    <div className="mb-2 rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2.5 shadow-(--shadow-card)">
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-lg bg-(--color-muted) text-(--color-foreground)">
          <Gauge className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-[12.5px] font-medium text-(--color-foreground)">
            {reason === "pricing_unavailable"
              ? t.app.modules.agents.budget.refusalPricing
              : t.app.modules.agents.budget.refusalReached}
          </p>
          <p className="mt-0.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {error.message}
          </p>
          <p className="mt-1 text-[11px] text-(--color-muted-foreground)">
            {t.app.modules.agents.budget.refusalBody}
          </p>
        </div>
        <Button size="sm" variant="ghost" onClick={onDismiss}>
          {t.app.modules.agents.budget.close}
        </Button>
      </div>

      {budget.data?.status.enabled ? (
        <div className="mt-2.5 border-t border-(--color-border) pt-2.5">
          <BudgetMeter status={budget.data} />
        </div>
      ) : null}

      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" asChild>
          <Link to={`/app/modules/agents/${agentId}/settings`}>{t.app.modules.agents.budget.adjustLimit}</Link>
        </Button>
        <span className="text-[11px] text-(--color-muted-foreground)">
          {t.app.modules.agents.budget.orWait}
        </span>
      </div>
    </div>
  );
}
