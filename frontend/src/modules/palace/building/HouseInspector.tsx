/**
 * Opening an object without leaving the Palace.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   REVEAL IN PLACE BEFORE NAVIGATING AWAY
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Pressing a cabinet in the house shows what is in that list, over the
 * house, with the house still on screen behind it. The reader should
 * understand "I opened this object inside my Palace", never "I went to
 * another screen".
 *
 * ── Why a panel and not a route ────────────────────────────────────────
 * A route change unmounts the scene, and an unmounted scene is the
 * teleport C1.1 exists to stop. Navigation still exists and is still
 * useful, so it is offered as a clearly SECONDARY action inside the panel:
 * the reader chooses to leave rather than being taken.
 *
 * ── Why not a hover tooltip ────────────────────────────────────────────
 * Hover does not exist on touch, cannot be reached by keyboard, and cannot
 * hold a list somebody needs to read. Tooltips can identify; they cannot
 * be the interface for content. So this opens on click, Enter and Space,
 * stays open until dismissed, and closes on Escape with focus returning to
 * the object it came from.
 *
 * ── Read only ──────────────────────────────────────────────────────────
 * No writes, no checkboxes a reader could tick without saving, no
 * inline editing. Writing to the Palace is a conversation with an agent.
 */

import { X } from "lucide-react";
import { Link } from "react-router-dom";

import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";

import { ArtifactContent } from "../components/ArtifactContent";
import { SensitivityBadge, StatusBadge } from "../components/Badges";
import { useArtifactKindLabel } from "../components/labels";
import { usePalaceArtifact, usePalaceMemories } from "../hooks/usePalace";
import type { HouseItem } from "./houseModel";

/**
 * How many entries the panel asks for.
 *
 * The same ceiling the room's own inspector uses. An artifact with more
 * than this says so and offers the full page, rather than showing the
 * first hundred as though they were all of them: a list that is silently
 * short is a list somebody will act on.
 */
const ITEM_LIMIT = 100;

/** How many memories the panel lists before pointing at the Library. */
const MEMORY_LIMIT = 25;

export interface HouseInspectorProps {
  item: HouseItem;
  roomName: string;
  onClose: () => void;
  /**
   * Move the Palace's attention to the room this object stands in.
   *
   * ══════════════════════════════════════════════════════════════════
   *
   *   THIS USED TO BE A LINK OUT OF THE PALACE. IT IS A CAMERA NOW
   *
   * ══════════════════════════════════════════════════════════════════
   *
   * It pointed at `/rooms/:roomId`, which unmounted the house and put a
   * different page on screen — the teleport this whole surface exists to
   * avoid, reached from inside the one panel that had just proved it was
   * not necessary. The room is here. Looking at it is a change of view,
   * not a change of address, and the address does not move.
   *
   * The route still exists for deep links, for the Library and for the
   * fallback list. It is simply no longer how somebody looks at a room
   * they can already see.
   *
   * Absent when there is no house to move the camera around — the small
   * -screen fallback — in which case the panel offers no room action at
   * all rather than a control that would do nothing.
   */
  onFocusRoom?: (roomId: string) => void;
  /**
   * Which room the Palace is already looking at.
   *
   * An object standing in THAT room offers no "look at this room": the
   * reader is already looking at it, and a control that would do nothing
   * is the affordance-that-lies this surface refuses everywhere else. An
   * object in any other room still offers it, which is how attention moves
   * from one room to the next without returning to the overview first.
   */
  focusedRoomId?: string | null;
  /**
   * The tallest this panel may be, as a CSS length.
   *
   * Handed down rather than expressed as `max-h-full`: the anchor is
   * absolutely positioned with an auto height, and a percentage
   * max-height has nothing definite to resolve against there, so the
   * panel quietly ignored it and ran off the bottom of the stage.
   */
  maxHeight: string;
}

