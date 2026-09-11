import { useState } from "react";
import { Gauge } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import type { ApiAgent, ApiBudget } from "@/modules/agents/api/agents";
import { useUpdateAgent } from "@/modules/agents/hooks/useAgents";
import { useAgentBudget } from "@/modules/agents/hooks/useBudget";
import { BudgetMeter } from "./BudgetMeter";
import { useT } from "@/lib/i18n";

/**
 * The daily limits of one agent, and where the day stands.
 *
 * ── Why this is its own card and not a field in AgentForm ──────────────
 * Everything in AgentForm changes how the agent *behaves*. These two change
 * whether it is allowed to run at all. Mixing an enforcement control into a
 * form of tuning knobs is how someone raises a temperature slider and
 * accidentally clears a spending limit.
 *
 * ── Why USD, with no BRL field ─────────────────────────────────────────
 * The gateway prices and bills in dollars, so a dollar is the only figure
 * that means the same thing tomorrow. A limit typed in reais would be a
 * conversion frozen as if it were a fact, and the rate that produced it
 * would be stale by the next day. The module already shows BRL where money
 * is *read*; a limit is money being *decided*, and that stays in USD.
 */
export function BudgetCard({ agent }: { agent: ApiAgent }) {
  const t = useT();
  const budgetQuery = useAgentBudget(agent.id);
  const updateAgent = useUpdateAgent();

  const [tokensOn, setTokensOn] = useState(agent.budget.daily_token_limit !== null);
  const [costOn, setCostOn] = useState(agent.budget.daily_cost_limit_usd !== null);
  const [tokenValue, setTokenValue] = useState(
    agent.budget.daily_token_limit !== null ? String(agent.budget.daily_token_limit) : "100000",
  );
  const [costValue, setCostValue] = useState(
    agent.budget.daily_cost_limit_usd !== null ? String(agent.budget.daily_cost_limit_usd) : "2.00",
  );
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const save = async () => {
    setError(null);
    setSaved(false);
    const budget: ApiBudget = {
      daily_token_limit: tokensOn ? Number(tokenValue) : null,
      daily_cost_limit_usd: costOn ? Number(costValue) : null,
    };
    if (
      (budget.daily_token_limit !== null && !Number.isFinite(budget.daily_token_limit)) ||
      (budget.daily_cost_limit_usd !== null && !Number.isFinite(budget.daily_cost_limit_usd))
    ) {
      setError("Informe um número válido.");
      return;
    }
    try {
      await updateAgent.mutateAsync({ id: agent.id, body: { budget } });
      setSaved(true);
      void budgetQuery.refetch();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };

  return (
    <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card) sm:p-5">
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-(--color-brand-50) text-(--color-brand-700)">
          <Gauge className="size-3.5" />
        </span>
        <div className="min-w-0">
          <h2 className="text-[13px] font-semibold text-(--color-foreground)">{t.app.modules.agents.budget.dailyLimit}</h2>
          <p className="mt-1 max-w-xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.budget.cardLead}{" "}
            <strong className="font-medium text-(--color-foreground)">UTC</strong>
            {t.app.modules.agents.budget.utcNote}
          </p>
        </div>
      </div>

      {budgetQuery.data ? (
        <div className="mt-4 rounded-xl border border-(--color-border) bg-(--color-background) px-3 py-2.5">
          <p className="mb-2 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
            {t.app.modules.agents.budget.today}
          </p>
          <BudgetMeter status={budgetQuery.data} />
        </div>
      ) : null}

      <div className="mt-4 space-y-3">
        <LimitField
          label={t.app.modules.agents.budget.tokenLimit}
          hint={t.app.modules.agents.budget.tokenLimitHint}
          enabled={tokensOn}
          onToggle={setTokensOn}
          value={tokenValue}
          onChange={setTokenValue}
          inputMode="numeric"
          suffix="tokens"
        />
        <LimitField
          label={t.app.modules.agents.budget.spendLimit}
          hint={t.app.modules.agents.budget.spendLimitHint}
          enabled={costOn}
          onToggle={setCostOn}
          value={costValue}
          onChange={setCostValue}
          inputMode="decimal"
          prefix="US$"
        />
      </div>

      {error ? (
        <p className="mt-3 text-[11.5px] text-(--color-destructive)">{error}</p>
      ) : null}

      <div className="mt-4 flex items-center gap-3">
        <Button size="sm" onClick={() => void save()} disabled={updateAgent.isPending}>
          {updateAgent.isPending ? "Salvando…" : "Salvar limites"}
        </Button>
        {saved ? (
          <span className="text-[11.5px] text-(--color-muted-foreground)">
            {t.app.modules.agents.budget.saved}
          </span>
        ) : null}
      </div>
    </div>
  );
}

function LimitField({
  label,
  hint,
  enabled,
  onToggle,
  value,
  onChange,
  inputMode,
  prefix,
  suffix,
}: {
  label: string;
  hint: string;
  enabled: boolean;
  onToggle: (on: boolean) => void;
  value: string;
  onChange: (v: string) => void;
  inputMode: "numeric" | "decimal";
  prefix?: string;
  suffix?: string;
}) {
  return (
    <div className="rounded-xl border border-(--color-border) px-3 py-2.5">
      <label className="flex cursor-pointer items-start gap-2.5">
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => onToggle(e.target.checked)}
          className="mt-0.5 size-3.5 shrink-0 accent-(--color-accent)"
        />
        <span className="min-w-0">
          <span className="block text-[12.5px] font-medium text-(--color-foreground)">{label}</span>
          <span className="mt-0.5 block text-[11px] leading-relaxed text-(--color-muted-foreground)">
            {hint}
          </span>
        </span>
      </label>

      <div className={cn("mt-2 flex items-center gap-1.5 pl-6", !enabled && "opacity-40")}>
        {prefix ? (
          <span className="font-mono text-[11px] text-(--color-muted-foreground)">{prefix}</span>
        ) : null}
        <input
          value={value}
          inputMode={inputMode}
          disabled={!enabled}
          onChange={(e) => onChange(e.target.value)}
          className={cn(
            "h-8 w-32 rounded-lg border border-(--color-border) bg-(--color-card) px-2.5",
            "font-mono text-[12px] tabular-nums text-(--color-foreground) outline-none",
            "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
            "disabled:cursor-not-allowed",
          )}
        />
        {suffix ? (
          <span className="font-mono text-[11px] text-(--color-muted-foreground)">{suffix}</span>
        ) : null}
      </div>
    </div>
  );
}
