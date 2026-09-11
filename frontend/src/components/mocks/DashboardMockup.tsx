import {
  Activity,
  Brain,
  CalendarHeart,
  CircleDashed,
  DollarSign,
  GitCommitHorizontal,
  LoaderCircle,
  Radar,
  Search,
  Workflow,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";

/**
 * DashboardMockup — preview of the personal AI workspace.
 * Every value is intentional — no `—` placeholders, no "agente offline"
 * artifacts. Modules and labels come from i18n so PT/EN both feel native.
 */

const sidebarIcons = [Activity, Radar, DollarSign, Brain, CalendarHeart];

const kpiData = [
  { value: "2", caption: "solo, in motion" },
  { value: "5", caption: "commits on main" },
  { value: "4", caption: "on the horizon" },
  { value: "Solo", caption: "in control" },
];

export function DashboardMockup() {
  const t = useT();

  return (
    <div className="grid grid-cols-[180px_1fr] min-h-[480px]">
      {/* Sidebar */}
      <aside className="border-r border-(--color-border) bg-(--color-card) px-3 py-4">
        <div className="flex items-center gap-2 px-2 pb-3">
          <div className="flex size-7 items-center justify-center rounded-lg bg-(--color-brand-500) text-white">
            <span className="font-display text-xs font-bold">C</span>
          </div>
          <span className="font-display text-sm font-semibold tracking-tight text-(--color-foreground)">
            C.O.R.S.I
          </span>
        </div>
        <nav className="space-y-0.5 pt-2">
          {t.platform.mockup.sidebar.map((label, i) => {
            const Icon = sidebarIcons[i] ?? Workflow;
            const active = i === 1; // Application Tracker
            return (
              <div
                key={label}
                className={cn(
                  "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12.5px]",
                  active
                    ? "bg-(--color-brand-50) text-(--color-brand-700) font-semibold dark:bg-(--color-brand-500)/15 dark:text-(--color-brand-300)"
                    : "text-(--color-muted-foreground)",
                )}
              >
                <Icon className="size-3.5" />
                {label}
              </div>
            );
          })}
        </nav>
        <div className="mt-6 rounded-lg border border-(--color-brand-200) bg-(--color-brand-50) px-2.5 py-2 text-[10px] text-(--color-brand-800) dark:border-(--color-brand-700)/40 dark:bg-(--color-brand-500)/10 dark:text-(--color-brand-300)">
          <div className="font-semibold uppercase tracking-[0.14em]">
            {t.meta.status} · {t.meta.version}
          </div>
          <div className="mt-1 text-[10px] opacity-80">
            {t.hero.badge}
          </div>
        </div>
      </aside>

      {/* Main */}
      <div className="px-5 py-4">
        {/* Top bar */}
        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-1 items-center gap-2 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5">
            <Search className="size-3.5 text-(--color-muted-foreground)" />
            <span className="font-mono text-[11px] text-(--color-muted-foreground)">
              {t.platform.title}
            </span>
          </div>
          <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-200 bg-emerald-50 px-2 py-0.5 text-[10px] font-semibold text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300">
            <span className="size-1.5 rounded-full bg-emerald-500" />
            {t.app.common.shared.online}
          </span>
        </div>

        {/* KPI grid */}
        <div className="mt-4 grid grid-cols-4 gap-3">
          {t.platform.mockup.kpiLabels.map((label, i) => {
            const kpi = kpiData[i] ?? kpiData[0];
            const caption = t.platform.mockup.kpiCaptions[i] ?? kpi.caption;
            return (
              <div
                key={label}
                className="rounded-xl border border-(--color-border) bg-(--color-card) p-3"
              >
                <div className="flex items-center justify-between text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                  {label}
                  <GitCommitHorizontal className="size-3 text-(--color-muted-foreground)" />
                </div>
                <div className="mt-2 font-display text-xl font-bold tracking-tight text-(--color-foreground) tabular">
                  {kpi.value}
                </div>
                <div className="mt-0.5 font-mono text-[10px] text-(--color-muted-foreground)">
                  {caption}
                </div>
              </div>
            );
          })}
        </div>

        {/* Pipeline + Activity */}
        <div className="mt-3 grid grid-cols-[1.4fr_1fr] gap-3">
          <div className="rounded-xl border border-(--color-border) bg-(--color-card) p-3">
            <div className="text-[10px] font-semibold uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {t.platform.mockup.pipelineTitle}
            </div>
            <ul className="mt-3 space-y-2">
              {[
                { label: "Collect", state: "in_progress", pct: 60 },
                { label: "Filter", state: "in_progress", pct: 35 },
                { label: "Rank", state: "planned", pct: 0 },
                { label: "Prepare", state: "planned", pct: 0 },
                { label: "Track", state: "planned", pct: 0 },
              ].map((r) => (
                <li key={r.label} className="text-[11.5px]">
                  <div className="flex items-center justify-between">
                    <span
                      className={cn(
                        "inline-flex items-center gap-1.5 truncate",
                        r.state === "in_progress"
                          ? "text-(--color-foreground) font-medium"
                          : "text-(--color-muted-foreground)",
                      )}
                    >
                      {r.state === "in_progress" ? (
                        <LoaderCircle className="size-3 text-(--color-brand-600) animate-spin" />
                      ) : (
                        <CircleDashed className="size-3 text-(--color-muted-foreground)" />
                      )}
                      {r.label}
                    </span>
                    <span className="font-mono text-[10px] text-(--color-muted-foreground)">
                      {r.state === "in_progress" ? `${r.pct}%` : "soon"}
                    </span>
                  </div>
                  <div className="mt-1 h-1 overflow-hidden rounded-full bg-(--color-muted)">
                    <div
                      className={cn(
                        "h-full rounded-full transition-all duration-700",
                        r.state === "in_progress"
                          ? "bg-(--color-brand-500)"
                          : "bg-transparent",
                      )}
                      style={{ width: `${r.pct}%` }}
                    />
                  </div>
                </li>
              ))}
            </ul>
          </div>

          <div className="rounded-xl border border-(--color-border) bg-(--color-card) p-3">
            <div className="text-[10px] font-semibold uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {t.platform.mockup.activityTitle}
            </div>
            <ul className="mt-3 space-y-2">
              {t.labNotes.entries.slice(0, 4).map((entry) => (
                <li
                  key={entry.date + entry.module}
                  className="grid grid-cols-[44px_1fr] gap-2 text-[11px]"
                >
                  <span className="font-mono text-[10px] text-(--color-muted-foreground)">
                    {entry.date}
                  </span>
                  <span className="truncate text-(--color-foreground)/85">
                    <span className="text-(--color-brand-600) dark:text-(--color-brand-400)">
                      {entry.module}
                    </span>
                    {" · "}
                    {entry.text}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </div>
    </div>
  );
}
