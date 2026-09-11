import { Link, useParams } from "react-router-dom";
import { ArrowLeft, History } from "lucide-react";

import { EmptyState, PageHeader } from "@/components/workspace";
import { cn } from "@/lib/utils";
import { useLang, useT } from "@/lib/i18n";
import { useReleaseModule } from "@/modules/releases/hooks/useReleases";
import {
  ModuleStatusBadge,
  ReleaseStatusBadge,
  StabilityBadge,
  VersionPill,
} from "@/modules/releases/components/ReleaseBits";
import { formatReleaseDate } from "@/modules/releases/format";
import type { ApiRelease } from "@/modules/releases/api/releases";

/**
 * Module detail — the current release, then the timeline.
 *
 * The timeline is vertical and newest-first, and it marks exactly one node
 * as current. A draft sits in the same list, visually distinct and never
 * marked current: the point of showing it is that the owner can see a
 * version has been *recorded* without it claiming to have *shipped*.
 */
export function ModuleTimelinePage() {
  const { moduleKey = "" } = useParams();
  const t = useT();
  const [lang] = useLang();
  const { data, isLoading, isError, error } = useReleaseModule(moduleKey);

  if (isLoading) {
    return <div className="h-64 animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40" />;
  }
  if (isError || !data) {
    return (
      <div className="space-y-6">
        <BackLink label={t.app.releases.back} />
        <EmptyState
          icon={History}
          title={t.app.releases.notFound.title}
          description={error instanceof Error ? error.message : t.app.releases.notFound.body}
        />
      </div>
    );
  }

  const current = data.current_release;

  return (
    <div className="space-y-8">
      <BackLink label={t.app.releases.back} />

      <PageHeader
        eyebrow={t.app.releases.eyebrow}
        title={data.name}
        description={data.description}
        actions={
          <ModuleStatusBadge status={data.status} label={t.app.releases.moduleStatus[data.status]} />
        }
      />

      {/* Current release, stated plainly — or its honest absence. */}
      <section className="rounded-2xl border border-(--color-border) bg-(--color-card) p-5 shadow-(--shadow-soft)">
        <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
          {t.app.releases.currentRelease}
        </p>
        {current ? (
          <div className="mt-2 flex flex-wrap items-center gap-3">
            <VersionPill version={current.version} className="text-lg" />
            <StabilityBadge
              stability={current.stability}
              label={t.app.releases.stability[current.stability]}
            />
            <ReleaseStatusBadge status={current.status} label={t.app.releases.status.published} />
            <span className="text-[13px] text-(--color-muted-foreground)">
              {t.app.releases.releasedOn.replace(
                "{date}",
                formatReleaseDate(current.released_at, lang),
              )}
            </span>
          </div>
        ) : (
          <p className="mt-2 text-[13px] text-(--color-muted-foreground)">
            {t.app.releases.noCurrentBody}
          </p>
        )}
      </section>

      <section className="space-y-4">
        <h2 className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {t.app.releases.timeline}
        </h2>

        {data.releases.length === 0 ? (
          <EmptyState
            icon={History}
            title={t.app.releases.noReleases.title}
            description={t.app.releases.noReleases.body}
          />
        ) : (
          <ol className="relative space-y-1">
            {data.releases.map((rel, i) => (
              <TimelineNode
                key={rel.id}
                release={rel}
                moduleKey={data.key}
                isCurrent={current?.id === rel.id}
                isLast={i === data.releases.length - 1}
              />
            ))}
          </ol>
        )}
      </section>
    </div>
  );
}

function TimelineNode({
  release,
  moduleKey,
  isCurrent,
  isLast,
}: {
  release: ApiRelease;
  moduleKey: string;
  isCurrent: boolean;
  isLast: boolean;
}) {
  const t = useT();
  const [lang] = useLang();

  return (
    <li className="relative flex gap-4">
      {/* The rail: a dot per release, a line between them. The current
          release gets a filled, ringed dot — one glance answers "where are
          we now". */}
      <div className="flex w-4 shrink-0 flex-col items-center pt-4">
        <span
          className={cn(
            "size-2.5 shrink-0 rounded-full border-2",
            isCurrent
              ? "border-(--color-brand-500) bg-(--color-brand-500) ring-4 ring-(--color-brand-500)/15"
              : release.status === "draft"
                ? "border-dashed border-amber-500 bg-transparent"
                : "border-(--color-border) bg-(--color-card)",
          )}
          aria-hidden
        />
        {!isLast ? <span className="mt-1 w-px flex-1 bg-(--color-border)" aria-hidden /> : null}
      </div>

      <Link
        to={`/app/releases/${moduleKey}/${release.version}`}
        className={cn(
          "group mb-2 min-w-0 flex-1 rounded-xl border px-4 py-3 transition-colors duration-150",
          "border-(--color-border) bg-(--color-card) hover:bg-(--color-muted)/40",
          isCurrent ? "border-(--color-brand-500)/40" : "",
        )}
      >
        <div className="flex flex-wrap items-center gap-2">
          <VersionPill version={release.version} />
          {isCurrent ? (
            <span className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-(--color-brand-600) dark:text-(--color-brand-400)">
              {t.app.releases.current}
            </span>
          ) : null}
          {release.status === "draft" ? (
            <ReleaseStatusBadge status="draft" label={t.app.releases.status.draft} />
          ) : (
            <StabilityBadge
              stability={release.stability}
              label={t.app.releases.stability[release.stability]}
            />
          )}
          <span className="ml-auto shrink-0 text-[11.5px] text-(--color-muted-foreground)">
            {release.status === "published"
              ? formatReleaseDate(release.released_at, lang)
              : t.app.releases.notReleased}
          </span>
        </div>
        <p className="mt-1 line-clamp-2 text-[13px] leading-relaxed text-(--color-muted-foreground)">
          {release.summary}
        </p>
      </Link>
    </li>
  );
}

function BackLink({ label }: { label: string }) {
  return (
    <Link
      to="/app/releases"
      className="inline-flex items-center gap-1.5 text-[12.5px] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
    >
      <ArrowLeft className="size-3.5" />
      {label}
    </Link>
  );
}