export function HouseInspector({
  item,
  roomName,
  onClose,
  onFocusRoom,
  focusedRoomId = null,
  maxHeight,
}: HouseInspectorProps) {
  const t = useT();

  return (
    /*
      Escape is handled by the page, for the whole surface, not just here:
      activating an object leaves focus ON THE OBJECT, so a handler bound
      to this panel would only work for somebody who had already tabbed
      into it — which is nobody, immediately after opening it. The same
      lesson S6.1 recorded for the room's inspector.
    */
    <aside
      role="dialog"
      aria-modal="false"
      aria-label={item.type === "memory" ? t.app.palace.scene.memorySurface : roomName}
      data-testid="house-inspector"
      style={{ maxHeight }}
      /*
        The panel wears the same accent edge the open object does. It is
        anchored beside that object rather than pinned to a corner (see
        `inspectorPlacement`), and the shared edge is what makes the two
        read as one thing instead of a sheet that happens to be on top.
      */
      className="pointer-events-auto overflow-y-auto rounded-2xl border border-(--color-accent)/45 bg-(--color-card)/97 p-4 shadow-(--shadow-lift) ring-1 ring-(--color-accent)/10 backdrop-blur-sm"
    >
      {item.type === "memory" ? (
        <MemoryPanel
          item={item}
          roomName={roomName}
          onClose={onClose}
          onFocusRoom={onFocusRoom}
          focusedRoomId={focusedRoomId}
        />
      ) : (
        <ArtifactPanel
          item={item}
          roomName={roomName}
          onClose={onClose}
          onFocusRoom={onFocusRoom}
          focusedRoomId={focusedRoomId}
        />
      )}
    </aside>
  );
}

function PanelHeader({
  eyebrow,
  title,
  onClose,
  children,
}: {
  eyebrow: string;
  title: string;
  onClose: () => void;
  children?: React.ReactNode;
}) {
  const t = useT();
  return (
    <div className="mb-4 flex items-start justify-between gap-3">
      <div className="min-w-0">
        <p className="text-xs font-medium tracking-wide text-(--color-muted-foreground) uppercase">
          {eyebrow}
        </p>
        <h2 className="mt-0.5 text-lg font-semibold">{title}</h2>
        {children ? <div className="mt-2 flex flex-wrap gap-1.5">{children}</div> : null}
      </div>
      <Button
        variant="ghost"
        size="icon"
        onClick={onClose}
        aria-label={t.app.palace.scene.inspectorClose}
      >
        <X aria-hidden="true" />
      </Button>
    </div>
  );
}

/**
 * One artifact, opened where it stands.
 *
 * This is the case the whole slice is judged on: pressing the drawer
 * cabinet of a list has to show that list's real entries.
 */
function ArtifactPanel({
  item,
  roomName,
  onClose,
  onFocusRoom,
  focusedRoomId,
}: {
  item: HouseItem;
  roomName: string;
  onClose: () => void;
  onFocusRoom?: (roomId: string) => void;
  focusedRoomId?: string | null;
}) {
  const t = useT();
  const fmt = useFormat();
  const kindLabel = useArtifactKindLabel();
  const query = usePalaceArtifact(item.artifactId, {
    item_limit: ITEM_LIMIT,
    item_offset: 0,
  });

  const artifact = query.data;
  const truncated = artifact ? artifact.item_total > artifact.items.length : false;

  return (
    <>
      <PanelHeader
        eyebrow={artifact ? kindLabel(artifact.kind) : kindLabel(item.kind!)}
        title={artifact?.title ?? ""}
        onClose={onClose}
      >
        {artifact ? (
          <>
            {/* Status is shown because an archived artifact reached through
                a stale view would otherwise read as current. */}
            <StatusBadge status={artifact.status} />
            <SensitivityBadge sensitivity={artifact.sensitivity} />
          </>
        ) : null}
      </PanelHeader>

      {query.isPending ? (
        <div aria-busy="true" aria-label={t.app.library.states.loading} className="space-y-3">
          {[0, 1, 2].map((i) => (
            <div key={i} aria-hidden="true" className="h-4 animate-pulse rounded bg-(--color-muted)" />
          ))}
        </div>
      ) : query.isError ? (
        <p className="text-sm text-(--color-muted-foreground)">{query.error.message}</p>
      ) : artifact ? (
        <>
          <ArtifactContent artifact={artifact} />

          {/*
            Never silently short. If the artifact holds more entries than
            one page, the panel says how many and sends the reader to the
            surface that can page through them.
          */}
          {truncated ? (
            <p className="mt-3 text-xs text-(--color-muted-foreground)">
              {t.app.library.detail.itemsPage
                .replace("{first}", fmt.number(1))
                .replace("{last}", fmt.number(artifact.items.length))
                .replace("{total}", fmt.number(artifact.item_total))}
            </p>
          ) : null}

          {/*
            SECONDARY. The primary act was opening the object here; this is
            the reader deciding to leave.
          */}
          <div className="mt-5 flex flex-wrap gap-2 border-t border-(--color-border) pt-4">
            <Button variant="outline" size="sm" asChild>
              <Link to={`/app/modules/palace/library/artifacts/${item.artifactId}`}>
                {t.app.palace.scene.openDetails}
              </Link>
            </Button>
            <FocusRoomAction
              item={item}
              roomName={roomName}
              onFocusRoom={onFocusRoom}
              focusedRoomId={focusedRoomId}
            />
          </div>
        </>
      ) : null}
    </>
  );
}

