import { useEffect } from "react";
import { AlertTriangle, Gauge, X } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ApiMessage } from "@/modules/agents/api/conversations";
import {
  blockLabel,
  blockRows,
  exclusionRows,
  roundRows,
  toolErrorLabel,
  turnCost,
  turnUsage,
  warnings,
} from "@/modules/agents/contextReport";
import { formatTokens, formatUSD } from "@/modules/agents/format";
import { useFormat } from "@/lib/i18n";
import { useT } from "@/lib/i18n";

/**
 * What this turn actually received, and what it cost.
 *
 * ── It describes one turn, in the past ─────────────────────────────────
 * Everything here comes from the report stamped on the message when it ran.
 * Nothing is recomputed from the agent's current configuration, which is
 * why a turn answered last week still reports last week's instructions,
 * memories and sources — including the ones that have since been deleted.
 *
 * That also sets the limit of what it can offer: it can say *how much*
 * memory a turn carried, never *which* memories, because the report holds
 * counts and no references. Linking out to today's Memory page would be
 * offering "what it knows now" while appearing to answer "what it used
 * then". Those are different promises and this panel makes only the second.
 *
 * ── A drawer, not a modal ──────────────────────────────────────────────
 * Reading this is an act of comparing it against the answer beside it. A
 * centred modal would cover exactly the thing being explained.
 */
export function ContextInspector({
  message,
  agentName,
  onClose,
}: {
  message: ApiMessage;
  agentName?: string;
  onClose: () => void;
}) {
  const t = useT();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const report = message.context_report;
  const usage = turnUsage(message);
  const cost = turnCost(message);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div aria-hidden onClick={onClose} className="absolute inset-0 bg-black/30 backdrop-blur-[2px]" />

      <aside
        role="dialog"
        aria-modal="true"
        aria-label={t.app.modules.agents.contextInspector.ariaLabel}
        className={cn(
          "relative flex h-full w-[380px] max-w-[92vw] flex-col",
          "border-l border-(--color-border) bg-(--color-card) shadow-(--shadow-card)",
        )}
      >
        <header className="flex shrink-0 items-start gap-2.5 border-b border-(--color-border) px-4 py-3">
          <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-(--color-brand-50) text-(--color-brand-700)">
            <Gauge className="size-3.5" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-semibold text-(--color-foreground)">{t.app.modules.agents.contextInspector.title}</p>
            <p className="mt-0.5 text-[11px] leading-relaxed text-(--color-muted-foreground)">
              {t.app.modules.agents.interp.inspectorLead.replace("{agent}", agentName ?? t.app.modules.agents.interp.theAgent)}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label={t.app.modules.agents.contextInspector.close}
            className="rounded-lg p-1 text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
          >
            <X className="size-3.5" />
          </button>
        </header>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-4">
          {report ? (
            <>
              <InputContext report={report} />
              <Rounds report={report} />
              <Exclusions report={report} />
              <Warnings report={report} />
            </>
          ) : (
            /* Absent is not empty. A turn from before the report existed
               has no account of itself, and saying "0 tokens" would be an
               invented measurement. */
            <Section title={t.app.modules.agents.contextInspector.inputContext}>
              <p className="text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
                {t.app.modules.agents.contextInspector.noReport}
              </p>
            </Section>
          )}

          <Usage usage={usage} cost={cost} model={message.model} />
        </div>
      </aside>
    </div>
  );
}

/* ── provider rounds ─────────────────────────────────────────────────── */

/**
 * The calls this turn made, when it made more than one.
 *
 * ── Why this section exists ────────────────────────────────────────────
 * Before tools, one turn was one call, so the block list above described
 * the whole thing. With tools it describes only the first call: the context
 * grows between calls, and the tokens and the money are spent once per
 * call. Showing the first snapshot as though it covered all of them would
 * be this panel's first lie.
 *
 * ── Why it is a summary and not a trace ────────────────────────────────
 * Counts, a cost and the names of what ran. No arguments, no results, no
 * message bodies — those live in the audit trail, which the transcript
 * opens on demand. The Inspector answers "what happened in this turn and
 * what did it cost"; it is not a debugger.
 *
 * Absent for every ordinary turn, and absent is right: an array of one
 * would be noise on almost every turn ever stored.
 */
