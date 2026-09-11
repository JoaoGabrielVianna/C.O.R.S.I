import { useState } from "react";
import { Brain } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import type { ApiAgent, ApiMemoryPolicy } from "@/modules/agents/api/agents";
import { MAX_MEMORY_POLICY_NOTES } from "@/modules/agents/memoryPolicy";
import { useUpdateAgent } from "@/modules/agents/hooks/useAgents";
import { useT } from "@/lib/i18n";

/**
 * What this agent may be asked to do with its own memory.
 *
 * ── Why it is a card and not a field in AgentForm ──────────────────────
 * Instructions say who the agent is and how it answers. This says what may
 * end up being remembered about the person talking to it, across every
 * future conversation. Folding a knowledge policy into the personality
 * textarea would mean the two are edited, reviewed and reasoned about as
 * one thing, and they are not — the same reason the daily limit has a card
 * of its own.
 *
 * ── Why the guidance box is small and the rules are not shown ──────────
 * What deserves to be remembered is the product's rule and it lives in the
 * backend: stable preferences, long-term goals, decisions, recurring
 * constraints — and not small talk, not the temporary, not speculation.
 * The box here is an addendum to that, and saying so is what stops it from
 * being read as "write your own policy or there is none".
 */
export function MemoryPolicyCard({ agent }: { agent: ApiAgent }) {
  const t = useT();
  const updateAgent = useUpdateAgent();

  const [mode, setMode] = useState<ApiMemoryPolicy["mode"]>(agent.memory_policy.mode);
  const [notes, setNotes] = useState(agent.memory_policy.notes);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const tooLong = notes.length > MAX_MEMORY_POLICY_NOTES;

  const save = async () => {
    setError(null);
    setSaved(false);
    try {
      // Sent whole. There is no partial policy worth expressing between
      // "leave it alone" (omit the field, which every other save does) and
      // "here is the policy".
      await updateAgent.mutateAsync({
        id: agent.id,
        body: { memory_policy: { mode, notes } },
      });
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };

  return (
    <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card) sm:p-5">
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-(--color-brand-50) text-(--color-brand-700)">
          <Brain className="size-3.5" />
        </span>
        <div className="min-w-0">
          <h2 className="text-[13px] font-semibold text-(--color-foreground)">
            {t.app.modules.agents.memory.policy.title}
          </h2>
          <p className="mt-1 max-w-xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.memory.policy.descriptionLead}{" "}
            <strong className="font-medium text-(--color-foreground)">
              {t.app.modules.agents.memory.policy.descriptionEmphasis}
            </strong>{" "}
            {t.app.modules.agents.memory.policy.policyTail}
          </p>
        </div>
      </div>

      <fieldset className="mt-4 space-y-2">
        <legend className="mb-2 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {t.app.modules.agents.memory.policy.consolidation}
        </legend>
        <ModeOption
          value="on_request"
          checked={mode === "on_request"}
          onSelect={setMode}
          label={t.app.modules.agents.memory.policy.onDemand}
          hint={t.app.modules.agents.memory.policy.onDemandHint}
        />
        <ModeOption
          value="off"
          checked={mode === "off"}
          onSelect={setMode}
          label={t.app.modules.agents.memory.policy.off}
          hint={t.app.modules.agents.memory.policy.offHint}
        />
      </fieldset>

      <div className="mt-4">
        <label
          htmlFor="memory-policy-notes"
          className="block text-[12.5px] font-medium text-(--color-foreground)"
        >
          {t.app.modules.agents.memory.policy.extraGuidance}
        </label>
        <p className="mt-0.5 text-[11px] leading-relaxed text-(--color-muted-foreground)">
          {t.app.modules.agents.memory.policy.extraGuidanceHint}
        </p>
        <textarea
          id="memory-policy-notes"
          rows={3}
          value={notes}
          disabled={mode === "off"}
          onChange={(e) => setNotes(e.target.value)}
          placeholder={t.app.modules.agents.memory.policy.extraGuidancePlaceholder}
          className={cn(
            "mt-2 w-full resize-y rounded-xl border border-(--color-border) bg-(--color-background) px-3 py-2",
            "text-[12.5px] leading-relaxed text-(--color-foreground) outline-none",
            "placeholder:text-(--color-muted-foreground)",
            "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
            "disabled:cursor-not-allowed disabled:opacity-50",
          )}
        />
        <p
          className={cn(
            "mt-1 text-right font-mono text-[10px] tabular-nums",
            tooLong ? "text-(--color-destructive)" : "text-(--color-muted-foreground)",
          )}
        >
          {notes.length}/{MAX_MEMORY_POLICY_NOTES}
        </p>
      </div>

      {error ? <p className="mt-2 text-[11.5px] text-(--color-destructive)">{error}</p> : null}

      <div className="mt-3 flex items-center gap-3">
        <Button size="sm" onClick={() => void save()} disabled={updateAgent.isPending || tooLong}>
          {updateAgent.isPending ? "Salvando…" : "Salvar política"}
        </Button>
        {saved ? (
          <span className="text-[11.5px] text-(--color-muted-foreground)">{t.app.modules.agents.memory.policy.saved}</span>
        ) : null}
      </div>
    </div>
  );
}

function ModeOption({
  value,
  checked,
  onSelect,
  label,
  hint,
}: {
  value: ApiMemoryPolicy["mode"];
  checked: boolean;
  onSelect: (mode: ApiMemoryPolicy["mode"]) => void;
  label: string;
  hint: string;
}) {
  return (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-2.5 rounded-xl border px-3 py-2.5 transition-colors",
        checked
          ? "border-(--color-brand-500) bg-(--color-brand-50)/40"
          : "border-(--color-border) hover:bg-(--color-muted)",
      )}
    >
      <input
        type="radio"
        name="memory-policy-mode"
        value={value}
        checked={checked}
        onChange={() => onSelect(value)}
        className="mt-0.5 size-3.5 shrink-0 accent-(--color-accent)"
      />
      <span className="min-w-0">
        <span className="block text-[12.5px] font-medium text-(--color-foreground)">{label}</span>
        <span className="mt-0.5 block text-[11px] leading-relaxed text-(--color-muted-foreground)">
          {hint}
        </span>
      </span>
    </label>
  );
}
