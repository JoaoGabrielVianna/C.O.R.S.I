import { useEffect, useState, type KeyboardEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import {
  Building2,
  Calendar,
  Code2,
  ExternalLink,
  MapPin,
  MessageSquare,
  RotateCcw,
  Send,
  Trash2,
  Wallet,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/Input";
import { useT } from "@/lib/i18n";
import { useTalkToAgent } from "@/modules/job-radar/talkToAgent";
import { OPPORTUNITY_REFERENCE_TYPE } from "@/modules/job-radar/referenceType";
import type { JobRadarStore } from "./store";
import {
  PIPELINE_STAGE_ORDER,
  type Opportunity,
  type PipelineStage,
} from "./types";
import { daysBetween, formatAbsolute } from "./time";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = {
  store: JobRadarStore;
};

const STAGE_DOT: Record<PipelineStage, string> = {
  saved:      "bg-slate-400",
  applied:    "bg-sky-400",
  interview:  "bg-amber-400",
  technical:  "bg-violet-400",
  offer:      "bg-emerald-400",
  rejected:   "bg-rose-400",
};

/**
 * OpportunityModal — centered detail dialog (max-w 960px).
 * Two-column layout on lg+: left = body (overview + description + stack + notes),
 * right = action panel (Save / Apply, stage picker, timeline).
 */
export function OpportunityModal({ store }: Props) {
  const { modalOpportunity, closeModal, companyById } = store;
  const open = modalOpportunity !== null;

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeModal();
    };
    window.addEventListener("keydown", onKey as unknown as EventListener);
    return () => window.removeEventListener("keydown", onKey as unknown as EventListener);
  }, [open, closeModal]);

  return (
    <AnimatePresence>
      {open && modalOpportunity ? (
        <div className="fixed inset-0 z-[90] flex items-start justify-center px-4 py-[8vh] sm:py-[10vh]">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.18 }}
            onClick={closeModal}
            className="absolute inset-0 bg-black/50 backdrop-blur-sm"
            aria-hidden
          />
          <motion.div
            role="dialog"
            aria-modal="true"
            initial={{ opacity: 0, y: -12, scale: 0.985 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -8, scale: 0.985 }}
            transition={{ duration: 0.22, ease }}
            className={cn(
              "relative flex max-h-[84vh] w-full max-w-[960px] flex-col overflow-hidden rounded-2xl",
              "border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)",
            )}
          >
            <ModalBody
              opportunity={modalOpportunity}
              companyName={companyById.get(modalOpportunity.companyId)?.name ?? "—"}
              store={store}
            />
          </motion.div>
        </div>
      ) : null}
    </AnimatePresence>
  );
}

