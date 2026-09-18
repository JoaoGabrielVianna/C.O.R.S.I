/**
 * The three read-by-id views the Library can open.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A 404 HERE SAYS ONLY "NOT FOUND", AND THAT IS THE WHOLE POINT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The backend answers the same not-found for a fabricated id, another
 * workspace's row, a withheld row, and something inside a withheld room.
 * This screen must not add a word that tells them apart. There is no
 * "access denied" copy in this file, no lock icon and no hint, because
 * there is no such state to describe: for this surface, content the
 * backend did not return does not exist.
 *
 * ── What is NOT here ───────────────────────────────────────────────────
 * Neighbours. The API exists and is proved, and the inspector that shows
 * relations is a later slice. Rendering it early would be the visual half
 * of a feature whose interaction design has not been decided.
 */

import { ArrowLeft } from "lucide-react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge, StatusBadge } from "@/modules/palace/components/Badges";
import { useArtifactKindLabel, useConfidenceLabel, useMemoryKindLabel } from "@/modules/palace/components/labels";
import {
  usePalaceArtifact,
  usePalaceMemory,
  usePalaceRoom,
} from "@/modules/palace/hooks/usePalace";
import { UNFILED } from "@/modules/palace/api/palace";
import { ArtifactContent } from "@/modules/palace/components/ArtifactContent";

const ITEMS_PER_PAGE = 100;

/* ── shell ───────────────────────────────────────────────────────────── */

function DetailShell({
  eyebrow,
  title,
  badges,
  children,
}: {
  eyebrow: string;
  title: string;
  badges?: React.ReactNode;
  children: React.ReactNode;
}) {
  const t = useT();
  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-8 sm:px-6">
      <Link
        to="/app/modules/palace/library"
        className="inline-flex items-center gap-1.5 rounded-lg text-sm text-(--color-muted-foreground) outline-none hover:text-(--color-foreground) focus-visible:ring-2 focus-visible:ring-(--color-ring)/50"
      >
        <ArrowLeft aria-hidden="true" className="size-4" />
        {t.app.library.detail.back}
      </Link>

      <header className="mt-4 mb-6">
        <p className="text-xs font-medium tracking-wide text-(--color-muted-foreground) uppercase">
          {eyebrow}
        </p>
        <h1 className="mt-1 text-2xl font-semibold text-(--color-foreground)">{title}</h1>
        {badges ? <div className="mt-2.5 flex flex-wrap gap-1.5">{badges}</div> : null}
      </header>

      {children}
    </div>
  );
}

function DetailState({ error, pending }: { error: Error | null; pending: boolean }) {
  const t = useT();

  if (pending) {
    return (
      <div aria-busy="true" aria-label={t.app.library.states.loading} className="space-y-3">
        {[0, 1, 2].map((i) => (
          <div
            key={i}
            aria-hidden="true"
            className="h-4 animate-pulse rounded bg-(--color-muted)"
            style={{ width: `${[85, 70, 55][i]}%` }}
          />
        ))}
      </div>
    );
  }

  const notFound = error instanceof ApiError && error.status === 404;
  return (
    <div className="rounded-xl border border-(--color-border) p-8 text-center">
      <p className="text-sm font-medium text-(--color-foreground)">
        {notFound ? t.app.library.detail.notFoundTitle : t.app.library.states.errorTitle}
      </p>
      <p className="mx-auto mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
        {notFound
          ? t.app.library.detail.notFoundBody
          : (error?.message ?? t.app.library.states.errorFallback)}
      </p>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs font-medium text-(--color-muted-foreground)">{label}</dt>
      <dd className="mt-0.5 text-sm text-(--color-foreground)">{value}</dd>
    </div>
  );
}

/* ── room ────────────────────────────────────────────────────────────── */

export function RoomDetailPage() {
  const t = useT();
  const fmt = useFormat();
  const { roomId } = useParams<{ roomId: string }>();
  const query = usePalaceRoom(roomId);

  if (!query.isSuccess) {
    return (
      <DetailShell eyebrow={t.app.library.tabs.rooms} title="">
        <DetailState error={query.error} pending={query.isPending} />
      </DetailShell>
    );
  }

  const room = query.data;
  return (
    <DetailShell
      eyebrow={t.app.library.tabs.rooms}
      title={room.name}
      badges={
        <>
          <StatusBadge status={room.status} />
          <SensitivityBadge sensitivity={room.sensitivity} />
        </>
      }
    >
      {room.description ? (
        <p className="mb-6 max-w-prose text-sm whitespace-pre-wrap text-(--color-foreground)">
          {room.description}
        </p>
      ) : null}

      <dl className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Field
          label={t.app.library.tabs.artifacts}
          value={fmt.number(room.artifact_count)}
        />
        <Field label={t.app.library.tabs.memories} value={fmt.number(room.memory_count)} />
        <Field
          label={t.app.palace.status.archived}
          value={fmt.number(room.archived_count)}
        />
        <Field
          label={t.app.library.detail.updatedAt}
          value={fmt.date(room.updated_at, "short")}
        />
      </dl>

      <Button variant="outline" size="sm" asChild>
        <Link to={`/app/modules/palace/library?tab=artifacts&room=${room.room_id}`}>
          {t.app.library.detail.openRoom}
        </Link>
      </Button>
    </DetailShell>
  );
}

