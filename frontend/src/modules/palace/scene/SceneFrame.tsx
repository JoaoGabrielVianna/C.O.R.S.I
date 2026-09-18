/**
 * The room, drawn and operable.
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
 * ── Why the buttons are HTML and not SVG ───────────────────────────────
 * Because a `<button>` is focusable, announced, activated by Enter AND
 * Space, and understood by every assistive technology without a single
 * aria attribute. An SVG `<g tabindex="0">` needs `role`, needs a key
 * handler, and is inconsistently focusable across engines. The cost is
 * keeping the two layers aligned, and that is one function: `fit`.
 *
 * ── Why the focus order is not the DOM order ───────────────────────────
 * The buttons are emitted in PAINT order, because pointer hit-testing
 * resolves overlaps by DOM order and a near object must win. Focus is
 * managed separately by a roving tabindex over the SEMANTIC order. Paint
 * order and focus order are different questions and this is where they
 * stop being confused for each other.
 */

import { useMemo, useRef, useState } from "react";

import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

import type { LayoutOutput } from "../layout/engine";
import { ArtifactShape, Notebook, PileShape } from "./Affordances";
import { Decor, RoomShell } from "./RoomShell";
import type { Decoration } from "./decoration";
import { toContainerPoint, type Fit } from "./fit";
import { BASE_DEPTH } from "./presentation";
import { viewBoxOf, type SceneBounds } from "./sceneBounds";
import { groupsOf, paintOrder, sceneItems, type SceneItem } from "./sceneModel";

export interface SceneFrameProps {
  layout: LayoutOutput;
  decoration: Decoration;
  bounds: SceneBounds;
  fit: Fit | null;
  /** How an item is named to a screen reader. Never mentions position. */
  labelOf: (item: SceneItem) => string;
  onActivate: (item: SceneItem) => void;
  selectedKey?: string;
  roomLabel: string;
  roomDescription: string;
}

export function SceneFrame({
  layout,
  decoration,
  bounds,
  fit,
  labelOf,
  onActivate,
  selectedKey,
  roomLabel,
  roomDescription,
}: SceneFrameProps) {
  const items = useMemo(() => sceneItems(layout), [layout]);
  const painted = useMemo(() => paintOrder(items), [items]);

  return (
    <div className="relative h-full w-full">
      <svg
        role="img"
        aria-label={roomLabel}
        className="h-full w-full"
        viewBox={viewBoxOf(bounds)}
        preserveAspectRatio="xMidYMid meet"
        data-testid="room-scene"
      >
        <title>{roomLabel}</title>
        <desc>{roomDescription}</desc>
        <g className="text-(--color-foreground)">
          <RoomShell decoration={decoration} />
          <Decor decoration={decoration} />
          {painted.map((item) => (
            <g
              key={item.key}
              aria-hidden="true"
              data-paint-key={item.key}
              data-plane={item.plane}
              data-z={item.z}
              className={cn(
                "transition-[opacity,transform] duration-200 [transition-timing-function:var(--ease-premium)]",
                selectedKey === item.key
                  ? "text-(--color-accent)"
                  : "text-(--color-foreground)",
              )}
            >
              {item.type === "memory" ? (
                <Notebook at={item.at} />
              ) : item.type === "pile" ? (
                <PileShape kind={item.kind!} at={item.at} />
              ) : (
                <ArtifactShape kind={item.kind!} at={item.at} />
              )}
              {selectedKey === item.key ? (
                <rect
                  x={item.at.x - item.footprint.width / 2 - 4}
                  y={item.at.y - item.footprint.height - 4}
                  width={item.footprint.width + 8}
                  height={item.footprint.height + BASE_DEPTH + 8}
                  rx="8"
                  fill="none"
                  stroke="var(--color-ring)"
                  strokeWidth="2"
                />
              ) : null}
            </g>
          ))}
        </g>
      </svg>

      {fit ? (
        <HitLayer
          items={items}
          painted={painted}
          bounds={bounds}
          fit={fit}
          labelOf={labelOf}
          onActivate={onActivate}
          selectedKey={selectedKey}
        />
      ) : null}
    </div>
  );
}

/**
 * The interactive overlay.
 *
 * ── Roving tabindex ────────────────────────────────────────────────────
 * One tab stop per group. Arrows move inside it, in semantic order; Home
 * and End jump to its ends. Nothing carries a positive `tabindex`, which
 * would reorder the whole page's tab sequence for everyone.
 */