function ModalBody({
  opportunity,
  companyName,
  store,
}: {
  opportunity: Opportunity;
  companyName: string;
  store: JobRadarStore;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal;
  const stagesLabel = t.app.modules.jobRadar.pipeline.stages;

  return (
    <>
      <ModalHeader
        opportunity={opportunity}
        companyName={companyName}
        onClose={store.closeModal}
        onDelete={() => store.removeOpportunity(opportunity.id)}
      />
      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[1fr_320px]">
        {/* Left column — body */}
        <div className="min-h-0 overflow-y-auto px-6 py-5">
          <OverviewBlock opportunity={opportunity} />
          <DescriptionBlock
            opportunity={opportunity}
            onChange={(v) => store.updateOpportunity(opportunity.id, { description: v })}
          />
          <StackEditor
            stack={opportunity.stack}
            onChange={(stack) => store.updateOpportunity(opportunity.id, { stack })}
          />
          <NotesBlock
            opportunity={opportunity}
            updateNotes={store.updateNotes}
          />
        </div>

        {/* Right column — action panel + timeline */}
        <aside className="min-h-0 overflow-y-auto border-t border-(--color-border) bg-(--color-background)/40 px-5 py-5 lg:border-l lg:border-t-0">
          <TalkToAgentBlock opportunity={opportunity} />
          <ActionPanel opportunity={opportunity} store={store} />
          <StageBlock
            opportunity={opportunity}
            stagesLabel={stagesLabel}
            onMove={(s) => store.moveStage(opportunity.id, s)}
            onSetNextAction={(v) => store.setNextAction(opportunity.id, v)}
            note={labels.stagesNote}
          />
          <TimelineBlock
            opportunity={opportunity}
            stagesLabel={stagesLabel}
            emptyHint={labels.timelineEmpty}
          />
        </aside>
      </div>
    </>
  );
}

// ── Header ──────────────────────────────────────────────────────────────

function ModalHeader({
  opportunity,
  companyName,
  onClose,
  onDelete,
}: {
  opportunity: Opportunity;
  companyName: string;
  onClose: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal;
  return (
    <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-6 py-4">
      <div className="min-w-0">
        <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-(--color-muted-foreground)">
          {labels.eyebrow} · {opportunity.source}
        </p>
        <h2 className="mt-0.5 font-display text-lg font-semibold tracking-tight text-(--color-foreground) sm:text-xl">
          {opportunity.role || "—"}
        </h2>
        <p className="mt-1 inline-flex items-center gap-1.5 text-[13px] text-(--color-muted-foreground)">
          <Building2 className="size-3" />
          {companyName}
        </p>
      </div>
      <div className="flex items-center gap-1">
        {opportunity.sourceUrl ? (
          <a
            href={opportunity.sourceUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-(--color-border) bg-(--color-card) px-2.5 text-[11.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted)"
            title={labels.actions.openOriginal}
          >
            <ExternalLink className="size-3.5" />
            <span className="hidden sm:inline">{labels.actions.openOriginal}</span>
          </a>
        ) : null}
        <button
          type="button"
          onClick={onDelete}
          aria-label={labels.actions.delete}
          title={labels.actions.delete}
          className="flex size-8 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-rose-500/10 hover:text-rose-500"
        >
          <Trash2 className="size-3.5" />
        </button>
        <button
          type="button"
          onClick={onClose}
          aria-label={labels.actions.close}
          className="flex size-8 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
        >
          <X className="size-3.5" />
        </button>
      </div>
    </header>
  );
}

// ── Left column blocks ──────────────────────────────────────────────────

function OverviewBlock({ opportunity }: { opportunity: Opportunity }) {
  const t = useT();
  return (
    <section className="grid gap-3 pb-5 grid-cols-[repeat(auto-fit,minmax(140px,1fr))]">
      <Field icon={Wallet} label={t.app.modules.jobRadar.jobRadarDetail.salary} value={opportunity.salary || "—"} mono />
      <Field icon={MapPin} label={t.app.modules.jobRadar.jobRadarDetail.location} value={opportunity.location || "—"} mono />
      <Field icon={Calendar} label={t.app.modules.jobRadar.jobRadarDetail.posted} value={formatAbsolute(opportunity.postedAt)} mono />
      <Field icon={Code2} label={t.app.modules.jobRadar.jobRadarDetail.source} value={opportunity.source} mono />
    </section>
  );
}

function Field({
  icon: Icon,
  label,
  value,
  mono,
}: {
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <p className="inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        <Icon className="size-2.5" />
        {label}
      </p>
      <p className={cn("mt-0.5 text-[13px] text-(--color-foreground)", mono ? "font-mono text-[12px]" : "")}>
        {value}
      </p>
    </div>
  );
}

function DescriptionBlock({
  opportunity,
  onChange,
}: {
  opportunity: Opportunity;
  onChange: (next: string) => void;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal;
  return (
    <section className="border-t border-(--color-border) pb-5 pt-5">
      <p className="mb-2 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {labels.sections.description}
      </p>
      <textarea
        value={opportunity.description}
        onChange={(e) => onChange(e.target.value)}
        placeholder={labels.placeholders.description}
        rows={4}
        className={cn(
          "w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2 text-[13px]",
          "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
          "shadow-(--shadow-soft) outline-none resize-y",
          "transition-[border-color,box-shadow] duration-[250ms]",
          "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
        )}
      />
    </section>
  );
}

function StackEditor({
  stack,
  onChange,
}: {
  stack: string[];
  onChange: (next: string[]) => void;
}) {
  const t = useT();
  const [draft, setDraft] = useState("");

  const commit = () => {
    const v = draft.trim().replace(/,$/, "");
    if (!v) return;
    if (stack.includes(v)) {
      setDraft("");
      return;
    }
    onChange([...stack, v]);
    setDraft("");
  };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      commit();
    }
  };

  return (
    <section className="border-t border-(--color-border) pb-5 pt-5">
      <p className="mb-2 inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        <Code2 className="size-2.5" />
        {t.app.modules.jobRadar.jobRadarDetail.stack}
      </p>
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKey}
        onBlur={commit}
        placeholder={t.app.modules.jobRadar.jobRadarDetail.addTech}
        className="h-9 text-[12.5px]"
      />
      {stack.length > 0 ? (
        <div className="mt-2 flex flex-wrap gap-1">
          {stack.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => onChange(stack.filter((x) => x !== s))}
              className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-muted) px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide text-(--color-foreground) transition-colors hover:border-rose-300 hover:bg-rose-50 hover:text-rose-700 dark:hover:border-rose-700 dark:hover:bg-rose-500/10 dark:hover:text-rose-300"
            >
              {s}
              <X className="size-2.5" />
            </button>
          ))}
        </div>
      ) : null}
    </section>
  );
}