/**
 * "Look at this room", which moves the camera and stays put otherwise.
 *
 * Short label, full accessible name. Spelling the room out on the button
 * wrapped the two actions onto a second line and pushed them under the
 * fold of a panel this width.
 */
function FocusRoomAction({
  item,
  roomName,
  onFocusRoom,
  focusedRoomId,
}: {
  item: HouseItem;
  roomName: string;
  onFocusRoom?: (roomId: string) => void;
  focusedRoomId?: string | null;
}) {
  const t = useT();
  const roomId = item.roomId;
  if (!roomId || !onFocusRoom || roomId === focusedRoomId) return null;

  return (
    <Button
      variant="ghost"
      size="sm"
      data-testid="focus-room-action"
      onClick={() => onFocusRoom(roomId)}
      aria-label={t.app.palace.scene.focusRoom.replace("{name}", roomName)}
    >
      {t.app.palace.scene.focusRoomShort}
    </Button>
  );
}

/**
 * The room's memories, opened from its surface.
 *
 * ── Why a list here and not one object per memory ──────────────────────
 * Because the surface knows THAT a room has memories and never how many.
 * One notebook per memory would make a room with forty of them unreadable
 * and would publish a count the geometry is forbidden to learn. The
 * notebook is the affordance; the list is what is behind it.
 */
function MemoryPanel({
  item,
  roomName,
  onClose,
  onFocusRoom,
  focusedRoomId,
}: {
  item: HouseItem;
  roomName: string;
  onClose: () => void;
  onFocusRoom?: (roomId: string) => void;
  focusedRoomId?: string | null;
}) {
  const t = useT();
  const fmt = useFormat();
  const query = usePalaceMemories({
    room_id: item.roomId,
    status: "active",
    limit: MEMORY_LIMIT,
    offset: 0,
  });

  const page = query.data;
  const truncated = page ? page.total > page.items.length : false;

  return (
    <>
      <PanelHeader
        eyebrow={t.app.palace.scene.memorySurface}
        title={roomName}
        onClose={onClose}
      />

      {query.isPending ? (
        <div aria-busy="true" aria-label={t.app.library.states.loading} className="space-y-3">
          {[0, 1, 2].map((i) => (
            <div key={i} aria-hidden="true" className="h-4 animate-pulse rounded bg-(--color-muted)" />
          ))}
        </div>
      ) : query.isError ? (
        <p className="text-sm text-(--color-muted-foreground)">{query.error.message}</p>
      ) : page && page.items.length > 0 ? (
        <>
          <ul className="divide-y divide-(--color-border) rounded-xl border border-(--color-border)">
            {page.items.map((memory) => (
              <li key={memory.memory_id} className="px-4 py-2.5">
                <p className="text-sm text-(--color-foreground)">{memory.summary}</p>
                {memory.content_excerpt ? (
                  <p className="mt-1 text-xs text-(--color-muted-foreground)">
                    {memory.content_excerpt}
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
          {truncated ? (
            <p className="mt-3 text-xs text-(--color-muted-foreground)">
              {t.app.library.detail.itemsPage
                .replace("{first}", fmt.number(1))
                .replace("{last}", fmt.number(page.items.length))
                .replace("{total}", fmt.number(page.total))}
            </p>
          ) : null}
          <div className="mt-5 flex flex-wrap gap-2 border-t border-(--color-border) pt-4">
            <Button variant="outline" size="sm" asChild>
              <Link to={`/app/modules/palace/library?tab=memories&room=${item.roomId ?? ""}`}>
                {t.app.palace.scene.openDetails}
              </Link>
            </Button>
            {/*
              The house's way of attending to a room, now that the name on
              the room is a caption rather than a link. Named, secondary,
              and the reader's choice.
            */}
            <FocusRoomAction
              item={item}
              roomName={roomName}
              onFocusRoom={onFocusRoom}
              focusedRoomId={focusedRoomId}
            />
          </div>
        </>
      ) : (
        /*
          The surface said this room has memories and the read came back
          empty. That is a stale view, not a claim that something is
          hidden: it is said plainly and without a number.
        */
        <p className="text-sm text-(--color-muted-foreground)">
          {t.app.library.states.emptyMemoriesBody}
        </p>
      )}
    </>
  );
}
