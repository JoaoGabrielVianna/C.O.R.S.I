/**
 * One room, as a space.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THIS PAGE OWNS THE VERDICT, NOT THE GEOMETRY
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * It measures the container, decides between the spatial scene and the
 * room's Library representation, opens the inspector, and hands the
 * Library the pile's navigation. It computes no position: every point it
 * draws came from `layout()` by way of `sceneItems()`.
 */

import { useState } from "react";
import { ArrowLeft, List, X } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";

import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge, StatusBadge } from "@/modules/palace/components/Badges";
import { useArtifactKindLabel } from "@/modules/palace/components/labels";
import { useRoomScene } from "@/modules/palace/hooks/useRoomScene";
import { useSceneFit } from "@/modules/palace/hooks/useSceneFit";
import { SceneFrame } from "@/modules/palace/scene/SceneFrame";
import { decorationFor } from "@/modules/palace/scene/decoration";
import { sceneBounds } from "@/modules/palace/scene/sceneBounds";
import type { SceneItem } from "@/modules/palace/scene/sceneModel";
import { ArtifactContent } from "@/modules/palace/components/ArtifactContent";
import { usePalaceArtifact } from "@/modules/palace/hooks/usePalace";

import { RoomLibrary } from "./RoomLibrary";

/** `?view=spatial` is the only place the override lives. Never storage. */
const FORCE_PARAM = "view";