function Rounds({ report }: { report: NonNullable<ApiMessage["context_report"]> }) {
  const t = useT();
  const rounds = roundRows(report);
  if (rounds.length === 0) return null;

  return (
    <Section title={`Chamadas ao modelo (${rounds.length})`}>
      <p className="mb-2.5 text-[11px] leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.contextInspector.multiRound}
      </p>
      <ul className="space-y-2">
        {rounds.map((r) => (
          <li
            key={r.round}
            className="rounded-xl border border-(--color-border) bg-(--color-muted)/20 px-2.5 py-2"
          >
            <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <span className="text-[11.5px] font-medium text-(--color-foreground)">
                Chamada {r.round}
              </span>
              <span className="font-mono text-[10.5px] tabular-nums text-(--color-muted-foreground)">
                {formatTokens(r.promptTokens)} in · {formatTokens(r.completionTokens)} out
              </span>
              {/* Unknown is never rendered as zero. */}
              <span className="ml-auto font-mono text-[10.5px] tabular-nums text-(--color-muted-foreground)">
                {r.cost === null ? "custo desconhecido" : formatUSD(r.cost)}
              </span>
            </div>
            {!r.measured ? (
              <p className="mt-0.5 text-[10.5px] text-(--color-muted-foreground)">
                {t.app.modules.agents.contextInspector.estimatedRound}
              </p>
            ) : null}
            {r.addedTokens > 0 ? (
              <p className="mt-0.5 text-[10.5px] text-(--color-muted-foreground)">
                +~{formatTokens(r.addedTokens)} tokens que a chamada anterior não carregava.
              </p>
            ) : null}
            {r.tools.length > 0 ? (
              <ul className="mt-1 space-y-0.5">
                {r.tools.map((t, i) => (
                  <li
                    key={`${t.name}-${i}`}
                    className="flex items-baseline gap-1.5 font-mono text-[10.5px]"
                  >
                    <span className="text-(--color-muted-foreground)">{t.name}</span>
                    <span
                      className={
                        t.ok ? "text-(--color-brand-700)" : "text-(--color-destructive)"
                      }
                    >
                      {t.ok ? "ok" : toolErrorLabel(t.errorCode)}
                    </span>
                    <span className="ml-auto tabular-nums text-(--color-muted-foreground)">
                      {t.durationMS}ms
                    </span>
                  </li>
                ))}
              </ul>
            ) : null}
          </li>
        ))}
      </ul>
    </Section>
  );
}

/* ── input context ───────────────────────────────────────────────────── */

