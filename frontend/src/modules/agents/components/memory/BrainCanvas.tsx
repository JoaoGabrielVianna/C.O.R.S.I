import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Maximize2, Minus, Plus } from "lucide-react";
import { cn } from "@/lib/utils";
import type { BrainGraph, BrainNode } from "@/modules/agents/brain";
import { useT } from "@/lib/i18n";

/**
 * The Brain, drawn.
 *
 * ── Why hand-rolled SVG and no graph library ───────────────────────────
 * The module had no graph dependency, and the cheapest one that does force
 * layout, zoom and hit-testing costs tens of kilobytes and a rendering model
 * of its own. What is needed here is a few hundred circles at coordinates
 * something else already computed. The layout is deterministic arithmetic in
 * `brain.ts`, so there is no simulation to run and nothing left for a
 * library to do but draw — which SVG does natively.
 *
 * The practical gain is not the bundle size. It is that a picture nobody
 * committed to a library's data model can be replaced wholesale when the
 * Sources batch changes what a node means.
 *
 * ── Why the transform lives on one <g> ─────────────────────────────────
 * Pan and zoom move a single group element. The node and edge elements are
 * memoised, so dragging re-renders one attribute rather than five hundred
 * circles: React sees the identical children array and skips it. That is the
 * difference between a graph that drags smoothly at 500 memories and one
 * that stutters.
 */

const MIN_SCALE = 0.25;
const MAX_SCALE = 3;
const ZOOM_STEP = 1.25;

interface View {
  scale: number;
  /** Translation in screen pixels, applied after the scale. */
  x: number;
  y: number;
}