export function RoomView() {
  const t = useT();
  const fmt = useFormat();
  const navigate = useNavigate();
  const { roomId } = useParams<{ roomId: string }>();
  const [search, setSearch] = useSearchParams();
  const kindLabel = useArtifactKindLabel();

  const scene = useRoomScene(roomId);
  const bounds = sceneBounds(scene.layout);
  const forced = search.get(FORCE_PARAM) === "spatial";
  const { ref, fit } = useSceneFit(bounds, forced);

  const [selected, setSelected] = useState<SceneItem | null>(null);
  const [returnFocusKey, setReturnFocusKey] = useState<string | null>(null);

  const room = scene.room;
  const decoration = decorationFor(roomId ?? "");

  const labelOf = (item: SceneItem): string => {
    if (item.type === "memory") return t.app.palace.scene.memorySurface;
    if (item.type === "pile") {
      return t.app.palace.scene.pile
        .replace("{count}", fmt.number(item.count ?? 0))
        .replace("{kind}", kindLabel(item.kind!));
    }
    // Type and title. Never a position: "third from the left" is not
    // something a reader without the picture can use.
    return `${kindLabel(item.kind!)}: ${scene.titles.get(item.objectId ?? "") ?? ""}`;
  };

  const activate = (item: SceneItem) => {
    if (item.type === "pile") {
      navigate(
        `/app/modules/palace/library?tab=artifacts&room=${roomId}&kind=${item.kind}`,
      );
      return;
    }
    if (item.type === "memory") {
      navigate(`/app/modules/palace/library?tab=memories&room=${roomId}`);
      return;
    }
    setReturnFocusKey(item.key);
    setSelected(item);
  };

  const closeInspector = () => {
    setSelected(null);
    // Focus goes back where it came from. A panel that closes onto the top
    // of the document loses a keyboard reader's place entirely.
    //
    // ── Why the element is found by comparing, not by a selector ───────
    // An attribute selector would need the key escaped, and `CSS.escape`
    // is a browser global that is simply absent in some environments. The
    // first version used it, threw where it was missing, and the focus
    // silently never returned — with a test that passed anyway, because
    // focus had not moved in the first place. Walking the nodes needs no
    // CSS parsing and cannot throw.
    if (!returnFocusKey) return;
    requestAnimationFrame(() => {
      const target = [...document.querySelectorAll<HTMLButtonElement>("[data-hit-key]")].find(
        (el) => el.dataset.hitKey === returnFocusKey,
      );
      target?.focus();
    });
  };

  if (scene.error) {
    return (
      <Shell roomId={roomId}>
        <div className="rounded-xl border border-(--color-border) p-8 text-center">
          <p className="text-sm font-medium">{t.app.library.states.errorTitle}</p>
          <p className="mt-1.5 text-sm text-(--color-muted-foreground)">
            {scene.error.message}
          </p>
          <Button variant="outline" size="sm" className="mt-3" onClick={scene.refetch}>
            {t.app.library.states.retry}
          </Button>
        </div>
      </Shell>
    );
  }

  const artifactCount = room?.artifact_count ?? 0;
  const memoryCount = room?.memory_count ?? 0;
  const summary = t.app.palace.scene.roomSummary
    .replace("{artifacts}", fmt.number(artifactCount))
    .replace("{memories}", fmt.number(memoryCount));

  const empty = !scene.isPending && scene.layout.containers.length === 0 && !scene.layout.memorySurface;

  return (
    <Shell
      roomId={roomId}
      room={room}
      summary={summary}
      onEscape={selected ? closeInspector : undefined}
    >
      {/*
        ══════════════════════════════════════════════════════════════════
          THE BOX THAT IS MEASURED HOLDS NOTHING. THAT IS THE WHOLE FIX
        ══════════════════════════════════════════════════════════════════

        ── The loop this shape exists to break ─────────────────────────
        The `ref` used to sit on this container, and the container held the
        presenter. So the verdict changed the content, the content changed
        the container's height, and the next measurement was taken from a
        box the previous verdict had resized. The decision was an input to
        itself.

        Measured, same page instance, shrinking to 380px and back:

          1440x900 → 1088x762  spatial
           900x800 →  812x569  spatial
           600x800 →  536x556  LIBRARY
           900x800 →  812x548  LIBRARY   same viewport, 21px less
          1440x900 → 1088x640  LIBRARY   same viewport, 122px less
          1500x950 → 1148x804  spatial   only here does it come back

        A window that supported the room refused to support it again until
        it grew past where it started. Nothing about the room had changed.

        ── What makes it stop ──────────────────────────────────────────
        Both children below are OUT OF FLOW. With no in-flow child, this
        area has no content height to contribute: what remains is the flex
        algorithm and `min-h`, which depend on the window and nothing else.
        The measurer is an empty box pinned to that area, and the presenter
        is its sibling, scrolling inside its own bounds.

          stable area (flex-1, min-h, overflow-hidden)
          ├── measurer   absolute inset-0, EMPTY, observed
          └── presenter  absolute inset-0, scene OR library

        The thresholds are untouched: 44 to hand over, 52 to take back.
        This slice changes where the number comes from, never the number.
      */}
      <div
        data-testid="scene-container"
        className="relative min-h-[26rem] flex-1 shrink overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card)"
      >
        {/*
          The measurement source. It is empty, it is `aria-hidden`, and it
          takes no pointer events: it exists to have a size. Putting
          anything inside it, or moving the `ref` back onto a box that
          holds the presenter, restores the loop — `RoomView.measure.test`
          fails if either happens.
        */}
        <div
          ref={ref}
          data-testid="scene-measure"
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
        />

        {/*
          The presenter. Absolute, so its content cannot push the area it
          is measured against; `overflow-y-auto`, so a long Library scrolls
          inside the room's box instead of stretching it.
        */}
        <div data-testid="scene-presenter" className="absolute inset-0 overflow-y-auto">
          {fit && fit.mode === "library" ? (
            <div className="p-4">
              <div
                role="status"
                className="mb-4 rounded-xl border border-(--color-border) bg-(--color-muted)/60 p-4"
              >
                <p className="text-sm font-medium">{t.app.palace.scene.tooSmallTitle}</p>
                <p className="mt-1 text-sm text-(--color-muted-foreground)">
                  {t.app.palace.scene.tooSmallBody}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-3"
                  onClick={() => {
                    const next = new URLSearchParams(search);
                    next.set(FORCE_PARAM, "spatial");
                    setSearch(next);
                  }}
                >
                  {t.app.palace.scene.forceSpatial}
                </Button>
              </div>
              <RoomLibrary roomId={roomId} />
            </div>
          ) : empty ? (
            <div className="flex h-full flex-col items-center justify-center p-10 text-center">
              <p className="text-sm font-medium">{t.app.palace.scene.emptyRoomTitle}</p>
              <p className="mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
                {t.app.palace.scene.emptyRoomBody}
              </p>
            </div>
          ) : (
            <SceneFrame
              layout={scene.layout}
              decoration={decoration}
              bounds={bounds}
              fit={fit}
              labelOf={labelOf}
              onActivate={activate}
              selectedKey={selected?.key}
              roomLabel={room?.name ?? ""}
              roomDescription={summary}
            />
          )}
        </div>

        {/*
          The inspector, over the room rather than under it.

          ── Why it cannot be a sibling below the scene ─────────────────
          It used to be, and with the measured area finally sized to the
          visible space that turned into a second version of the same bug:
          opening an object added an in-flow card to the column, the column
          had less room for the scene, the scene measured smaller, D5
          handed over to the Library — and the object somebody had just
          activated vanished, taking the focus with it. Measured at
          1440x900: Enter opened the panel and Escape had nothing to close
          because the scene was gone and focus had fallen to <body>.

          The old shape hid this by overflowing the viewport: the panel
          went below the fold instead of taking the scene's height. That is
          not a fix, it is a different way to lose the panel.

          Absolute, so the geometry the fit reads is untouched: the room
          stays exactly where it was, behind the panel, and the object that
          was activated is still on screen and still focused. It remains a
          real `role="dialog"`, Escape still closes it, and focus still
          returns to the object it came from.
        */}
        {selected?.objectId ? (
          <div className="absolute inset-x-0 bottom-0 max-h-[75%] overflow-y-auto p-3">
            <Inspector artifactId={selected.objectId} onClose={closeInspector} />
          </div>
        ) : null}
      </div>
    </Shell>
  );
}

