import { useState } from "react";
import { ListChecks, Plus, Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";
import { useFormat, useT } from "@/lib/i18n";
import { AddOpportunityModal } from "./AddOpportunityModal";
import { DiscoverFeed } from "./DiscoverFeed";
import { FilterBar } from "./FilterBar";
import { OpportunityModal } from "./OpportunityModal";
import { PipelineBoard } from "./PipelineBoard";
import { useJobRadar, type JobRadarStore } from "./store";
import type { JobRadarTab } from "./types";

/**
 * Job Radar — career operations module (v0.0.0).
 *
 * Two tabs: Discover (feed of untracked opportunities) and Pipeline (tracked
 * applications by stage). Filters apply to Discover only. No fabricated
 * metrics, no curated rails — interface stays honest while the collector +
 * normalizer are built.
 *
 * Data flow today:
 *   user manual entry → useJobRadar → /job-radar → postgres → screens
 *
 * The same application layer is reachable by an authorized agent through
 * the `job_radar.opportunity.*` tools, so a stage changed in conversation
 * and one changed by dragging a card are the same write.
 *
 * Data flow planned:
 *   collector → normalizer → postgres → /job-radar → useJobRadar → screens
 */
export function JobRadarPage() {
  const t = useT();
  const store = useJobRadar();
  const [tab, setTab] = useState<JobRadarTab>("discover");
  const [manualOpen, setManualOpen] = useState(false);

  const labels = t.app.modules.jobRadar;

  return (
    <div className="flex flex-col gap-4 lg:h-[calc(100dvh-6.5rem)]">
      <header className="flex shrink-0 flex-wrap items-end justify-between gap-3 border-b border-(--color-border) pb-3">
        <div className="min-w-0">
          <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-(--color-muted-foreground)">
            {labels.badge}
          </p>
          <h1 className="mt-0.5 font-display text-xl font-semibold tracking-tight text-(--color-foreground) sm:text-2xl">
            {labels.title}
          </h1>
          <p className="mt-1 max-w-2xl text-[12.5px] text-(--color-muted-foreground)">
            {labels.description}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setManualOpen(true)}
          className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) transition-colors hover:bg-(--color-muted)"
        >
          <Plus className="size-3.5" />
          <span className="hidden sm:inline">{labels.addManually}</span>
          <span className="sm:hidden">{labels.addManuallyShort}</span>
        </button>
      </header>

      <ImportBanner store={store} />
      {store.error ? (
        <p
          role="alert"
          className="shrink-0 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] text-(--color-foreground)"
        >
          {store.error}
        </p>
      ) : null}

      <TabsRow
        tab={tab}
        onChange={setTab}
        discoverCount={store.counts.discover}
        pipelineCount={store.counts.pipeline}
      />

      {tab === "discover" ? (
        <div className="shrink-0">
          <FilterBar store={store} />
        </div>
      ) : null}

      <div className="min-h-0 flex-1">
        {tab === "discover" ? <DiscoverFeed   store={store} onAddManually={() => setManualOpen(true)} /> : null}
        {tab === "pipeline" ? <PipelineBoard  store={store} /> : null}
      </div>

      <AddOpportunityModal
        open={manualOpen}
        onClose={() => setManualOpen(false)}
        onSubmit={(input) => store.addOpportunity(input)}
      />
      <OpportunityModal store={store} />
    </div>
  );
}

/**
 * The one-time migration prompt.
 *
 * ── Why it asks instead of importing automatically ─────────────────────
 * Because the records are a real job search, the import is not idempotent
 * by design, and the person is the only one who can confirm that the
 * pipeline on screen is the one they expect to move. Running it silently on
 * first load would mean discovering a duplicated pipeline afterwards, with
 * no moment at which anyone chose it.
 *
 * Declining does not delete the browser copy — see the note on
 * LEGACY_STORAGE_KEY. Nothing here destroys the only copy of anything.
 */
function ImportBanner({ store }: { store: JobRadarStore }) {
  const t = useT();
  const fmt = useFormat();
  if (store.importResult) {
    const { created_count, failed_count } = store.importResult;
    return (
      <div className="shrink-0 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px] text-(--color-foreground)">
        {fmt
          .plural(created_count, t.app.modules.jobRadar.jobRadarDetail.importDone)
          .replace("{count}", fmt.number(created_count))}
        {failed_count > 0
          ? t.app.modules.jobRadar.jobRadarDetail.importFailedTail.replace("{count}", fmt.number(failed_count))
          : ""}
        .
      </div>
    );
  }

  if (store.pendingImportCount === 0) return null;

  return (
    <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2">
      <p className="text-[12.5px] text-(--color-foreground)">
        {fmt
          .plural(store.pendingImportCount, t.app.modules.jobRadar.jobRadarDetail.importPending)
          .replace("{count}", fmt.number(store.pendingImportCount))}{" "}
        {t.app.modules.jobRadar.jobRadarDetail.importPendingTail}
      </p>
      <div className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          onClick={() => void store.runImport()}
          disabled={store.importing}
          className="inline-flex items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-muted) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-card) disabled:opacity-60"
        >
          {store.importing ? "Migrando…" : "Migrar agora"}
        </button>
        <button
          type="button"
          onClick={store.dismissImport}
          disabled={store.importing}
          className="rounded-lg px-2 py-1.5 text-[12.5px] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground) disabled:opacity-60"
        >
          {t.app.modules.jobRadar.jobRadarDetail.notNow}
        </button>
      </div>
    </div>
  );
}

type TabDef = { id: JobRadarTab; label: string; icon: React.ComponentType<React.SVGProps<SVGSVGElement>>; count?: number };

function TabsRow({
  tab,
  onChange,
  discoverCount,
  pipelineCount,
}: {
  tab: JobRadarTab;
  onChange: (next: JobRadarTab) => void;
  discoverCount: number;
  pipelineCount: number;
}) {
  const t = useT();
  const labels = t.app.modules.jobRadar.tabs;

  const tabs: TabDef[] = [
    { id: "discover", label: labels.discover, icon: Sparkles,   count: discoverCount },
    { id: "pipeline", label: labels.pipeline, icon: ListChecks, count: pipelineCount },
  ];

  return (
    <div
      role="tablist"
      className="flex shrink-0 items-center gap-1 border-b border-(--color-border) pb-px"
    >
      {tabs.map((entry) => {
        const Icon = entry.icon;
        const active = entry.id === tab;
        return (
          <button
            key={entry.id}
            role="tab"
            aria-selected={active}
            type="button"
            onClick={() => onChange(entry.id)}
            className={cn(
              "relative inline-flex items-center gap-1.5 rounded-t-md px-3 py-1.5 text-[12.5px] font-medium transition-colors",
              active
                ? "text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            <Icon
              className={cn(
                "size-3.5",
                active ? "text-(--color-brand-600) dark:text-(--color-brand-400)" : "",
              )}
            />
            {entry.label}
            {entry.count !== undefined && entry.count > 0 ? (
              <span
                className={cn(
                  "rounded-full px-1.5 font-mono text-[10px]",
                  active
                    ? "bg-(--color-brand-500) text-white"
                    : "bg-(--color-muted) text-(--color-muted-foreground)",
                )}
              >
                {entry.count}
              </span>
            ) : null}
            {active ? (
              <span
                aria-hidden
                className="absolute inset-x-2 bottom-[-1px] h-[2px] rounded-full bg-(--color-brand-500)"
              />
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
