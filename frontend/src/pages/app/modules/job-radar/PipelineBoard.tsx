import { useMemo, useState, type DragEvent } from "react";
import { Building2, ListChecks } from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import type { JobRadarStore } from "./store";
import { PIPELINE_STAGE_ORDER, type Opportunity, type PipelineStage } from "./types";
import { daysBetween } from "./time";

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
 * PipelineBoard — six-column kanban over tracked opportunities.
 *
 * Stages, in order: saved · applied · interview · technical · offer · rejected.
 * Drag-and-drop preserved (native HTML5, MIME `application/x-jobradar-opp`).
 */
export function PipelineBoard({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.jobRadar.pipeline;
  const { state, companyById, openModal, moveStage } = store;
  const [now] = useState(() => Date.now());

  const tracked = useMemo(
    () => state.opportunities.filter((o) => o.tracking !== null),
    [state.opportunities],
  );

  if (tracked.length === 0) {
    return (
      <div className="flex min-h-[280px] items-center justify-center">
        <div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-12 text-center">
          <span className="flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
            <ListChecks className="size-4" />
          </span>
          <p className="text-sm font-medium text-(--color-foreground)">{labels.empty.title}</p>
          <p className="max-w-sm text-[13px] text-(--color-muted-foreground)">{labels.empty.body}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-full min-h-0 gap-3 overflow-x-auto pb-1">
        {PIPELINE_STAGE_ORDER.map((stage) => {
          const items = tracked
            .filter((o) => o.tracking?.stage === stage)
            .sort((a, b) => (b.tracking?.stageEnteredAt ?? 0) - (a.tracking?.stageEnteredAt ?? 0));
          return (
            <StageColumn
              key={stage}
              stage={stage}
              items={items}
              dot={STAGE_DOT[stage]}
              label={labels.stages[stage]}
              help={labels.stageHelp[stage]}
              columnEmpty={labels.columnEmpty}
              selectedId={state.modalId}
              now={now}
              companyName={(id) => companyById.get(id)?.name ?? "—"}
              onSelect={(id) => openModal(id)}
              onDrop={(id) => moveStage(id, stage)}
            />
          );
        })}
      </div>
    </div>
  );
}

type StageColumnProps = {
  stage: PipelineStage;
  items: Opportunity[];
  dot: string;
  label: string;
  help: string;
  columnEmpty: string;
  selectedId: string | null;
  now: number;
  companyName: (id: string) => string;
  onSelect: (id: string) => void;
  onDrop: (id: string) => void;
};

function StageColumn({
  stage,
  items,
  dot,
  label,
  help,
  columnEmpty,
  selectedId,
  now,
  companyName,
  onSelect,
  onDrop,
}: StageColumnProps) {
  const [dragOver, setDragOver] = useState(false);

  const onDragOver = (e: DragEvent<HTMLDivElement>) => {
    if (e.dataTransfer.types.includes("application/x-jobradar-opp")) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      setDragOver(true);
    }
  };
  const onDragLeave = () => setDragOver(false);
  const onDropInternal = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    const id = e.dataTransfer.getData("application/x-jobradar-opp");
    setDragOver(false);
    if (id) onDrop(id);
  };

  return (
    <div
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDropInternal}
      data-stage={stage}
      className={cn(
        "flex h-full min-w-[260px] flex-1 basis-[260px] flex-col rounded-2xl border bg-(--color-card)/70 shadow-(--shadow-soft) backdrop-blur transition-colors duration-150",
        dragOver
          ? "border-(--color-brand-400) bg-(--color-brand-500)/[0.06]"
          : "border-(--color-border)",
      )}
    >
      <header className="flex shrink-0 items-start justify-between gap-2 border-b border-(--color-border) px-4 pt-3 pb-2.5">
        <div className="min-w-0">
          <div className="inline-flex items-center gap-2">
            <span aria-hidden className={cn("size-1.5 rounded-full", dot)} />
            <span className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-(--color-foreground)">
              {label}
            </span>
          </div>
          <p className="mt-0.5 text-[11px] text-(--color-muted-foreground)">{help}</p>
        </div>
        <span className="shrink-0 rounded-full bg-(--color-muted) px-1.5 py-0.5 font-mono text-[10px] font-medium text-(--color-muted-foreground)">
          {items.length}
        </span>
      </header>

      <div className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto p-3">
        {items.length === 0 ? (
          <div className="flex h-16 items-center justify-center rounded-lg border border-dashed border-(--color-border) text-[11px] text-(--color-muted-foreground)">
            {columnEmpty}
          </div>
        ) : (
          items.map((o) => (
            <TrackedCard
              key={o.id}
              opportunity={o}
              companyName={companyName(o.companyId)}
              selected={selectedId === o.id}
              now={now}
              onSelect={() => onSelect(o.id)}
            />
          ))
        )}
      </div>
    </div>
  );
}

