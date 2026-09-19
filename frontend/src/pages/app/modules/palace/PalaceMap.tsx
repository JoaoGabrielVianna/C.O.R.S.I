/**
 * The Palace's entrance: a house you can look into.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A PALACE YOU CAN INSPECT, NOT A ROOM NAVIGATOR
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── What the two previous versions got wrong, in order ─────────────────
 * The first was a grid of cards with an isometric vignette inside each.
 * Its reasoning was sound as far as it went — a plan positions rooms, a
 * reader takes position for meaning, semantic adjacency is forbidden — but
 * the conclusion was too large: "adjacency must not MEAN anything" does
 * not imply "draw no adjacency".
 *
 * The second drew a connected building and passed every invariant, and the
 * human gate rejected it anyway: a long diagonal of empty shells with dead
 * space around it, where each room was one big navigation card. It was a
 * menu with architecture painted on.
 *
 * This one draws a compact house whose rooms hold the operator's real
 * artifacts, and pressing one opens it WHERE IT STANDS. The rooms are
 * environments; the objects are the controls.
 *
 * ── Why the arrangement is not explained to the reader ─────────────────
 * Rooms are placed in `created_at` order because that is the only order
 * that never moves when meaning is edited. That is a mechanism, and the
 * previous version printed it on the screen ("side by side is chronology,
 * not a relationship"), which turned an implementation detail into a claim
 * about the operator's life. Adjacency simply does not assert a relation.
 * Saying so out loud invites the reading it was denying.
 *
 * ── The verdict, and why this page owns it ─────────────────────────────
 * Same shape as `RoomView`: this page measures, decides between the house
 * and the list, and computes no position. Every point it draws came from
 * `placeBuilding()` and `furnishRoom()`.
 */

import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";

import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge } from "@/modules/palace/components/Badges";
import { useArtifactKindLabel } from "@/modules/palace/components/labels";
import { usePalaceArtifacts, usePalaceOverview } from "@/modules/palace/hooks/usePalace";
import { useBuildingFit } from "@/modules/palace/hooks/useBuildingFit";
import { RoomVignette, UnfiledTray } from "@/modules/palace/scene/Vignettes";
import { decorationFor } from "@/modules/palace/scene/decoration";
import { BuildingFrame } from "@/modules/palace/building/BuildingFrame";
import { HouseInspector } from "@/modules/palace/building/HouseInspector";
import { buildingBounds } from "@/modules/palace/building/bounds";
import {
  buildingRoomsOf,
  interiorArtifactsByRoom,
} from "@/modules/palace/building/fromOverview";
import { furnishRoom, type InteriorOutput } from "@/modules/palace/building/interior";
import { houseItems, type HouseItem } from "@/modules/palace/building/houseModel";
import { inspectorPlacement } from "@/modules/palace/building/inspectorPlacement";
import { BUILDING_VERSION, placeBuilding } from "@/modules/palace/building/placement";
import type { OverviewRoom } from "@/modules/palace/api/types";

/** `?view=spatial` is the only place the override lives. Never storage. */
const FORCE_PARAM = "view";

/**
 * How many artifacts the house asks for, in ONE request.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   ONE READ FOR THE WHOLE HOUSE, NOT ONE PER ROOM
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `GET /palace/artifacts` without a `room_id` returns rows across every
 * room, each carrying its own `room_id`, under the same visibility
 * predicate the counts use. So the furniture for the entire Palace costs
 * one request and the grouping happens here. A request per room would be
 * N+1 on the surface the operator opens first.
 *
 * 100 is the backend's ceiling (`repo.maxLimit`), not a number chosen
 * here: asking for more returns 100 anyway.
 *
 * ── What happens above the ceiling, and why it stays honest ────────────
 * A Palace with more than 100 active artifacts gets a partial page, so
 * some rooms will be short of furniture. The PILE does not come from this
 * page: it is computed from `OverviewRoom.artifact_count`, which the
 * backend counts in full. So a room whose artifacts did not fit in the
 * page still reports the right remainder — the objects drawn are fewer,
 * and the number of objects not drawn individually is still exactly right.
 *
 * The honest limitation, recorded rather than hidden: above the ceiling,
 * WHICH artifacts get drawn individually follows the wire's
 * `updated_at DESC` page, so editing an artifact can change which of a
 * large room's objects are shown by name. Below the ceiling — which is
 * every Palace that exists today — the page holds everything and the
 * arrangement is fully stable. Fixing it above the ceiling needs a
 * read-model that returns the first few artifacts per room, which is a
 * backend change this slice does not make.
 */