function InputContext({ report }: { report: NonNullable<ApiMessage["context_report"]> }) {
  const t = useT();
  const fmt = useFormat();
  const rows = blockRows(report);

  return (
    <Section title={t.app.modules.agents.contextInspector.inputContext}>
      <p className="mb-2.5 flex items-baseline gap-1.5">
        <span className="text-lg font-semibold tabular-nums text-(--color-foreground)">
          ~{formatTokens(report.total_estimated_tokens)}
        </span>
        <span className="text-[11px] text-(--color-muted-foreground)">
          {t.app.modules.agents.contextInspector.estimatedTokensSuffix} {fmt.number(report.total_characters)}{" "}
          {t.app.modules.agents.contextInspector.charactersSuffix}
        </span>
      </p>

      {rows.length === 0 ? (
        <p className="text-[11.5px] text-(--color-muted-foreground)">
          {t.app.modules.agents.contextInspector.noBlocks}
        </p>
      ) : (
        <ul className="space-y-1.5">
          {rows.map((row) => (
            <li key={row.kind}>
              <div className="flex items-baseline justify-between gap-3">
                <span className="truncate text-[12px] text-(--color-foreground)">{row.label}</span>
                <span className="shrink-0 font-mono text-[11px] tabular-nums text-(--color-muted-foreground)">
                  ~{formatTokens(row.estimatedTokens)}
                </span>
              </div>
              {/* Proportion, in CSS. A chart library for five bars would be
                  a dependency to draw a div. */}
              <div className="mt-1 h-1 overflow-hidden rounded-full bg-(--color-muted)">
                <div
                  className="h-full rounded-full bg-(--color-brand-500)"
                  style={{ width: `${Math.max(row.share * 100, 1)}%` }}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

/* ── usage ───────────────────────────────────────────────────────────── */

function Usage({
  usage,
  cost,
  model,
}: {
  usage: ReturnType<typeof turnUsage>;
  cost: number | null;
  model?: string;
}) {
  const t = useT();
  const fmt = useFormat();
  return (
    <Section title={t.app.modules.agents.contextInspector.consumption}>
      <dl className="space-y-1.5">
        <Line
          label={t.app.modules.agents.contextInspector.estimatedInput}
          value={usage.estimatedInput === null ? null : `~${formatTokens(usage.estimatedInput)}`}
        />
        {usage.measured ? (
          <>
            <Line
              label={t.app.modules.agents.contextInspector.reportedInput}
              value={usage.actualInput === null ? null : fmt.number(usage.actualInput)}
              emphasis
            />
            {usage.difference !== null ? (
              <Line
                label={t.app.modules.agents.contextInspector.difference}
                value={`${usage.difference >= 0 ? "+" : ""}${fmt.number(usage.difference)}`}
                muted
              />
            ) : null}
          </>
        ) : (
          /* No usage frame. The estimate is all there is, and pretending
             otherwise would turn "we do not know" into "it consumed 0". */
          <p className="text-[11px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.contextInspector.noUsage}
          </p>
        )}
        <Line
          label={t.app.modules.agents.contextInspector.output}
          value={usage.output === null ? null : fmt.number(usage.output)}
          emphasis={usage.measured}
        />
      </dl>

      <div className="mt-3 border-t border-(--color-border) pt-2.5">
        {cost === null ? (
          <>
            <p className="text-[12px] font-medium text-(--color-foreground)">{t.app.modules.agents.contextInspector.unknownCost}</p>
            <p className="mt-0.5 text-[11px] leading-relaxed text-(--color-muted-foreground)">
              {t.app.modules.agents.contextInspector.unknownCostBody}
            </p>
          </>
        ) : (
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-[12px] text-(--color-foreground)">{t.app.modules.agents.contextInspector.cost}</span>
            <span className="font-mono text-[12px] font-semibold tabular-nums text-(--color-foreground)">
              {formatUSD(cost)}
            </span>
          </div>
        )}
        {model ? (
          <p className="mt-1.5 font-mono text-[10px] text-(--color-muted-foreground)">{model}</p>
        ) : null}
      </div>
    </Section>
  );
}

/* ── exclusions and warnings ─────────────────────────────────────────── */

function Exclusions({ report }: { report: NonNullable<ApiMessage["context_report"]> }) {
  const t = useT();
  const rows = exclusionRows(report);
  if (rows.length === 0) return null;

  return (
    <Section title={t.app.modules.agents.contextInspector.exclusions}>
      <ul className="space-y-1.5">
        {rows.map((row) => (
          <li key={`${row.kind}:${row.reason}`} className="flex items-baseline justify-between gap-3">
            <span className="truncate text-[12px] text-(--color-foreground)">
              {row.blockLabel}
              <span className="text-(--color-muted-foreground)">
                {" · "}
                {/*
                  An exclusion with no items did not leave an item out — it
                  cut part of one that IS there. Rendering "0 itens" would
                  send a reader looking for something that was never missing,
                  so that row states what it actually lost: characters.
                */}
                {row.items > 0
                  ? `${row.items} ${row.items === 1 ? "item" : "itens"}`
                  : `${row.characters} caracteres`}
              </span>
            </span>
            <span className="shrink-0 rounded-full bg-(--color-muted) px-1.5 py-px font-mono text-[9.5px] uppercase tracking-wider text-(--color-muted-foreground)">
              {row.reasonLabel}
            </span>
          </li>
        ))}
      </ul>
    </Section>
  );
}

function Warnings({ report }: { report: NonNullable<ApiMessage["context_report"]> }) {
  const t = useT();
  const kinds = warnings(report);
  if (kinds.length === 0) return null;

  return (
    <Section title={t.app.modules.agents.contextInspector.warnings}>
      <div className="space-y-1.5">
        {kinds.map((kind) => (
          <p
            key={kind}
            className="flex items-start gap-1.5 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-2.5 py-2 text-[11px] leading-relaxed text-(--color-destructive)"
          >
            <AlertTriangle className="mt-0.5 size-3 shrink-0" />
            <span>
              {blockLabel(kind)} não pôde ser carregada neste turno. A resposta foi gerada sem essa
              parte do contexto.
            </span>
          </p>
        ))}
      </div>
    </Section>
  );
}

/* ── small parts ─────────────────────────────────────────────────────── */

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {title}
      </h3>
      {children}
    </section>
  );
}

function Line({
  label,
  value,
  emphasis,
  muted,
}: {
  label: string;
  /** Null renders an explicit absence, never a zero. */
  value: string | null;
  emphasis?: boolean;
  muted?: boolean;
}) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-[12px] text-(--color-foreground)">{label}</dt>
      <dd
        className={cn(
          "shrink-0 font-mono text-[11px] tabular-nums",
          value === null
            ? "text-(--color-muted-foreground)"
            : muted
              ? "text-(--color-muted-foreground)"
              : emphasis
                ? "font-semibold text-(--color-foreground)"
                : "text-(--color-foreground)",
        )}
      >
        {value ?? "—"}
      </dd>
    </div>
  );
}