function NotesBlock({
  opportunity,
  updateNotes,
}: {
  opportunity: Opportunity;
  updateNotes: JobRadarStore["updateNotes"];
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal;
  return (
    <section className="border-t border-(--color-border) pt-5">
      <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {labels.sections.notes}
      </p>
      <div className="space-y-3">
        <TextArea
          label={labels.sections.general}
          value={opportunity.notes.general}
          onChange={(v) => updateNotes(opportunity.id, { general: v })}
          placeholder={labels.placeholders.general}
        />
        <TextArea
          label={labels.sections.interview}
          value={opportunity.notes.interview}
          onChange={(v) => updateNotes(opportunity.id, { interview: v })}
          placeholder={labels.placeholders.interview}
          rows={4}
        />
        <TextArea
          label={labels.sections.technical}
          value={opportunity.notes.technical}
          onChange={(v) => updateNotes(opportunity.id, { technical: v })}
          placeholder={labels.placeholders.technical}
        />
        <TextArea
          label={labels.sections.personal}
          value={opportunity.notes.personal}
          onChange={(v) => updateNotes(opportunity.id, { personal: v })}
          placeholder={labels.placeholders.personal}
        />
      </div>
    </section>
  );
}

function TextArea({
  label,
  value,
  onChange,
  placeholder,
  rows = 3,
}: {
  label: string;
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  rows?: number;
}) {
  return (
    <div>
      <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {label}
      </p>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={rows}
        className={cn(
          "w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px]",
          "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
          "shadow-(--shadow-soft) outline-none resize-y",
          "transition-[border-color,box-shadow] duration-[250ms]",
          "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
        )}
      />
    </div>
  );
}

// ── Right column blocks ─────────────────────────────────────────────────

/**
 * "Conversar sobre esta vaga" — the doorway from an entity into a thread.
 *
 * ── What crosses the boundary, and what does not ───────────────────────
 * One value leaves this module:
 *
 *   { type: "job_radar.opportunity", id: <uuid> }
 *
 * No company, no role, no stage. The chat resolves the label from Job
 * Radar's own service, so what the conversation displays cannot drift from
 * what the record says, and the agent reads the CURRENT state through its
 * tools rather than from anything this button sent.
 *
 * ── Why there is a picker at all ───────────────────────────────────────
 * See `useTalkToAgent`: this system has no designated agent, and inventing
 * one by name or by a new cross-module setting were both worse than asking
 * once and remembering.
 */
function TalkToAgentBlock({ opportunity }: { opportunity: Opportunity }) {
  const t = useT();
  const talk = useTalkToAgent();

  return (
    <section className="mb-5">
      <button
        type="button"
        disabled={talk.busy}
        onClick={() =>
          void talk.start({ type: OPPORTUNITY_REFERENCE_TYPE, id: opportunity.id })
        }
        className="inline-flex w-full items-center justify-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted) disabled:opacity-60"
      >
        <MessageSquare className="size-3.5" />
        {talk.busy ? "Abrindo…" : "Conversar sobre esta vaga"}
      </button>

      {talk.error ? (
        <p role="alert" className="mt-2 text-[11.5px] text-(--color-muted-foreground)">
          {talk.error}
        </p>
      ) : null}

      {talk.choices.length > 0 ? (
        <div className="mt-2 rounded-lg border border-(--color-border) bg-(--color-card) p-2">
          <p className="px-1 pb-1.5 text-[11.5px] text-(--color-muted-foreground)">
            {t.app.modules.jobRadar.jobRadarDetail.whichAgent}
          </p>
          <ul className="flex flex-col gap-0.5">
            {talk.choices.map((agent) => (
              <li key={agent.id}>
                <button
                  type="button"
                  disabled={talk.busy}
                  onClick={() => void talk.choose(agent.id)}
                  className="w-full truncate rounded-md px-2 py-1.5 text-left text-[12.5px] text-(--color-foreground) transition-colors hover:bg-(--color-muted) disabled:opacity-60"
                >
                  {agent.name}
                </button>
              </li>
            ))}
          </ul>
          <button
            type="button"
            onClick={talk.cancel}
            className="mt-1 w-full rounded-md px-2 py-1 text-[11.5px] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
          >
            {t.app.modules.jobRadar.jobRadarDetail.cancel}
          </button>
        </div>
      ) : null}
    </section>
  );
}

function ActionPanel({
  opportunity,
  store,
}: {
  opportunity: Opportunity;
  store: JobRadarStore;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal.actions;
  const isTracked = opportunity.tracking !== null;

  if (isTracked) {
    return (
      <section className="mb-5">
        <button
          type="button"
          onClick={() => store.untrack(opportunity.id)}
          className="inline-flex w-full items-center justify-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted)"
        >
          <RotateCcw className="size-3.5" />
          {labels.untrack}
        </button>
      </section>
    );
  }

  return (
    <section className="mb-5 grid grid-cols-2 gap-2">
      <button
        type="button"
        onClick={() => store.moveStage(opportunity.id, "saved")}
        className="inline-flex items-center justify-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted)"
      >
        {labels.save}
      </button>
      <button
        type="button"
        onClick={() => store.moveStage(opportunity.id, "applied")}
        className="inline-flex items-center justify-center gap-1.5 rounded-lg bg-(--color-accent) px-3 py-2 text-[12.5px] font-medium text-(--color-accent-foreground) shadow-(--shadow-card) transition-[transform,box-shadow] hover:-translate-y-px hover:shadow-(--shadow-glow)"
      >
        <Send className="size-3.5" />
        {labels.apply}
      </button>
    </section>
  );
}

function StageBlock({
  opportunity,
  stagesLabel,
  onMove,
  onSetNextAction,
  note,
}: {
  opportunity: Opportunity;
  stagesLabel: Record<PipelineStage, string>;
  onMove: (s: PipelineStage) => void;
  onSetNextAction: (v: string) => void;
  note: string;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal;
  const trackingStage = opportunity.tracking?.stage;

  if (!opportunity.tracking) return null;

  return (
    <section className="mb-5 space-y-2">
      <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {labels.fields.stage}
      </p>
      <div className="flex flex-wrap gap-1">
        {PIPELINE_STAGE_ORDER.map((s) => {
          const active = s === trackingStage;
          return (
            <button
              key={s}
              type="button"
              onClick={() => onMove(s)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] font-medium transition-colors",
                active
                  ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-foreground)"
                  : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:border-(--color-border-strong) hover:text-(--color-foreground)",
              )}
            >
              <span aria-hidden className={cn("size-1.5 rounded-full", STAGE_DOT[s])} />
              {stagesLabel[s]}
            </button>
          );
        })}
      </div>
      <p className="font-mono text-[10px] text-(--color-muted-foreground)/80">{note}</p>

      <div className="mt-3">
        <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {labels.fields.nextAction}
        </p>
        <Input
          value={opportunity.tracking.nextAction}
          onChange={(e) => onSetNextAction(e.target.value)}
          placeholder={t.app.modules.jobRadar.pipeline.card.nextActionPlaceholder}
          className="h-9 text-[12.5px]"
        />
      </div>
    </section>
  );
}

