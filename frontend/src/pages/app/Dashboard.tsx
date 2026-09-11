import { Activity, Hammer, Radar, Target } from "lucide-react";
import { EmptyState, PageHeader, SectionCard } from "@/components/workspace";
import { useCommand } from "@/lib/command";
import { useT } from "@/lib/i18n";

const TILE_ICONS = {
  status: Hammer,
  activeModules: Target,
  currentFocus: Radar,
  nextMilestone: Activity,
} as const;

export function DashboardPage() {
  const t = useT();
  const { open: openPalette } = useCommand();
  const tiles = t.app.dashboard.tiles;

  const tileEntries: Array<{
    key: keyof typeof TILE_ICONS;
    label: string;
    value: string;
  }> = [
    { key: "status",        label: tiles.status.label,        value: tiles.status.value },
    { key: "activeModules", label: tiles.activeModules.label, value: tiles.activeModules.value },
    { key: "currentFocus",  label: tiles.currentFocus.label,  value: tiles.currentFocus.value },
    { key: "nextMilestone", label: tiles.nextMilestone.label, value: tiles.nextMilestone.value },
  ];

  return (
    <div className="max-w-7xl space-y-8">
      <PageHeader
        eyebrow="C.O.R.S.I"
        title={t.app.dashboard.title}
        description={t.app.dashboard.description}
      />

      <section className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(180px,1fr))]">
        {tileEntries.map((tile) => {
          const Icon = TILE_ICONS[tile.key];
          return (
            <div
              key={tile.key}
              className="flex flex-col gap-3 rounded-2xl border border-(--color-border) bg-(--color-card) p-5 shadow-(--shadow-soft)"
            >
              <div className="flex items-center justify-between">
                <span className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                  {tile.label}
                </span>
                <Icon className="size-4 text-(--color-muted-foreground)" />
              </div>
              <p className="font-display text-2xl font-semibold tracking-tight text-(--color-foreground)">
                {tile.value}
              </p>
            </div>
          );
        })}
      </section>

      <SectionCard title={t.app.dashboard.recentUpdates.title}>
        <EmptyState
          icon={Activity}
          title={t.app.dashboard.recentUpdates.empty.title}
          description={t.app.dashboard.recentUpdates.empty.body}
          action={
            <button
              type="button"
              onClick={openPalette}
              className="inline-flex items-center gap-2 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) transition-colors hover:bg-(--color-muted)"
            >
              {t.app.palette.trigger}
              <kbd className="rounded border border-(--color-border) bg-(--color-muted) px-1.5 py-0.5 font-mono text-[10px] text-(--color-muted-foreground)">
                {t.app.palette.shortcutHint}
              </kbd>
            </button>
          }
        />
      </SectionCard>
    </div>
  );
}