const HOUSE_ARTIFACT_LIMIT = 100;

export function PalaceMap() {
  const t = useT();
  const navigate = useNavigate();
  const kindLabel = useArtifactKindLabel();
  const [search, setSearch] = useSearchParams();

  const overview = usePalaceOverview();
  const artifacts = usePalaceArtifacts({
    status: "active",
    limit: HOUSE_ARTIFACT_LIMIT,
    offset: 0,
  });

  const rooms = overview.data?.rooms ?? [];
  const hasUnfiled = (overview.data?.unfiled.artifact_count ?? 0) > 0;

  /*
    The arrangement, computed from ids and creation instants ONLY.

    `buildingRoomsOf` is what makes the wire's `updated_at DESC` harmless:
    it narrows each row to an id and an instant before the placement ever
    sees it, and the placement then sorts canonically.
  */
  const layout = placeBuilding({
    rooms: buildingRoomsOf(rooms),
    buildingVersion: BUILDING_VERSION,
    hasUnfiled,
  });

  const artifactRows = artifacts.data?.items ?? [];
  const byRoom = interiorArtifactsByRoom(artifactRows);

  // The furniture, one room at a time. `artifactTotal` comes from the
  // overview and not from `byRoom`, which is what keeps the pile honest
  // when the page did not reach every artifact.
  const interiors = new Map<string, InteriorOutput>(
    rooms.map((room) => [
      room.room_id,
      furnishRoom({
        roomId: room.room_id,
        artifacts: byRoom.get(room.room_id) ?? [],
        artifactTotal: room.artifact_count,
        hasMemories: room.memory_count > 0,
      }),
    ]),
  );

  const items = houseItems(layout, interiors);
  const bounds = buildingBounds(layout, interiors);
  const forced = search.get(FORCE_PARAM) === "spatial";
  const { ref, fit } = useBuildingFit(bounds, forced);

  const [selected, setSelected] = useState<HouseItem | null>(null);
  const [returnFocusKey, setReturnFocusKey] = useState<string | null>(null);

  const byId = new Map(rooms.map((room) => [room.room_id, room]));
  const nameOf = (roomId: string) => byId.get(roomId)?.name ?? "";
  const titleOf = new Map(artifactRows.map((row) => [row.artifact_id, row.title]));

  /*
    The same rooms, in the same order the house places them.

    A fallback that listed rooms in arrival order would reshuffle itself on
    every edit — the exact defect the placement exists to avoid — and it
    would do it on the surface that small screens and assistive readers
    actually get.
  */
  const orderedRooms = layout.rooms
    .map((placement) => byId.get(placement.roomId))
    .filter((room): room is OverviewRoom => room !== undefined);

  /*
    Type, name, and the room it stands in. Never a position: "third from
    the left" is not something a reader without the picture can use, but
    "in the Atelier" is exactly what a sighted reader gets for free from
    the caption painted on the room.

    That caption is `aria-hidden`, so this is the ONLY place the room
    reaches a screen reader. Dropping it here would leave a keyboard
    reader walking through objects with no idea which room they are in.
  */
  const labelOf = (item: HouseItem): string => {
    const room = item.roomId ? nameOf(item.roomId) : "";
    switch (item.type) {
      case "artifact":
        return t.app.palace.scene.objectInRoom
          .replace("{object}", `${kindLabel(item.kind!)}: ${titleOf.get(item.artifactId ?? "") ?? ""}`)
          .replace("{room}", room);
      case "pile":
        return t.app.palace.scene.morePileInRoom
          .replace("{count}", String(item.count ?? 0))
          .replace("{room}", room);
      case "memory":
        return t.app.palace.scene.memoriesOfRoom.replace("{room}", room);
      default:
        return t.app.palace.scene.openUnfiled;
    }
  };

  const activate = (item: HouseItem) => {
    /*
      A pile stands for artifacts this surface is NOT drawing individually,
      and the tray stands for what is filed nowhere. Neither is one object,
      so neither has one object to open: they go to the Library, filtered
      to exactly what they represent. That is navigation, and it is correct
      here — an inspector showing "17 things" would be a worse list.
    */
    if (item.type === "pile") {
      navigate(`/app/modules/palace/library?tab=artifacts&room=${item.roomId ?? ""}`);
      return;
    }
    if (item.type === "unfiled") {
      navigate("/app/modules/palace/library?tab=artifacts&room=none");
      return;
    }
    setReturnFocusKey(item.key);
    setSelected(item);
  };

  const closeInspector = () => {
    setSelected(null);
    /*
      Focus goes back where it came from. A panel that closes onto the top
      of the document loses a keyboard reader's place entirely.

      The element is found by COMPARING rather than by a selector: an
      attribute selector would need the key escaped, and `CSS.escape` is a
      browser global that is simply absent in some environments. The room's
      inspector used it once, threw where it was missing, and the focus
      silently never returned — with a test that passed anyway, because
      focus had not moved in the first place.
    */
    if (!returnFocusKey) return;
    requestAnimationFrame(() => {
      const target = [...document.querySelectorAll<HTMLButtonElement>("[data-hit-key]")].find(
        (el) => el.dataset.hitKey === returnFocusKey,
      );
      target?.focus();
    });
  };

  const placement = inspectorPlacement(selected, bounds, fit);

  const isPending = overview.isPending || artifacts.isPending;
  const error = overview.error ?? artifacts.error;
  const empty = overview.isSuccess && rooms.length === 0 && !hasUnfiled;

  return (
    <div
      className="flex min-h-0 flex-1 flex-col gap-4 px-4 py-6 sm:px-6"
      onKeyDown={(e) => {
        /*
          Escape is caught for the whole surface, not just for the panel.
          Activating an object leaves focus ON THE OBJECT, so a handler
          bound to the panel would only work for somebody who had already
          tabbed into it — which is nobody, immediately after opening it.
        */
        if (e.key === "Escape" && selected) {
          e.stopPropagation();
          closeInspector();
        }
      }}
    >
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{t.app.palace.scene.mapTitle}</h1>
          <p className="mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
            {t.app.palace.scene.mapDescription}
          </p>
        </div>
        <Button variant="outline" size="sm" asChild>
          <Link to="/app/modules/palace/library">{t.app.library.title}</Link>
        </Button>
      </header>

      {/*
        ══════════════════════════════════════════════════════════════════
          THE BOX THAT IS MEASURED HOLDS NOTHING. SAME RULE AS THE ROOM
        ══════════════════════════════════════════════════════════════════

        All three children are out of flow, so this area has no content
        height to contribute and what remains is the flex algorithm plus
        `min-h`, which depend on the window and nothing else. Moving the
        `ref` onto a box that contains the presenter reintroduces the N3
        feedback loop: the verdict would change the content, the content
        would change the height, and the next measurement would come from a
        box the previous verdict resized.
      */}
      <div
        data-testid="building-container"
        /*
          ══════════════════════════════════════════════════════════════
            THE STAGE IS SHAPED LIKE WHAT STANDS ON IT
          ══════════════════════════════════════════════════════════════

          Measured in Chrome at 1440x950: the container was 1088x724 and
          the house's own box is about 2:1, so the fit was WIDTH-bound and
          272 of those 724 pixels were letterbox. The house filled 91% of
          the width and 62% of the height, which is the "lots of empty
          space" the human gate reported.

          So the stage takes an aspect close to the house's. This is a
          window-derived rule and not a content-derived one: the height
          follows the element's own WIDTH, never the number of rooms and
          never what the presenter is showing. The N3 loop needs the
          verdict to be able to change the measured box, and nothing here
          gives it that power.

          The two clamps are what make it safe at the extremes, and both
          fall back to exactly the behaviour that shipped before:

            max-h-full   a very wide window would ask for a stage taller
                         than the page; height clamps, the aspect gives
                         way, and the house letterboxes as it used to
            max-h-full   a very wide window would ask for a stage taller
                         than the page; height clamps, the aspect gives
                         way, and the house letterboxes as it used to
            below `sm:`  there is no house at that width, only the
                         fallback list, and a list wants the page's full
                         height rather than a 2:1 box with dead space
                         under it. So the shape is a DESKTOP rule and the
                         narrow case keeps the `flex-1` it always had
        */
        className="relative min-h-[28rem] w-full flex-1 shrink overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) sm:aspect-[1.78/1] sm:max-h-full sm:flex-none"
      >
        <div
          ref={ref}
          data-testid="building-measure"
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
        />

        <div data-testid="building-presenter" className="absolute inset-0 overflow-y-auto">
          {isPending ? (
            <div className="p-4">
              <div
                aria-busy="true"
                aria-label={t.app.library.states.loading}
                className="h-40 animate-pulse rounded-2xl bg-(--color-muted)"
              />
            </div>
          ) : error ? (
            <div className="p-4">
              <div className="rounded-2xl border border-(--color-border) p-8 text-center">
                <p className="text-sm font-medium">{t.app.library.states.errorTitle}</p>
                <p className="mt-1.5 text-sm text-(--color-muted-foreground)">{error.message}</p>
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-3"
                  onClick={() => {
                    void overview.refetch();
                    void artifacts.refetch();
                  }}
                >
                  {t.app.library.states.retry}
                </Button>
              </div>
            </div>
          ) : empty ? (
            <div className="flex h-full flex-col items-center justify-center p-10 text-center">
              <p className="text-sm font-medium">{t.app.palace.scene.mapEmptyTitle}</p>
              <p className="mx-auto mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
                {t.app.palace.scene.mapEmptyBody}
              </p>
            </div>
          ) : fit && fit.mode === "library" ? (
            <div className="p-4">
              <div
                role="status"
                className="mb-4 rounded-xl border border-(--color-border) bg-(--color-muted)/60 p-4"
              >
                <p className="text-sm font-medium">{t.app.palace.scene.tooSmallBuildingTitle}</p>
                <p className="mt-1 text-sm text-(--color-muted-foreground)">
                  {t.app.palace.scene.tooSmallBuildingBody}
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
                  {t.app.palace.scene.forceBuilding}
                </Button>
              </div>
              <RoomList rooms={orderedRooms} hasUnfiled={hasUnfiled} />
            </div>
          ) : (
            <BuildingFrame
              layout={layout}
              items={items}
              bounds={bounds}
              fit={fit}
              nameOf={nameOf}
              labelOf={labelOf}
              onActivate={activate}
              selectedKey={selected?.key}
              buildingLabel={t.app.palace.scene.buildingLabel}
              buildingDescription={t.app.palace.scene.buildingDescription}
            />
          )}
        </div>

        {/*
          The inspector, OVER the house rather than instead of it.

          ── Why it is a layer and not a sibling in flow ────────────────
          Because the room's own view learned this the hard way in N3: an
          in-flow panel takes height from the area the verdict is measured
          against, the scene measures smaller, D5 hands over to the list,
          and the object somebody just activated vanishes taking the focus
          with it. Absolute, so the geometry the fit reads is untouched and
          the house stays exactly where it was, behind the panel.

          `pointer-events-none` on the wrapper so the house around the
          panel stays pressable: the reader can open a second object
          without closing the first.
        */}
        {selected ? (
          <div
            data-testid="house-inspector-anchor"
            style={placement.anchor}
            className="pointer-events-none absolute z-10 flex flex-col justify-end"
          >
            <HouseInspector
              item={selected}
              roomName={selected.roomId ? nameOf(selected.roomId) : ""}
              onClose={closeInspector}
              maxHeight={placement.panelMaxHeight}
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}

/**
 * The rooms as a list of cards: the shape this page had before there was a
 * house.
 *
 * Preserved rather than reinvented, because it already works, is already
 * tested and is already accessible — and because the promise that nothing
 * is reachable ONLY through the house has to be kept by something real.
 * Every room here has the same address it has in the house.
 *
 * ── The vignette still draws no artifacts ──────────────────────────────
 * A miniature with three desks in a room that holds fourteen invites
 * counting, and counting a picture is how somebody ends up wrong about
 * their own record. At this size there is no room to draw real furniture,
 * so it draws none: the numbers are text. The house, which has room, draws
 * the real thing.
 */
function RoomList({
  rooms,
  hasUnfiled,
}: {
  rooms: readonly OverviewRoom[];
  hasUnfiled: boolean;
}) {
  const t = useT();

  return (
    <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      {rooms.map((room) => (
        <li key={room.room_id}>
          <RoomCard room={room} />
        </li>
      ))}

      {/* Only when it holds something, and never with the silhouette of a
          room: it is a staging tray, not a place. */}
      {hasUnfiled ? (
        <li>
          <Link
            to="/app/modules/palace/library?tab=artifacts&room=none"
            aria-label={t.app.palace.scene.openUnfiled}
            className="group block overflow-hidden rounded-2xl border border-dashed border-(--color-border-strong) bg-(--color-muted)/40 outline-none transition-[box-shadow,transform] duration-200 [transition-timing-function:var(--ease-premium)] hover:-translate-y-px focus-visible:ring-2 focus-visible:ring-(--color-ring)"
            data-testid="unfiled-tray"
          >
            <UnfiledTray />
            <div className="p-4">
              <h2 className="text-sm font-semibold">{t.app.palace.scene.unfiledTitle}</h2>
              <p className="mt-1 text-xs text-(--color-muted-foreground)">
                {t.app.palace.scene.unfiledBody}
              </p>
            </div>
          </Link>
        </li>
      ) : null}
    </ul>
  );
}

function RoomCard({ room }: { room: OverviewRoom }) {
  const t = useT();
  const fmt = useFormat();

  return (
    <Link
      to={`/app/modules/palace/rooms/${room.room_id}`}
      aria-label={t.app.palace.scene.openRoom.replace("{name}", room.name)}
      className="group block overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft) outline-none transition-[box-shadow,transform] duration-200 [transition-timing-function:var(--ease-premium)] hover:-translate-y-px hover:shadow-(--shadow-lift) focus-visible:ring-2 focus-visible:ring-(--color-ring)"
    >
      <RoomVignette decoration={decorationFor(room.room_id)} />
      <div className="p-4">
        <div className="flex items-start justify-between gap-2">
          <h2 className="text-sm font-semibold">{room.name}</h2>
          <SensitivityBadge sensitivity={room.sensitivity} />
        </div>
        {/* Active artifacts and memories. Not `archived_count`: the
            entrance is the active space, and archived content has its own
            address in the Library. */}
        <p className="mt-1 text-xs text-(--color-muted-foreground)">
          {t.app.palace.scene.counts
            .replace("{artifacts}", fmt.number(room.artifact_count))
            .replace("{memories}", fmt.number(room.memory_count))}
        </p>
      </div>
    </Link>
  );
}