function TrackedCard({
  opportunity,
  companyName,
  selected,
  now,
  onSelect,
}: {
  opportunity: Opportunity;
  companyName: string;
  selected: boolean;
  now: number;
  onSelect: () => void;
}) {
  const t = useT();
  const card = t.app.modules.jobRadar.pipeline.card;
  const tracking = opportunity.tracking!;

  const inStageDays = daysBetween(tracking.stageEnteredAt, now);
  const totalDays   = daysBetween(tracking.trackedAt, now);

  const onDragStart = (e: DragEvent<HTMLButtonElement>) => {
    e.dataTransfer.setData("application/x-jobradar-opp", opportunity.id);
    e.dataTransfer.effectAllowed = "move";
  };

  return (
    <button
      type="button"
      draggable
      onDragStart={onDragStart}
      onClick={onSelect}
      className={cn(
        "group flex w-full cursor-grab flex-col gap-2 rounded-xl border bg-(--color-card) p-3 text-left shadow-(--shadow-soft) transition-[border-color,background-color,box-shadow] active:cursor-grabbing",
        selected
          ? "border-(--color-brand-400) bg-(--color-brand-500)/[0.06] shadow-(--shadow-card)"
          : "border-(--color-border) hover:border-(--color-border-strong) hover:bg-(--color-muted)/30 hover:shadow-(--shadow-card)",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="inline-flex min-w-0 items-center gap-1.5">
          <Building2 className="size-3 shrink-0 text-(--color-muted-foreground)" />
          <span className="truncate text-[12px] font-medium text-(--color-muted-foreground)">{companyName}</span>
        </span>
        <span className="shrink-0 rounded-full border border-(--color-border) bg-(--color-muted) px-1.5 py-px font-mono text-[9px] uppercase tracking-wide text-(--color-muted-foreground)">
          {opportunity.source}
        </span>
      </div>

      <span className="line-clamp-2 font-display text-[14px] font-semibold leading-snug tracking-tight text-(--color-foreground)">
        {opportunity.role || "—"}
      </span>

      {opportunity.salary ? (
        <p className="truncate font-mono text-[10.5px] text-(--color-muted-foreground)">
          {opportunity.salary}
        </p>
      ) : null}

      <div className="flex items-center justify-between gap-2 border-t border-(--color-border) pt-2">
        <span
          className="inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)"
          title={`${totalDays}d total`}
        >
          {inStageDays === 0 ? card.todayInStage : card.daysInStage.replace("{{n}}", String(inStageDays))}
          {totalDays > 0 ? <span className="text-(--color-muted-foreground)/60">· {totalDays}d</span> : null}
        </span>
      </div>

      {tracking.nextAction ? (
        <div className="rounded-md border border-(--color-border) bg-(--color-muted)/40 px-2 py-1.5">
          <p className="font-mono text-[9px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {card.nextAction}
          </p>
          <p className="mt-0.5 line-clamp-2 text-[11.5px] leading-snug text-(--color-foreground)/90">
            {tracking.nextAction}
          </p>
        </div>
      ) : null}
    </button>
  );
}