/* ── artifact ────────────────────────────────────────────────────────── */

export function ArtifactDetailPage() {
  const t = useT();
  const fmt = useFormat();
  const { artifactId } = useParams<{ artifactId: string }>();
  const [search, setSearch] = useSearchParams();

  // Item paging lives in the URL for the same reason the Library's does:
  // page four of a long checklist has to survive a refresh.
  const itemOffset = Math.max(0, Number.parseInt(search.get("items") ?? "", 10) || 0);
  const query = usePalaceArtifact(artifactId, {
    item_limit: ITEMS_PER_PAGE,
    item_offset: itemOffset,
  });
  const kindLabel = useArtifactKindLabel();

  if (!query.isSuccess) {
    return (
      <DetailShell eyebrow={t.app.library.tabs.artifacts} title="">
        <DetailState error={query.error} pending={query.isPending} />
      </DetailShell>
    );
  }

  const artifact = query.data;
  const lastItem = Math.min(artifact.item_offset + ITEMS_PER_PAGE, artifact.item_total);

  return (
    <DetailShell
      eyebrow={kindLabel(artifact.kind)}
      title={artifact.title}
      badges={
        <>
          <StatusBadge status={artifact.status} />
          <SensitivityBadge sensitivity={artifact.sensitivity} />
        </>
      }
    >
      <dl className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-3">
        <Field
          label={t.app.library.detail.room}
          value={artifact.room ? artifact.room.name : t.app.palace.unfiled}
        />
        <Field
          label={t.app.library.detail.createdAt}
          value={fmt.date(artifact.created_at, "short")}
        />
        <Field
          label={t.app.library.detail.updatedAt}
          value={fmt.date(artifact.updated_at, "short")}
        />
      </dl>

      <ArtifactContent artifact={artifact} />

      {artifact.item_total > ITEMS_PER_PAGE ? (
        <div className="mt-3 flex items-center justify-between gap-3">
          <p className="text-xs text-(--color-muted-foreground)">
            {t.app.library.detail.itemsPage
              .replace("{first}", fmt.number(artifact.item_offset + 1))
              .replace("{last}", fmt.number(lastItem))
              .replace("{total}", fmt.number(artifact.item_total))}
          </p>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={itemOffset <= 0}
              onClick={() => {
                const next = new URLSearchParams(search);
                next.set("items", String(Math.max(0, itemOffset - ITEMS_PER_PAGE)));
                setSearch(next);
              }}
            >
              {t.app.library.pagination.previous}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={lastItem >= artifact.item_total}
              onClick={() => {
                const next = new URLSearchParams(search);
                next.set("items", String(itemOffset + ITEMS_PER_PAGE));
                setSearch(next);
              }}
            >
              {t.app.library.pagination.next}
            </Button>
          </div>
        </div>
      ) : null}
    </DetailShell>
  );
}

/* ── memory ──────────────────────────────────────────────────────────── */

export function MemoryDetailPage() {
  const t = useT();
  const fmt = useFormat();
  const { memoryId } = useParams<{ memoryId: string }>();
  const query = usePalaceMemory(memoryId);
  const kindLabel = useMemoryKindLabel();
  const confidenceLabel = useConfidenceLabel();

  if (!query.isSuccess) {
    return (
      <DetailShell eyebrow={t.app.library.tabs.memories} title="">
        <DetailState error={query.error} pending={query.isPending} />
      </DetailShell>
    );
  }

  const memory = query.data;
  return (
    <DetailShell
      eyebrow={kindLabel(memory.kind)}
      title={memory.summary || t.app.library.tabs.memories}
      badges={
        <>
          <StatusBadge status={memory.status} />
          <SensitivityBadge sensitivity={memory.sensitivity} />
        </>
      }
    >
      <dl className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Field
          label={t.app.library.detail.importance}
          value={fmt.number(memory.importance)}
        />
        <Field
          label={t.app.library.detail.confidence}
          value={confidenceLabel(memory.confidence)}
        />
        <Field
          label={t.app.library.detail.occurredAt}
          value={memory.occurred_at ? fmt.date(memory.occurred_at, "short") : "—"}
        />
        <Field
          label={t.app.library.detail.createdAt}
          value={fmt.date(memory.created_at, "short")}
        />
      </dl>

      <section className="mb-6">
        <h2 className="mb-2 text-sm font-semibold text-(--color-foreground)">
          {t.app.library.detail.content}
        </h2>
        <p className="max-w-prose text-sm whitespace-pre-wrap text-(--color-foreground)">
          {memory.content}
        </p>
      </section>

      {memory.artifact_id ? (
        <Button variant="outline" size="sm" asChild>
          <Link to={`/app/modules/palace/library/artifacts/${memory.artifact_id}`}>
            {t.app.library.detail.openArtifact}
          </Link>
        </Button>
      ) : null}

      {memory.room_id === null ? (
        <p className="mt-4 text-xs text-(--color-muted-foreground)">
          {t.app.palace.unfiled}
        </p>
      ) : null}
    </DetailShell>
  );
}

export { UNFILED };
