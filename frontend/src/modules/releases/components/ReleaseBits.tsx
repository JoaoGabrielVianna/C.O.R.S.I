/**
 * The small, shared vocabulary of the release history: the badges, the
 * version pill, the metric tile and the date format.
 *
 * They live together because their job is consistency. A status that reads
 * one way on a card and another way on a detail page is the kind of drift
 * that makes an audit surface untrustworthy for reasons that have nothing
 * to do with the data.
 */

import { cn } from "@/lib/utils";
import type {
  ApiMetric,
  ModuleStatus,
  ReleaseStatus,
  Stability,
} from "@/modules/releases/api/releases";

const STABILITY_TONE: Record<Stability, string> = {
  stable:
    "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  beta: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  rc: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300",
};

/**
 * The maturity claim. Deliberately separate from the publication badge:
 * "published" and "stable" answer different questions, and a page that
 * showed only one of them would leave the other unanswerable.
 */
export function StabilityBadge({
  stability,
  label,
  className,
}: {
  stability: Stability;
  label: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5",
        "font-mono text-[9.5px] uppercase tracking-[0.14em]",
        STABILITY_TONE[stability],
        className,
      )}
    >
      {label}
    </span>
  );
}

const MODULE_TONE: Record<ModuleStatus, string> = {
  active:
    "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  partial: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  frozen:
    "border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)",
};

export function ModuleStatusBadge({
  status,
  label,
  className,
}: {
  status: ModuleStatus;
  label: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5",
        "font-mono text-[9.5px] uppercase tracking-[0.14em]",
        MODULE_TONE[status],
        className,
      )}
    >
      {label}
    </span>
  );
}

/**
 * A draft is deliberately loud. It is the one state where the page is
 * showing something that has *not* shipped, and a quiet label would let it
 * be read as history.
 */
export function ReleaseStatusBadge({
  status,
  label,
  className,
}: {
  status: ReleaseStatus;
  label: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5",
        "font-mono text-[9.5px] uppercase tracking-[0.14em]",
        status === "published"
          ? "border-(--color-brand-500)/30 bg-(--color-brand-500)/10 text-(--color-brand-700) dark:text-(--color-brand-300)"
          : "border-dashed border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300",
        className,
      )}
    >
      {label}
    </span>
  );
}

export function VersionPill({ version, className }: { version: string; className?: string }) {
  return (
    <span
      className={cn(
        "font-mono text-[13px] font-semibold tracking-tight text-(--color-foreground)",
        className,
      )}
    >
      v{version}
    </span>
  );
}

/**
 * A measured fact, rendered big enough to scan. The value is whatever was
 * recorded — "8/8" and "123" both belong here, which is why nothing tries
 * to parse it as a number.
 */
export function MetricTile({ metric }: { metric: ApiMetric }) {
  return (
    <div className="rounded-xl border border-(--color-border) bg-(--color-card) px-4 py-3">
      <p className="font-display text-xl font-semibold tracking-tight text-(--color-foreground)">
        {metric.value}
      </p>
      <p className="mt-0.5 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
        {metric.label}
      </p>
    </div>
  );
}
