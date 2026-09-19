/**
 * The Palace as one house you can look into.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   TWO LAYERS: ONE PAINTS, ONE IS PRESSED. NEITHER DOES THE OTHER'S JOB
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The SVG paints, back to front, and is `aria-hidden` from end to end. The
 * interactive layer is HTML `<button>` elements positioned over it.
 *
 * ── What is a control here, and what is not ────────────────────────────
 * The controls are the OBJECTS: the artifacts standing in the rooms, the
 * pile that stands for the ones that did not fit, the memory surface, and
 * the unfiled tray. Pressing one opens it where it is.
 *
 * The rooms are not controls. Their floors, walls, doorways and decoration
 * are scenery. C1 made the whole room a link and the human gate rejected
 * exactly that: a room-sized button is a navigation card with a picture on
 * it, and a house whose rooms are buttons is a menu. A room's NAME is a
 * small, explicit, separately reachable link, which is a different thing
 * from the room's surface being clickable.
 *
 * ── Why the interactive layer is HTML and not SVG ──────────────────────
 * Because a `<button>` is focusable, announced, activated by Enter AND
 * Space, and understood by every assistive technology without a single
 * aria attribute. The cost is keeping the two layers aligned, and that is
 * one function: `toBuildingPoint`.
 */

import { cn } from "@/lib/utils";

import { project } from "../layout/iso";
import type { Cell } from "../layout/kinds";
import { decorationFor, type Decoration } from "../scene/decoration";
import type { Fit } from "../scene/fit";
import { BuildingNotebook, BuildingObject, BuildingPile, BuildingTray } from "./BuildingObjects";
import { buildingViewBox, roomBox, type BuildingBounds } from "./bounds";
import { toBuildingPoint } from "./fit";
import { focusOrder, paintOrder, type HouseItem } from "./houseModel";
import {
  ROOM_SPAN,
  ROOM_WALL_ELEVATION,
  type BuildingLayout,
  type RoomPlacement,
} from "./placement";

/**
 * How wide a room must be drawn before its name is painted, in CSS px.
 *
 * Presentation only. It decides whether a LABEL is painted; it never
 * decides where anything is and never affects the D5 verdict. Every object
 * stays reachable and announced whatever this number does, because the
 * accessible names live on the buttons, not in the picture.
 */
const MIN_ROOM_LABEL_PX = 110;

/** How wide the doorway gap in a shared wall is, in cells. */
const DOOR_WIDTH = 1.6;

/**
 * How opaque a room's side wall is drawn.
 *
 * ── Why these are not the room scene's numbers ─────────────────────────
 * `--palace-scene-wall` is pure white in the light theme, and the house
 * sits on a white card, so a wall filled with it at 0.35 opacity is
 * invisible: the first browser pass showed floor plates with no rooms
 * around them. The side wall is therefore filled with the OBJECT's shaded
 * side, which has contrast against both themes' backgrounds, and the back
 * wall keeps the lighter fill with a strong outline. Light from the upper
 * left, one stop between the two faces.
 */
const WALL_TONE_OPACITY: Record<Decoration["wall"], number> = {
  light: 0.55,
  mid: 0.72,
  deep: 0.88,
};

function pts(...points: { x: number; y: number }[]): string {
  return points.map((p) => `${p.x},${p.y}`).join(" ");
}

export interface BuildingFrameProps {
  layout: BuildingLayout;
  items: readonly HouseItem[];
  bounds: BuildingBounds;
  fit: Fit | null;
  /** Room name by id. The picture never learns it; the label does. */
  nameOf: (roomId: string) => string;
  /** How an item is named to a screen reader. Never mentions position. */
  labelOf: (item: HouseItem) => string;
  onActivate: (item: HouseItem) => void;
  selectedKey?: string;
  buildingLabel: string;
  buildingDescription: string;
}

