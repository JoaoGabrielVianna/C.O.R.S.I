import { Link } from "react-router-dom";
import { ArrowRight, History, Layers } from "lucide-react";

import { EmptyState, PageHeader } from "@/components/workspace";
import { useLang, useT } from "@/lib/i18n";
import { useReleaseModules } from "@/modules/releases/hooks/useReleases";
import {
  ModuleStatusBadge,
  StabilityBadge,
  VersionPill,
} from "@/modules/releases/components/ReleaseBits";
import { formatReleaseDate } from "@/modules/releases/format";
import type { ApiModuleCard } from "@/modules/releases/api/releases";

/**
 * Release History — the overview.
 *
 * One card per versioned module. What a card must never do is imply a
 * release that has not been declared: a module whose only record is a
 * draft shows "no published release", not the draft's version.
 */
export function ReleasesHomePage() {
  const t = useT();
  const { data, isLoading, isError, error } = useReleaseModules();

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow={t.app.releases.eyebrow}
        title={t.app.releases.title}
        description={t.app.releases.description}
      />

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="h-44 animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40"
            />
          ))}
        </div>
      ) : isError ? (
        <EmptyState
          icon={History}
          title={t.app.releases.error.title}
          description={error instanceof Error ? error.message : undefined}
        />
      ) : !data || data.length === 0 ? (
        <EmptyState
          icon={Layers}
          title={t.app.releases.empty.title}
          description={t.app.releases.empty.body}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {data.map((m) => (
            <ModuleCard key={m.key} module={m} />
          ))}
        </div>
      )}
    </div>
  );
}

function ModuleCard({ module: m }: { module: ApiModuleCard }) {
  const t = useT();
  const [lang] = useLang();
  const current = m.current_release;

  return (
    <Link
      to={`/app/releases/${m.key}`}
      className={[
        "group flex flex-col rounded-2xl border border-(--color-border) bg-(--color-card) p-5",
        "shadow-(--shadow-soft) transition-colors duration-150",
        "hover:border-(--color-brand-500)/40 hover:bg-(--color-muted)/30",
      ].join(" ")}
    >
      <div className="flex items-start justify-between gap-3">
        <h2 className="font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
          {m.name}
        </h2>
        <ModuleStatusBadge status={m.status} label={t.app.releases.moduleStatus[m.status]} />
      </div>

      <div className="mt-1.5 flex items-baseline gap-2">
        {current ? (
          <>
            <VersionPill version={current.version} />
            <StabilityBadge
              stability={current.stability}
              label={t.app.releases.stability[current.stability]}
            />
          </>
        ) : (
          <span className="font-mono text-[12.5px] text-(--color-muted-foreground)">
            {t.app.releases.noCurrent}
          </span>
        )}
      </div>

      <p className="mt-3 line-clamp-3 flex-1 text-[13px] leading-relaxed text-(--color-muted-foreground)">
        {m.description}
      </p>

      <div className="mt-4 flex items-end justify-between gap-3 border-t border-(--color-border) pt-3">
        <div className="min-w-0 space-y-0.5">
          <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {m.release_count === 1
              ? t.app.releases.releaseCount.one
              : t.app.releases.releaseCount.many.replace("{n}", String(m.release_count))}
          </p>
          {m.last_released_at ? (
            <p className="truncate text-[11.5px] text-(--color-muted-foreground)">
              {t.app.releases.updated.replace(
                "{date}",
                formatReleaseDate(m.last_released_at, lang),
              )}
            </p>
          ) : null}
        </div>
        <ArrowRight className="size-4 shrink-0 text-(--color-muted-foreground) transition-transform duration-150 group-hover:translate-x-0.5 group-hover:text-(--color-brand-600) dark:group-hover:text-(--color-brand-400)" />
      </div>
    </Link>
  );
}
