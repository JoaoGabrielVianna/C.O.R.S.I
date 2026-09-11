import { Link, useParams } from "react-router-dom";
import { AlertTriangle, ArrowLeft, Check, FileText, History, Scale, Wrench } from "lucide-react";

import { EmptyState, PageHeader, SectionCard } from "@/components/workspace";
import { useLang, useT } from "@/lib/i18n";
import { usePublishRelease, useRelease } from "@/modules/releases/hooks/useReleases";
import {
  MetricTile,
  ReleaseStatusBadge,
  StabilityBadge,
} from "@/modules/releases/components/ReleaseBits";
import { formatReleaseDate } from "@/modules/releases/format";
import type { ApiNote } from "@/modules/releases/api/releases";

/**
 * Release detail — one recorded snapshot, rendered as it was stored.
 *
 * Every section below is optional and disappears when empty, because a
 * release that recorded no decisions should show no decisions rather than
 * an empty heading implying something was lost. The one section that is
 * never hidden is the summary: a release without one cannot exist.
 */
export function ReleaseDetailPage() {
  const { moduleKey = "", version = "" } = useParams();
  const t = useT();
  const [lang] = useLang();
  const { data: rel, isLoading, isError, error } = useRelease(moduleKey, version);
  const publish = usePublishRelease(moduleKey);

  if (isLoading) {
    return <div className="h-64 animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40" />;
  }
  if (isError || !rel) {
    return (
      <div className="space-y-6">
        <BackLink to={`/app/releases/${moduleKey}`} label={t.app.releases.back} />
        <EmptyState
          icon={History}
          title={t.app.releases.notFound.title}
          description={error instanceof Error ? error.message : t.app.releases.notFound.body}
        />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <BackLink to={`/app/releases/${moduleKey}`} label={t.app.releases.back} />

      <PageHeader
        eyebrow={`${moduleKey} / v${rel.version}`}
        title={`v${rel.version}`}
        description={rel.summary}
        actions={
          <div className="flex items-center gap-2">
            <StabilityBadge
              stability={rel.stability}
              label={t.app.releases.stability[rel.stability]}
            />
            <ReleaseStatusBadge
              status={rel.status}
              label={
                rel.status === "published" ? t.app.releases.status.published : t.app.releases.status.draft
              }
            />
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-[13px] text-(--color-muted-foreground)">
        <span>
          {rel.status === "published"
            ? t.app.releases.releasedOn.replace("{date}", formatReleaseDate(rel.released_at, lang))
            : t.app.releases.notReleased}
        </span>
      </div>

      {/* A draft says so, loudly, and offers the one action that changes
          it. Publishing is irreversible: after it, the row is frozen by the
          database, so the button states that rather than implying an edit
          can follow. */}
      {rel.status === "draft" ? (
        <div className="rounded-2xl border border-dashed border-amber-500/40 bg-amber-500/5 p-5">
          <div className="flex items-start gap-3">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
            <div className="min-w-0 flex-1 space-y-3">
              <div>
                <p className="text-[13.5px] font-medium text-(--color-foreground)">
                  {t.app.releases.draftNotice.title}
                </p>
                <p className="mt-1 text-[13px] leading-relaxed text-(--color-muted-foreground)">
                  {t.app.releases.draftNotice.body}
                </p>
              </div>
              <button
                type="button"
                disabled={publish.isPending}
                onClick={() => publish.mutate(rel.version)}
                className={[
                  "inline-flex items-center gap-2 rounded-lg border border-(--color-brand-500)/40",
                  "bg-(--color-brand-500)/10 px-3 py-1.5 text-[13px] font-medium",
                  "text-(--color-brand-700) transition-colors hover:bg-(--color-brand-500)/20",
                  "disabled:cursor-not-allowed disabled:opacity-60 dark:text-(--color-brand-300)",
                ].join(" ")}
              >
                <Check className="size-3.5" />
                {publish.isPending ? t.app.releases.publishing : t.app.releases.publish}
              </button>
              {publish.isError ? (
                <p className="text-[12.5px] text-red-600 dark:text-red-400">
                  {publish.error instanceof Error ? publish.error.message : ""}
                </p>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}

      {rel.capabilities.length > 0 ? (
        <SectionCard title={t.app.releases.sections.capabilities}>
          <ul className="grid gap-2 sm:grid-cols-2">
            {rel.capabilities.map((c) => (
              <li
                key={c.name}
                className="flex gap-2.5 rounded-xl border border-(--color-border) bg-(--color-muted)/20 px-3.5 py-2.5"
              >
                <Check className="mt-0.5 size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
                <div className="min-w-0">
                  <p className="text-[13px] font-medium text-(--color-foreground)">{c.name}</p>
                  {c.note ? (
                    <p className="mt-0.5 text-[12.5px] leading-relaxed text-(--color-muted-foreground)">
                      {c.note}
                    </p>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </SectionCard>
      ) : null}

      {rel.evidence.length > 0 ? (
        <SectionCard
          title={t.app.releases.sections.evidence}
          description={t.app.releases.evidenceNote}
        >
          <div className="grid gap-3 sm:grid-cols-3 lg:grid-cols-5">
            {rel.evidence.map((m) => (
              <MetricTile key={m.label} metric={m} />
            ))}
          </div>
        </SectionCard>
      ) : null}

      {rel.limitations.length > 0 ? (
        <NoteSection
          title={t.app.releases.sections.limitations}
          notes={rel.limitations}
          icon={AlertTriangle}
          tone="text-amber-600 dark:text-amber-400"
        />
      ) : null}

      {rel.decisions.length > 0 ? (
        <NoteSection
          title={t.app.releases.sections.decisions}
          notes={rel.decisions}
          icon={Scale}
          tone="text-(--color-brand-600) dark:text-(--color-brand-400)"
        />
      ) : null}

      {rel.technical_notes.length > 0 ? (
        <NoteSection
          title={t.app.releases.sections.technical}
          notes={rel.technical_notes}
          icon={Wrench}
          tone="text-(--color-muted-foreground)"
        />
      ) : null}

      {rel.doc_refs.length > 0 ? (
        <SectionCard
          title={t.app.releases.sections.docs}
          description={t.app.releases.docsNote}
        >
          <ul className="space-y-1.5">
            {rel.doc_refs.map((d) => (
              <li key={d.path} className="flex items-baseline gap-2.5">
                <FileText className="size-3.5 shrink-0 translate-y-0.5 text-(--color-muted-foreground)" />
                <span className="text-[13px] text-(--color-foreground)">{d.label}</span>
                <code className="truncate font-mono text-[11.5px] text-(--color-muted-foreground)">
                  {d.path}
                </code>
              </li>
            ))}
          </ul>
        </SectionCard>
      ) : null}
    </div>
  );
}

function NoteSection({
  title,
  notes,
  icon: Icon,
  tone,
}: {
  title: string;
  notes: ApiNote[];
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  tone: string;
}) {
  return (
    <SectionCard title={title}>
      <ul className="space-y-3">
        {notes.map((n) => (
          <li key={n.text} className="flex gap-2.5">
            <Icon className={`mt-0.5 size-3.5 shrink-0 ${tone}`} />
            <div className="min-w-0">
              <p className="text-[13px] leading-relaxed text-(--color-foreground)">{n.text}</p>
              {n.ref ? (
                <code className="mt-0.5 inline-block font-mono text-[11px] text-(--color-muted-foreground)">
                  {n.ref}
                </code>
              ) : null}
            </div>
          </li>
        ))}
      </ul>
    </SectionCard>
  );
}

function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link
      to={to}
      className="inline-flex items-center gap-1.5 text-[12.5px] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
    >
      <ArrowLeft className="size-3.5" />
      {label}
    </Link>
  );
}