export function BuildingFrame({
  layout,
  items,
  bounds,
  fit,
  nameOf,
  labelOf,
  onActivate,
  selectedKey,
  buildingLabel,
  buildingDescription,
}: BuildingFrameProps) {
  const painted = paintOrder(items);
  // Rooms paint back to front too, and every room paints before every
  // object: a shell drawn later would cover the furniture of the room
  // behind it. Two passes rather than one interleaved sort, because the
  // cutaway only reads correctly when the whole structure is down first.
  const shells = [...layout.rooms].sort((a, b) => a.z - b.z);

  return (
    <div className="relative h-full w-full">
      <svg
        role="img"
        aria-label={buildingLabel}
        className="h-full w-full"
        viewBox={buildingViewBox(bounds)}
        preserveAspectRatio="xMidYMid meet"
        data-testid="palace-building"
      >
        <title>{buildingLabel}</title>
        <desc>{buildingDescription}</desc>

        <defs>
          {/* Defined once for the whole house rather than once per room:
              duplicate pattern ids in one document are a collision, and
              the loser renders as nothing. */}
          <pattern
            id="palace-house-floor-plank"
            width="32"
            height="16"
            patternUnits="userSpaceOnUse"
            patternTransform="skewY(-26.57)"
          >
            <rect width="32" height="16" fill="var(--palace-scene-floor)" />
            <line x1="0" y1="16" x2="32" y2="16" stroke="var(--color-border)" strokeWidth="0.75" />
          </pattern>
          <pattern
            id="palace-house-floor-herringbone"
            width="24"
            height="24"
            patternUnits="userSpaceOnUse"
          >
            <rect width="24" height="24" fill="var(--palace-scene-floor)" />
            <path d="M0 24 L12 12 L24 24" fill="none" stroke="var(--color-border)" strokeWidth="0.75" />
          </pattern>
          <pattern id="palace-house-floor-tile" width="24" height="12" patternUnits="userSpaceOnUse">
            <rect width="24" height="12" fill="var(--palace-scene-floor)" />
            <rect width="24" height="12" fill="none" stroke="var(--color-border)" strokeWidth="0.75" />
          </pattern>
        </defs>

        {/*
          Scenery: `aria-hidden`, unfocusable, no pointer events. A
          decoration a reader could reach would be an affordance, and an
          affordance that opens onto nothing is what this design refuses.
        */}
        <g
          aria-hidden="true"
          style={{ pointerEvents: "none" }}
          className="text-(--color-foreground)"
        >
          {shells.map((room) => (
            <RoomShell key={room.roomId} room={room} />
          ))}

          {painted.map((item) => (
            <g key={item.key} data-paint-key={item.key} data-z={item.z}>
              {/* Behind the object, so the wash lights it rather than
                  tinting it. */}
              {selectedKey === item.key ? (
                <rect
                  x={item.box.minX - 6}
                  y={item.box.minY - 6}
                  width={item.box.maxX - item.box.minX + 12}
                  height={item.box.maxY - item.box.minY + 12}
                  rx="10"
                  fill="var(--color-accent)"
                  fillOpacity="0.12"
                />
              ) : null}
              <g
                className={
                  selectedKey === item.key
                    ? "text-(--color-accent)"
                    : "text-(--color-foreground)"
                }
              >
                {item.type === "artifact" ? (
                  <BuildingObject kind={item.kind!} at={item.at} />
                ) : item.type === "pile" ? (
                  <BuildingPile at={item.at} />
                ) : item.type === "memory" ? (
                  <BuildingNotebook at={item.at} />
                ) : (
                  <BuildingTray at={item.at} />
                )}
              </g>
              {/*
                The open object, marked where it stands.

                Two stops rather than one: a soft accent wash so the eye
                finds it across the whole house, and the ring so the edge
                is unambiguous. The panel opens beside this, and these two
                together are what say the panel belongs to it. No leader
                line, no arrow, nothing that would need geometry of its
                own.
              */}
              {selectedKey === item.key ? (
                <rect
                  x={item.box.minX - 3}
                  y={item.box.minY - 3}
                  width={item.box.maxX - item.box.minX + 6}
                  height={item.box.maxY - item.box.minY + 6}
                  rx="7"
                  fill="none"
                  stroke="var(--color-accent)"
                  strokeWidth="2"
                />
              ) : null}
            </g>
          ))}
        </g>
      </svg>

      {fit ? (
        <div className="absolute inset-0" data-testid="building-hit-layer">
          {/*
            The objects, in SEMANTIC order. Safe to be the DOM order
            because the lattice keeps every target disjoint: see
            `houseModel`. Paint order is the SVG's business.
          */}
          {focusOrder(items).map((item) => {
            const topLeft = toBuildingPoint(
              { x: item.box.minX, y: item.box.minY },
              bounds,
              fit,
            );
            return (
              <button
                key={item.key}
                type="button"
                data-hit-key={item.key}
                data-item-type={item.type}
                data-in-room={item.roomId ?? ""}
                aria-label={labelOf(item)}
                aria-pressed={selectedKey === item.key ? true : undefined}
                onClick={() => onActivate(item)}
                style={{
                  position: "absolute",
                  left: `${topLeft.left}px`,
                  top: `${topLeft.top}px`,
                  width: `${(item.box.maxX - item.box.minX) * fit.scale}px`,
                  height: `${(item.box.maxY - item.box.minY) * fit.scale}px`,
                }}
                className={cn(
                  "rounded-lg outline-none transition-colors duration-150",
                  "[transition-timing-function:var(--ease-premium)]",
                  "hover:bg-(--color-accent)/12",
                  "focus-visible:ring-2 focus-visible:ring-(--color-ring) focus-visible:ring-offset-2",
                  "focus-visible:ring-offset-(--color-background)",
                )}
              />
            );
          })}

          {/*
            ══════════════════════════════════════════════════════════
              THE NAME IS A CAPTION, NOT A CONTROL
            ══════════════════════════════════════════════════════════

            It was a link in the first draft of this slice, and the
            browser pass showed why that cannot work: in a room with
            several objects the chip lands on top of a cabinet. Whichever
            of the two ends up on top, the reader sees one thing and
            presses another, and no amount of z-ordering fixes a control
            that is hidden under a label or a label that swallows a
            press.

            So the name is `pointer-events: none` and `aria-hidden`, and
            it is rendered LAST so it stays readable over whatever it
            covers. Nothing is lost:

              sighted readers  see the name on the room
              screen readers   hear it in every object's own name
                               ("Lista: Ferramentas & Insumos, em
                               Ateliê de Marcenaria")
              navigation       "open the room" is a named, secondary
                               action inside the inspector

            An empty room therefore has no way INTO it from the house,
            which is deliberate rather than overlooked: the house is for
            looking at what exists, and a room with nothing in it has
            nothing to look at. It stays reachable by URL and from the
            Library, as every room does.
          */}
          {layout.rooms.map((room) => {
            const box = roomBox(room);
            const width = (box.maxX - box.minX) * fit.scale;
            if (width < MIN_ROOM_LABEL_PX) return null;

            const anchor = toBuildingPoint(
              project({ u: room.origin.u, v: room.origin.v }, ROOM_WALL_ELEVATION),
              bounds,
              fit,
            );
            return (
              <span
                key={room.roomId}
                aria-hidden="true"
                data-room-id={room.roomId}
                data-testid="building-room-label"
                style={{
                  position: "absolute",
                  left: `${anchor.left}px`,
                  top: `${anchor.top}px`,
                  transform: "translate(-10px, -100%)",
                  maxWidth: `${Math.min(180, Math.max(90, width * 0.6))}px`,
                }}
                /*
                  ── Annotation, not a chip ──────────────────────────
                  It had a card background, a border and a shadow, and
                  the human gate read exactly that: pills floating over
                  the architecture, which is dashboard furniture and not
                  part of a building.

                  What is left is the text itself, set the way a name is
                  set on a plan: small, upper case, letter-spaced, in the
                  muted ink. The halo is the only thing standing in for
                  the old background, and it exists for legibility where
                  a name crosses a patterned floor rather than to make a
                  surface for the name to sit on.
                */
                className="pointer-events-none inline-flex items-center truncate text-[11px] font-semibold tracking-[0.09em] text-(--color-muted-foreground) uppercase [text-shadow:0_0_5px_var(--color-card),0_0_5px_var(--color-card),0_0_10px_var(--color-card)]"
              >
                {nameOf(room.roomId)}
              </span>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

/**
 * One room's cutaway shell: a floor, two walls, and the doorways it shares
 * with the rooms next to it.
 *
 * The walls are short. At house scale they are volume, not enclosure:
 * full-height walls would hide the furniture of the room behind them, and
 * the whole point of this surface is that the furniture is visible.
 *
 * The decoration is the room's own, seeded by `room_id` and nothing else,
 * exactly as the room's own scene seeds it. Two rooms differ so they can be
 * told apart; no difference means anything.
 */
function RoomShell({ room }: { room: RoomPlacement }) {
  const decoration = decorationFor(room.roomId);
  const { u, v } = room.origin;
  const S = ROOM_SPAN;
  const E = ROOM_WALL_ELEVATION;
  const at = (du: number, dv: number, e = 0) => project({ u: u + du, v: v + dv } as Cell, e);

  const floor = [at(0, 0), at(S, 0), at(S, S), at(0, S)];
  const tone = WALL_TONE_OPACITY[decoration.wall];

  // The back wall rises from the far edge (v = 0), the side wall from the
  // left edge (u = 0). The two near ones are the cutaway, which is what
  // makes the room something you look INTO.
  //
  // A wall with a doorway is drawn as two posts with a gap between them,
  // and the gap is the doorway: there is no door object, no door list and
  // nothing anywhere that a reader could follow from one room to another.
  const mid = S / 2;
  const half = DOOR_WIDTH / 2;

  return (
    <g data-testid="building-room" data-room-shell={room.roomId}>
      {room.doorToRow ? (
        <>
          <WallPanel a={at(0, 0)} b={at(mid - half, 0)} aTop={at(0, 0, E)} bTop={at(mid - half, 0, E)} tone={tone} face="back" />
          <WallPanel a={at(mid + half, 0)} b={at(S, 0)} aTop={at(mid + half, 0, E)} bTop={at(S, 0, E)} tone={tone} face="back" />
        </>
      ) : (
        <WallPanel a={at(0, 0)} b={at(S, 0)} aTop={at(0, 0, E)} bTop={at(S, 0, E)} tone={tone} face="back" />
      )}

      {room.doorToColumn ? (
        <>
          <WallPanel a={at(0, 0)} b={at(0, mid - half)} aTop={at(0, 0, E)} bTop={at(0, mid - half, E)} tone={tone} face="side" />
          <WallPanel a={at(0, mid + half)} b={at(0, S)} aTop={at(0, mid + half, E)} bTop={at(0, S, E)} tone={tone} face="side" />
        </>
      ) : (
        <WallPanel a={at(0, 0)} b={at(0, S)} aTop={at(0, 0, E)} bTop={at(0, S, E)} tone={tone} face="side" />
      )}

      <polygon
        points={pts(...floor)}
        fill={`url(#palace-house-floor-${decoration.floor})`}
        stroke="var(--color-border-strong)"
        strokeWidth="1"
      />
    </g>
  );
}

function WallPanel({
  a,
  b,
  aTop,
  bTop,
  tone,
  face,
}: {
  a: { x: number; y: number };
  b: { x: number; y: number };
  aTop: { x: number; y: number };
  bTop: { x: number; y: number };
  tone: number;
  face: "back" | "side";
}) {
  return (
    <polygon
      points={pts(a, b, bTop, aTop)}
      fill={face === "back" ? "var(--palace-scene-wall)" : "var(--palace-scene-object-side)"}
      fillOpacity={face === "back" ? 1 : tone}
      stroke="var(--color-border-strong)"
      strokeWidth="1"
      data-testid="building-wall"
    />
  );
}
