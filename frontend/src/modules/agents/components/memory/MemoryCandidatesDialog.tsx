import { useState } from "react";
import { AlertTriangle, Brain, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import { MAX_MEMORY_CHARS, type ApiMemoryCandidates } from "@/modules/agents/api/memories";
import { useT } from "@/lib/i18n";

/**
 * What the agent found worth remembering, and what the user does with it.
 *
 * ── The rule this dialog exists to enforce ─────────────────────────────
 * The model proposes, the user confirms, the backend persists. Nothing was
 * saved to get here and nothing is saved by closing it. Every proposal is
 * editable before it is accepted, because a sentence a model wrote about
 * you is exactly the kind of thing worth correcting before it follows you
 * into every future conversation.
 *
 * ── Why the cost is on screen ──────────────────────────────────────────
 * This is the only thing in the product that spends money without
 * producing a visible answer. Hiding what it cost would make it the one
 * operation a user cannot govern, so the figure sits at the bottom —
 * discreet, and never rounded down to zero when it is unknown.
 */

export type ConsolidationPhase =
  | { status: "loading" }
  | { status: "ready"; data: ApiMemoryCandidates }
  | { status: "error"; message: string; kind: "policy" | "budget" | "other" };

interface Props {
  agentName: string;
  phase: ConsolidationPhase;
  /** True while the confirmation is being saved. */
  saving?: boolean;
  saveError?: string | null;
  onConfirm: (contents: string[], upToSeq: number) => void;
  onCancel: () => void;
  /** Opens the agent's settings, where the policy lives. */
  onOpenSettings: () => void;
}

interface Draft {
  content: string;
  reason: string;
  duplicate: boolean;
  selected: boolean;
}

function draftsFrom(data: ApiMemoryCandidates): Draft[] {
  return data.candidates.map((c) => ({
    content: c.content,
    reason: c.reason,
    duplicate: c.duplicate_of !== null,
    // A duplicate arrives unselected: the agent already knows it, so the
    // default action is to do nothing. It is still shown, and still
    // editable, because hiding it would leave the user wondering why the
    // model missed something obvious.
    selected: c.duplicate_of === null,
  }));
}

export function MemoryCandidatesDialog({
  agentName,
  phase,
  saving,
  saveError,
  onConfirm,
  onCancel,
  onOpenSettings,
}: Props) {
  const t = useT();
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div aria-hidden onClick={onCancel} className="absolute inset-0 bg-black/40 backdrop-blur-sm" />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={t.app.modules.agents.memory.candidates.title}
        className={cn(
          "relative flex max-h-[85vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl",
          "border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)",
        )}
      >
        <header className="flex shrink-0 items-start gap-2.5 px-4 pt-4 sm:px-5 sm:pt-5">
          <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-(--color-brand-50) text-(--color-brand-700)">
            <Brain className="size-3.5" />
          </span>
          <div className="min-w-0">
            <h2 className="text-[13px] font-semibold text-(--color-foreground)">
              {t.app.modules.agents.memory.candidates.title}
            </h2>
            <p className="mt-1 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {t.app.modules.agents.interp.candidatesLead.replace("{agent}", agentName)}
            </p>
          </div>
        </header>

        {phase.status === "loading" ? <Loading /> : null}
        {phase.status === "error" ? (
          <Failure phase={phase} onOpenSettings={onOpenSettings} />
        ) : null}
        {phase.status === "ready" ? (
          <Ready
            data={phase.data}
            saving={saving}
            saveError={saveError}
            onConfirm={onConfirm}
            onCancel={onCancel}
          />
        ) : null}

        {phase.status !== "ready" ? (
          <footer className="flex shrink-0 justify-end gap-2 border-t border-(--color-border) px-4 py-3 sm:px-5">
            <Button size="sm" variant="outline" onClick={onCancel}>
              {t.app.modules.agents.memory.candidates.close}
            </Button>
          </footer>
        ) : null}
      </div>
    </div>
  );
}

function Loading() {
  const t = useT();
  return (
    <div className="flex items-center gap-2 px-4 py-8 text-[12.5px] text-(--color-muted-foreground) sm:px-5">
      <Loader2 className="size-4 animate-spin" />
      <span>{t.app.modules.agents.memory.candidates.analysing}</span>
    </div>
  );
}

function Failure({
  phase,
  onOpenSettings,
}: {
  phase: Extract<ConsolidationPhase, { status: "error" }>;
  onOpenSettings: () => void;
}) {
  const t = useT();
  return (
    <div className="px-4 py-5 sm:px-5">
      <div className="flex items-start gap-2 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2.5">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-(--color-destructive)" />
        <p className="flex-1 text-[11.5px] leading-relaxed text-(--color-destructive)">
          {phase.message}
        </p>
      </div>
      {/* A refusal by policy is not a failure: it is a setting doing what it
          was set to do, so the way out is offered instead of only the bad
          news. */}
      {phase.kind === "policy" ? (
        <Button size="sm" variant="outline" className="mt-3" onClick={onOpenSettings}>
          {t.app.modules.agents.memory.candidates.openSettings}
        </Button>
      ) : null}
    </div>
  );
}