export function BrainCanvas({
  graph,
  selectedId,
  onSelect,
}: {
  graph: BrainGraph;
  selectedId: string | null;
  onSelect: (node: BrainNode | null) => void;
}) {
  const t = useT();
  const [view, setView] = useState<View>({ scale: 1, x: 0, y: 0 });
  const dragRef = useRef<{ x: number; y: number; moved: boolean } | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);

  const reset = useCallback(() => setView({ scale: 1, x: 0, y: 0 }), []);

  // The view is not reset when the graph changes, and that is deliberate:
  // toggling a memory redraws the graph, and yanking the reader back to the
  // centre every time they act on a node would be hostile. Arriving at a
  // *different agent* is the case that does need a fresh view, and the page
  // gets it by keying this component on the agent — a remount, not an
  // effect. See AgentBrain.
  const zoomBy = useCallback((factor: number) => {
    setView((v) => ({ ...v, scale: clamp(v.scale * factor, MIN_SCALE, MAX_SCALE) }));
  }, []);

  // Wheel zoom is bound imperatively rather than through onWheel because
  // React attaches wheel listeners as passive, and a passive listener cannot
  // preventDefault — so zooming the graph would scroll the page as well.
  useEffect(() => {
    const el = svgRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const px = e.clientX - rect.left;
      const py = e.clientY - rect.top;
      setView((v) => {
        const next = clamp(v.scale * (e.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP), MIN_SCALE, MAX_SCALE);
        const k = next / v.scale;
        // Keep the point under the cursor fixed, so zooming reads as
        // magnifying what you are looking at rather than as the drawing
        // sliding away from you.
        return { scale: next, x: px - (px - v.x) * k, y: py - (py - v.y) * k };
      });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const onPointerDown = (e: React.PointerEvent<SVGSVGElement>) => {
    if (e.button !== 0) return;
    dragRef.current = { x: e.clientX - view.x, y: e.clientY - view.y, moved: false };
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const onPointerMove = (e: React.PointerEvent<SVGSVGElement>) => {
    const d = dragRef.current;
    if (!d) return;
    const x = e.clientX - d.x;
    const y = e.clientY - d.y;
    // A few pixels of slop, so a click with an unsteady hand is still a
    // click and not a pan that swallows the selection.
    if (Math.abs(x - view.x) > 3 || Math.abs(y - view.y) > 3) d.moved = true;
    setView((v) => ({ ...v, x, y }));
  };

  const onPointerUp = (e: React.PointerEvent<SVGSVGElement>) => {
    const dragged = dragRef.current?.moved ?? false;
    dragRef.current = null;
    e.currentTarget.releasePointerCapture(e.pointerId);
    // Clicking the background clears the selection; finishing a pan there
    // does not.
    if (!dragged && e.target === e.currentTarget) onSelect(null);
  };

  const edges = useMemo(
    () => (
      <g>
        {graph.edges.map((edge) => (
          <line
            key={edge.id}
            x1={edge.from.x}
            y1={edge.from.y}
            x2={edge.to.x}
            y2={edge.to.y}
            className="stroke-(--color-border)"
            strokeWidth={1}
            strokeOpacity={edge.dimmed ? 0.35 : 0.9}
          />
        ))}
      </g>
    ),
    [graph.edges],
  );

  const nodes = useMemo(
    () => (
      <g>
        {graph.nodes.map((node) => (
          <NodeShape
            key={node.id}
            node={node}
            selected={node.id === selectedId}
            onSelect={onSelect}
          />
        ))}
      </g>
    ),
    [graph.nodes, selectedId, onSelect],
  );

  return (
    <div className="relative min-h-0 flex-1 overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card)">
      <svg
        ref={svgRef}
        role="img"
        aria-label={t.app.modules.agents.memory.brain.canvasLabel}
        viewBox={`${graph.bounds.x} ${graph.bounds.y} ${graph.bounds.width} ${graph.bounds.height}`}
        preserveAspectRatio="xMidYMid meet"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        className="size-full cursor-grab touch-none select-none active:cursor-grabbing"
      >
        {/* Screen-space pan and zoom around the laid-out drawing. The inner
            group is what the memo above keeps stable. */}
        <g transform={`translate(${view.x} ${view.y}) scale(${view.scale})`}>
          {edges}
          {nodes}
        </g>
      </svg>

      <div className="absolute bottom-3 right-3 flex flex-col gap-1 rounded-xl border border-(--color-border) bg-(--color-card)/90 p-1 shadow-(--shadow-card) backdrop-blur">
        <CanvasButton label={t.app.modules.agents.memory.brain.zoomIn} onClick={() => zoomBy(ZOOM_STEP)} icon={Plus} />
        <CanvasButton label={t.app.modules.agents.memory.brain.zoomOut} onClick={() => zoomBy(1 / ZOOM_STEP)} icon={Minus} />
        <CanvasButton label={t.app.modules.agents.memory.brain.fit} onClick={reset} icon={Maximize2} />
      </div>
    </div>
  );
}

/**
 * One node.
 *
 * Labels are rendered for the agent and the origins and withheld from the
 * memories: five hundred overlapping captions is not a picture, it is a
 * smear. A memory's text is one click away in the inspector, and its
 * `<title>` gives it back on hover for free.
 */
function NodeShape({
  node,
  selected,
  onSelect,
}: {
  node: BrainNode;
  selected: boolean;
  onSelect: (node: BrainNode) => void;
}) {
  const fill =
    node.kind === "agent"
      ? "fill-(--color-accent)"
      : node.kind === "origin"
        ? "fill-(--color-card) stroke-(--color-muted-foreground)"
        : node.dimmed
          ? "fill-(--color-muted) stroke-(--color-border)"
          : "fill-(--color-brand-500) stroke-(--color-brand-600)";

  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={labelOf(node)}
      onClick={(e) => {
        e.stopPropagation();
        onSelect(node);
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(node);
        }
      }}
      className="cursor-pointer outline-none"
    >
      <title>{labelOf(node)}</title>

      {selected ? (
        <circle
          cx={node.x}
          cy={node.y}
          r={node.r + 6}
          className="fill-none stroke-(--color-accent)"
          strokeWidth={2}
        />
      ) : null}

      <circle cx={node.x} cy={node.y} r={node.r} className={fill} strokeWidth={1.5} />

      {/* Pinned memories carry a ring, because `pinned` is what decides who
          survives the budget and that deserves to be visible in the picture
          rather than only in a panel. */}
      {node.kind === "memory" && node.memory.pinned ? (
        <circle
          cx={node.x}
          cy={node.y}
          r={node.r + 3}
          className="fill-none stroke-(--color-brand-500)"
          strokeWidth={1}
          strokeDasharray="2 2"
        />
      ) : null}

      {node.kind !== "memory" ? (
        <text
          x={node.x}
          y={node.y + node.r + 16}
          textAnchor="middle"
          className={cn(
            "pointer-events-none select-none",
            node.kind === "agent"
              ? "fill-(--color-foreground) text-[15px] font-semibold"
              : "fill-(--color-muted-foreground) text-[12px]",
          )}
        >
          {truncate(node.label, node.kind === "agent" ? 28 : 22)}
        </text>
      ) : null}
    </g>
  );
}

function CanvasButton({
  label,
  onClick,
  icon: Icon,
}: {
  label: string;
  onClick: () => void;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className="rounded-lg p-1.5 text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
    >
      <Icon className="size-3.5" />
    </button>
  );
}

function labelOf(node: BrainNode): string {
  switch (node.kind) {
    case "agent":
      return `Agente ${node.label}`;
    case "origin":
      return `${node.label} — ${node.memories} ${node.memories === 1 ? "memória" : "memórias"}`;
    case "memory":
      return truncate(node.label, 160);
  }
}

function truncate(text: string, max: number): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length <= max ? flat : `${flat.slice(0, max - 1)}…`;
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}