function Shell({
  roomId,
  room,
  summary,
  onEscape,
  children,
}: {
  roomId: string | undefined;
  room?: { name: string; status: string; sensitivity: string } | undefined;
  summary?: string;
  onEscape?: () => void;
  children: React.ReactNode;
}) {
  const t = useT();
  return (
    /*
      Escape is caught for the whole room, not just for the panel.
      Activating an object leaves focus ON THE OBJECT, so a handler bound
      to the panel would only work for somebody who had already tabbed
      into it — which is nobody, immediately after opening it.
    */
    <div
      /*
        `flex-1 min-h-0`, not `h-full`.
        The shell's chain is flex all the way down (`main` → container →
        page), so a page claims the remaining viewport height by GROWING,
        not by asking for 100% of a parent that has no definite height.
        With `h-full` the room collapsed to its `min-h`, D5 correctly
        decided the objects would be too small to press, and the spatial
        scene never appeared on a 900px-tall desktop. Found in a browser;
        no amount of jsdom would have shown it.
      */
      className="flex min-h-0 flex-1 flex-col gap-4 px-4 py-6 sm:px-6"
      onKeyDown={(e) => {
        if (e.key === "Escape" && onEscape) {
          e.stopPropagation();
          onEscape();
        }
      }}
    >
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <Link
            to="/app/modules/palace"
            className="inline-flex items-center gap-1.5 rounded-lg text-sm text-(--color-muted-foreground) outline-none hover:text-(--color-foreground) focus-visible:ring-2 focus-visible:ring-(--color-ring)/50"
          >
            <ArrowLeft aria-hidden="true" className="size-4" />
            {t.app.palace.scene.backToMap}
          </Link>
          <h1 className="mt-1 text-2xl font-semibold">{room?.name ?? ""}</h1>
          {summary ? (
            <p className="mt-1 text-sm text-(--color-muted-foreground)">{summary}</p>
          ) : null}
          <div className="mt-2 flex gap-1.5">
            {room ? (
              <>
                <StatusBadge status={room.status as "active" | "archived"} />
                <SensitivityBadge sensitivity={room.sensitivity as "normal" | "private"} />
              </>
            ) : null}
          </div>
        </div>

        {/* The visible semantic equivalent. Always present, never a
            fallback: a reader who cannot use the room has the same rows. */}
        <Button variant="outline" size="sm" asChild>
          <Link to={`/app/modules/palace/library?tab=artifacts&room=${roomId ?? ""}`}>
            <List aria-hidden="true" />
            {t.app.palace.scene.seeAsList}
          </Link>
        </Button>
      </header>
      {children}
    </div>
  );
}

/**
 * The Artifact Inspector.
 *
 * Reuses the Library's presentation through `ArtifactContent`, so the body
 * and the entries are rendered by one component rather than two that drift.
 * No neighbours, no archive, no relation UI, no writes: this exists to
 * prove the spatial interaction reaches real content.
 */
function Inspector({ artifactId, onClose }: { artifactId: string; onClose: () => void }) {
  const t = useT();
  const kindLabel = useArtifactKindLabel();
  const query = usePalaceArtifact(artifactId, { item_limit: 100, item_offset: 0 });

  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-label={query.data?.title ?? t.app.library.tabs.artifacts}
      className="rounded-2xl border border-(--color-border) bg-(--color-card) p-5 shadow-(--shadow-card)"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        }
      }}
    >
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          {query.data ? (
            <>
              <p className="text-xs font-medium tracking-wide text-(--color-muted-foreground) uppercase">
                {kindLabel(query.data.kind)}
              </p>
              <h2 className="mt-0.5 text-lg font-semibold">{query.data.title}</h2>
            </>
          ) : null}
        </div>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label={t.app.palace.scene.inspectorClose}>
          <X aria-hidden="true" />
        </Button>
      </div>

      {query.isPending ? (
        <div aria-busy="true" aria-label={t.app.library.states.loading} className="space-y-3">
          {[0, 1, 2].map((i) => (
            <div key={i} aria-hidden="true" className="h-4 animate-pulse rounded bg-(--color-muted)" />
          ))}
        </div>
      ) : query.isError ? (
        <p className="text-sm text-(--color-muted-foreground)">{query.error.message}</p>
      ) : (
        <ArtifactContent artifact={query.data} />
      )}
    </aside>
  );
}