function TimelineBlock({
  opportunity,
  stagesLabel,
  emptyHint,
}: {
  opportunity: Opportunity;
  stagesLabel: Record<PipelineStage, string>;
  emptyHint: string;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.modal;
  const tracking = opportunity.tracking;

  if (!tracking) {
    return (
      <section className="space-y-2">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {labels.sections.timeline}
        </p>
        <p className="rounded-md border border-dashed border-(--color-border) px-3 py-3 text-center text-[12px] text-(--color-muted-foreground)">
          {emptyHint}
        </p>
      </section>
    );
  }

  const history = [...tracking.history].sort((a, b) => a.at - b.at);

  return (
    <section className="space-y-2">
      <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {labels.sections.timeline}
      </p>
      <ol className="relative space-y-2 border-l border-(--color-border) pl-3.5">
        {history.map((h, i) => {
          const next = history[i + 1]?.at;
          const span = next ? daysBetween(h.at, next) : null;
          return (
            <li key={`${h.stage}-${h.at}`} className="relative">
              <span
                aria-hidden
                className={cn(
                  "absolute -left-[7px] top-1 size-2.5 rounded-full border-2 border-(--color-card)",
                  STAGE_DOT[h.stage],
                )}
              />
              <p className="text-[12px] font-medium text-(--color-foreground)">{stagesLabel[h.stage]}</p>
              <p className="font-mono text-[10px] text-(--color-muted-foreground)">
                {formatAbsolute(h.at)}
                {span !== null ? <span> · {span}d</span> : null}
              </p>
            </li>
          );
        })}
      </ol>
    </section>
  );
}