function HitLayer({
  items,
  painted,
  bounds,
  fit,
  labelOf,
  onActivate,
  selectedKey,
}: {
  items: SceneItem[];
  painted: SceneItem[];
  bounds: SceneBounds;
  fit: Fit;
  labelOf: (item: SceneItem) => string;
  onActivate: (item: SceneItem) => void;
  selectedKey?: string;
}) {
  const t = useT();
  const groups = useMemo(() => groupsOf(items), [items]);

  // The roving cursor: which item of each group holds that group's tab
  // stop. Keyed by item key rather than index so a room that changes
  // underneath does not point the cursor at something else.
  const [active, setActive] = useState<Record<number, string>>({});
  const refs = useRef(new Map<string, HTMLButtonElement>());

  /**
   * Which item holds this group's tab stop.
   *
   * Staleness is resolved by READING rather than by an effect that prunes:
   * a room that changed underneath simply falls back to the group's first
   * item, and a group never ends up with no tab stop at all. An effect
   * would be a second render for a value this one can already work out.
   */
  const activeKeyOf = (groupIndex: number) => {
    const group = groups.get(groupIndex);
    if (!group?.length) return undefined;
    const stored = active[groupIndex];
    return stored && group.some((i) => i.key === stored) ? stored : group[0].key;
  };

  const move = (item: SceneItem, delta: number | "home" | "end") => {
    const group = groups.get(item.group);
    if (!group) return;
    const current = group.findIndex((i) => i.key === item.key);
    const next =
      delta === "home"
        ? 0
        : delta === "end"
          ? group.length - 1
          : Math.min(group.length - 1, Math.max(0, current + delta));
    const target = group[next];
    if (!target) return;
    setActive((prev) => ({ ...prev, [item.group]: target.key }));
    refs.current.get(target.key)?.focus();
  };


  return (
    <div className="absolute inset-0" data-testid="hit-layer">
      {/* Emitted in PAINT order so the pointer resolves overlaps the way
          the drawing does. Focus order is the roving tabindex above. */}
      {painted.map((item) => {
        const topLeft = toContainerPoint(
          { x: item.at.x - item.footprint.width / 2, y: item.at.y - item.footprint.height },
          bounds,
          fit,
        );
        const width = item.footprint.width * fit.scale;
        const height = (item.footprint.height + BASE_DEPTH) * fit.scale;
        const isTabStop = activeKeyOf(item.group) === item.key;

        return (
          <button
            key={item.key}
            type="button"
            ref={(el) => {
              if (el) refs.current.set(item.key, el);
              else refs.current.delete(item.key);
            }}
            tabIndex={isTabStop ? 0 : -1}
            aria-label={labelOf(item)}
            aria-pressed={selectedKey === item.key ? true : undefined}
            data-hit-key={item.key}
            onClick={() => onActivate(item)}
            onFocus={() =>
              /*
                Only when the tab stop actually moves.
                An unconditional update re-rendered the whole layer every
                time focus merely arrived on the element that was already
                the stop, which is the common case for Tab.

                NOTE: this was tried as a fix for the keyboard-activation
                defect recorded in the S6 readback and did NOT fix it. It
                is kept because it is right on its own terms, not because
                it solved that.
              */
              setActive((prev) =>
                prev[item.group] === item.key ? prev : { ...prev, [item.group]: item.key },
              )
            }
            onKeyDown={(e) => {
              switch (e.key) {
                case "ArrowRight":
                case "ArrowDown":
                  e.preventDefault();
                  move(item, 1);
                  break;
                case "ArrowLeft":
                case "ArrowUp":
                  e.preventDefault();
                  move(item, -1);
                  break;
                case "Home":
                  e.preventDefault();
                  move(item, "home");
                  break;
                case "End":
                  e.preventDefault();
                  move(item, "end");
                  break;
                default:
                  break;
              }
            }}
            style={{
              position: "absolute",
              left: `${topLeft.left}px`,
              top: `${topLeft.top}px`,
              width: `${width}px`,
              height: `${height}px`,
            }}
            className={cn(
              "rounded-lg outline-none",
              "focus-visible:ring-2 focus-visible:ring-(--color-ring) focus-visible:ring-offset-2",
              "focus-visible:ring-offset-(--color-background)",
              "hover:bg-(--color-accent)/8 transition-colors duration-150",
              "[transition-timing-function:var(--ease-premium)]",
            )}
          >
            <span className="sr-only">{t.app.palace.scene.open}</span>
          </button>
        );
      })}
    </div>
  );
}