function Ready({
  data,
  saving,
  saveError,
  onConfirm,
  onCancel,
}: {
  data: ApiMemoryCandidates;
  saving?: boolean;
  saveError?: string | null;
  onConfirm: (contents: string[], upToSeq: number) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [drafts, setDrafts] = useState<Draft[]>(() => draftsFrom(data));

  const update = (i: number, patch: Partial<Draft>) =>
    setDrafts((current) => current.map((d, j) => (j === i ? { ...d, ...patch } : d)));

  const chosen = drafts.filter((d) => d.selected && d.content.trim() !== "");
  const tooLong = chosen.some((d) => d.content.length > MAX_MEMORY_CHARS);

  if (data.candidates.length === 0) {
    return (
      <>
        <div className="px-4 py-6 sm:px-5">
          <p className="text-[12.5px] leading-relaxed text-(--color-foreground)">
            {t.app.modules.agents.memory.candidates.nothingFound}
          </p>
          <p className="mt-1.5 text-[11px] leading-relaxed text-(--color-muted-foreground)">
            {data.considered_messages > 0
              ? `Analisei ${data.considered_messages} ${data.considered_messages === 1 ? "mensagem" : "mensagens"}.`
              : "Esta conversa ainda não tem nada para analisar."}
          </p>
        </div>
        <UsageLine data={data} />
      </>
    );
  }

  return (
    <>
      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto px-4 py-4 sm:px-5">
        {drafts.map((d, i) => (
          <div
            key={i}
            className={cn(
              "rounded-xl border px-3 py-2.5 transition-colors",
              d.selected ? "border-(--color-brand-500)" : "border-(--color-border)",
            )}
          >
            <label className="flex cursor-pointer items-start gap-2.5">
              <input
                type="checkbox"
                checked={d.selected}
                aria-label={`Salvar: ${d.content}`}
                onChange={(e) => update(i, { selected: e.target.checked })}
                className="mt-1 size-3.5 shrink-0 accent-(--color-accent)"
              />
              <span className="min-w-0 flex-1">
                <textarea
                  value={d.content}
                  rows={2}
                  aria-label={`Texto da memória ${i + 1}`}
                  onChange={(e) => update(i, { content: e.target.value })}
                  className={cn(
                    "w-full resize-y rounded-lg border border-transparent bg-transparent px-1 py-0.5",
                    "text-[12.5px] leading-relaxed text-(--color-foreground) outline-none",
                    "focus:border-(--color-border) focus:bg-(--color-background)",
                  )}
                />
                <span className="mt-1 block text-[11px] leading-relaxed text-(--color-muted-foreground)">
                  {d.reason}
                </span>
                {d.duplicate ? (
                  <span className="mt-1 inline-flex items-center rounded-md bg-(--color-muted) px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.12em] text-(--color-muted-foreground)">
                    {t.app.modules.agents.memory.candidates.alreadyStored}
                  </span>
                ) : null}
              </span>
            </label>
          </div>
        ))}
      </div>

      {saveError ? (
        <p className="px-4 pb-1 text-[11.5px] text-(--color-destructive) sm:px-5">{saveError}</p>
      ) : null}

      <UsageLine data={data} />

      <footer className="flex shrink-0 items-center justify-between gap-2 border-t border-(--color-border) px-4 py-3 sm:px-5">
        <span className="text-[11.5px] text-(--color-muted-foreground)">
          {chosen.length} {chosen.length === 1 ? "selecionada" : "selecionadas"}
        </span>
        <span className="flex gap-2">
          <Button size="sm" variant="outline" onClick={onCancel} disabled={saving}>
            {t.app.modules.agents.memory.candidates.cancel}
          </Button>
          <Button
            size="sm"
            disabled={chosen.length === 0 || tooLong || saving}
            onClick={() =>
              onConfirm(
                chosen.map((d) => d.content.trim()),
                data.effective_up_to_seq,
              )
            }
          >
            {saving ? "Salvando…" : "Salvar memórias"}
          </Button>
        </span>
      </footer>
    </>
  );
}

/**
 * What the analysis cost, in the place the act happened.
 *
 * An unknown cost is written as unknown. Rendering it as `US$ 0,00` would
 * be the one lie this line exists to prevent — the module distinguishes
 * "free" from "we could not read the price" everywhere else, and a dialog
 * is not the place to start collapsing them.
 */
function UsageLine({ data }: { data: ApiMemoryCandidates }) {
  if (!data.usage) return null;
  const { usage } = data;
  const tokens = usage.prompt_tokens + usage.completion_tokens;
  return (
    <p className="shrink-0 border-t border-(--color-border) px-4 py-2 font-mono text-[10px] text-(--color-muted-foreground) sm:px-5">
      {tokens} tokens · {usage.model} ·{" "}
      {usage.cost_usd === null ? "custo desconhecido" : `US$ ${usage.cost_usd.toFixed(4)}`}
      {usage.usage_source === "estimated" ? " · estimado" : ""}
    </p>
  );
}
