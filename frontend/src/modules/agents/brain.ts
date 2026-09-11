/**
 * Brain — the graph an agent's memory already is, made visible.
 *
 * ── What this is, and what it deliberately is not ──────────────────────
 * This module is a pure projection: memories in, positioned nodes and edges
 * out. Nothing here is persisted, nothing is inferred, and no model is
 * asked anything. The backend stores Memory; it does not store a graph, and
 * the picture must stay cheap enough to throw away and redraw differently
 * tomorrow without a migration.
 *
 * The single rule everything below follows:
 *
 *   **An edge is drawn only where the data already asserts a relation.**
 *
 * There is exactly one such relation in the approved v1 model, and it is
 * provenance: two memories captured in the same conversation are related,
 * because they were learnt in the same place. That is why the graph is
 * agent → origin → memory and not something denser.
 *
 * ── Why there are no topic or tag nodes ────────────────────────────────
 * The example that inspired this view has nodes like "YouTube" and "React"
 * between the memories. Those would be tags, and the v1 memory model has no
 * tags. Categories are deliberately deferred, on the grounds that a
 * taxonomy over eight items is bureaucracy.
 * Deriving topics from the text would mean either keyword matching (wrong
 * in silence) or a model call (forbidden: the Brain must cost nothing).
 *
 * So there are no topic nodes. A relation that cannot be derived
 * deterministically from what exists simply does not appear. When tags do
 * land, they become a fourth node kind here and nothing else changes.
 *
 * ── Why the shape has room for Sources ─────────────────────────────────
 * `BrainNodeKind` is a closed union that Sources will extend. Node kind is
 * carried explicitly rather than guessed from the id, so a Source node can
 * be added without any renderer having to tell one kind of circle from
 * another by parsing a string. Nothing about Sources is anticipated beyond
 * that: no field, no fetch, no placeholder.
 */

import type { ApiMemory } from "@/modules/agents/api/memories";
import { provenanceOf } from "@/modules/agents/api/memories";

/* ── model ───────────────────────────────────────────────────────────── */

/**
 * What a node stands for. `source` is not produced yet — it is listed so
 * the renderer's switch is already total when the Sources batch lands.
 */
export type BrainNodeKind = "agent" | "origin" | "memory";

/** Where a group of memories came from. Mirrors `Provenance`, grouped. */
export type BrainOriginKind = "manual" | "thread" | "lost";

interface BrainNodeBase {
  id: string;
  label: string;
  /** Centre, in graph units. The renderer supplies the viewport transform. */
  x: number;
  y: number;
  r: number;
}

export interface BrainAgentNode extends BrainNodeBase {
  kind: "agent";
  agentId: string;
  /** How many memories hang off this agent, in total. */
  memories: number;
}

export interface BrainOriginNode extends BrainNodeBase {
  kind: "origin";
  origin: BrainOriginKind;
  /** Present only for `thread`: what the node links to. */
  conversationId?: string;
  memories: number;
}

export interface BrainMemoryNode extends BrainNodeBase {
  kind: "memory";
  memory: ApiMemory;
  /** True when the budget is currently dropping this one. */
  dimmed: boolean;
}

export type BrainNode = BrainAgentNode | BrainOriginNode | BrainMemoryNode;

export interface BrainEdge {
  id: string;
  from: BrainNode;
  to: BrainNode;
  /** An edge into a memory the model is not currently receiving. */
  dimmed: boolean;
}

export interface BrainGraph {
  nodes: BrainNode[];
  edges: BrainEdge[];
  /** Bounding box of the laid-out graph, for the initial viewBox. */
  bounds: { x: number; y: number; width: number; height: number };
}

/* ── layout constants ────────────────────────────────────────────────── */

const AGENT_RADIUS = 30;
const ORIGIN_RADIUS = 13;
const MEMORY_RADIUS = 7;
const PINNED_MEMORY_RADIUS = 9;

/** Distance from the agent to its origin nodes. */
const ORIGIN_ORBIT = 190;
/** Distance from the agent to the first ring of memories. */
const MEMORY_ORBIT = 320;
/** Gap between memory rings when one ring cannot hold a group. */
const RING_STEP = 62;
/** Minimum arc length between two memories on the same ring. */
const MEMORY_SPACING = 44;
/** Blank space kept around the drawing. */
const MARGIN = 60;

/* ── derivation ──────────────────────────────────────────────────────── */

interface Group {
  key: string;
  kind: BrainOriginKind;
  label: string;
  conversationId?: string;
  memories: ApiMemory[];
}

/**
 * The grouping key, and the one place a false edge could be introduced.
 *
 * A memory whose conversation was *soft* deleted still carries its id, so
 * two such memories from the same thread genuinely share an origin and are
 * grouped. A memory whose conversation was *hard* deleted has no id left,
 * and nothing can be said about what it shares with anything — so it gets a
 * group of its own, keyed by the memory itself. Collapsing those into one
 * "removed" node would draw an edge asserting a common origin that the data
 * does not support, which is the one thing this module may not do.
 */
function groupKeyOf(m: ApiMemory): { key: string; kind: BrainOriginKind } {
  const p = provenanceOf(m);
  if (p.kind === "manual") return { key: "manual", kind: "manual" };
  if (m.source_conversation_id) {
    return { key: `conv:${m.source_conversation_id}`, kind: p.kind };
  }
  return { key: `lost:${m.id}`, kind: "lost" };
}

function groupsOf(memories: ApiMemory[]): Group[] {
  const byKey = new Map<string, Group>();
  // Insertion order is the server's order — pinned first, then most
  // recently updated — so the layout is stable between renders and between
  // reloads. Nothing here sorts, because any sort would be a second opinion
  // about priority.
  for (const m of memories) {
    const { key, kind } = groupKeyOf(m);
    let g = byKey.get(key);
    if (!g) {
      const p = provenanceOf(m);
      g = {
        key,
        kind,
        label:
          kind === "manual"
            ? "Adicionadas manualmente"
            : p.kind === "thread"
              ? p.title
              : "Conversa removida",
        conversationId: p.kind === "thread" ? p.conversationId : undefined,
        memories: [],
      };
      byKey.set(key, g);
    }
    g.memories.push(m);
  }
  return [...byKey.values()];
}

/**
 * Builds the graph for one agent.
 *
 * Layout is deterministic polar arithmetic, not a simulation: the same
 * memories always produce the same picture, which is what makes a node
 * findable again after a reload. Each origin gets an angular sector sized by
 * how many memories it holds, so a thread with thirty memories is not
 * crammed into the same wedge as one with two.
 */
export function buildBrainGraph(agent: { id: string; name: string }, memories: ApiMemory[]): BrainGraph {
  const agentNode: BrainAgentNode = {
    id: `agent:${agent.id}`,
    kind: "agent",
    label: agent.name,
    agentId: agent.id,
    memories: memories.length,
    x: 0,
    y: 0,
    r: AGENT_RADIUS,
  };

  const nodes: BrainNode[] = [agentNode];
  const edges: BrainEdge[] = [];

  const groups = groupsOf(memories);
  // Weight by memories + 1 so a group of one still gets a visible wedge.
  const totalWeight = groups.reduce((sum, g) => sum + g.memories.length + 1, 0);

  let angle = -Math.PI / 2; // start at the top, which reads as "first"
  for (const group of groups) {
    const sweep = (2 * Math.PI * (group.memories.length + 1)) / totalWeight;
    const mid = angle + sweep / 2;

    const originNode: BrainOriginNode = {
      id: `origin:${group.key}`,
      kind: "origin",
      label: group.label,
      origin: group.kind,
      conversationId: group.conversationId,
      memories: group.memories.length,
      x: Math.cos(mid) * ORIGIN_ORBIT,
      y: Math.sin(mid) * ORIGIN_ORBIT,
      r: ORIGIN_RADIUS,
    };
    nodes.push(originNode);
    edges.push({
      id: `edge:${agentNode.id}->${originNode.id}`,
      from: agentNode,
      to: originNode,
      dimmed: false,
    });

    for (const [i, position] of placeInSector(group.memories.length, angle, sweep).entries()) {
      const m = group.memories[i];
      const node: BrainMemoryNode = {
        id: `memory:${m.id}`,
        kind: "memory",
        label: m.content,
        memory: m,
        // Disabled and budget-dropped look the same here on purpose: both
        // mean "the model is not receiving this". Which of the two it is
        // belongs in the inspector, where there is room to say it.
        dimmed: !m.in_context,
        x: position.x,
        y: position.y,
        r: m.pinned ? PINNED_MEMORY_RADIUS : MEMORY_RADIUS,
      };
      nodes.push(node);
      edges.push({
        id: `edge:${originNode.id}->${node.id}`,
        from: originNode,
        to: node,
        dimmed: node.dimmed,
      });
    }

    angle += sweep;
  }

  return { nodes, edges, bounds: boundsOf(nodes) };
}

/**
 * Positions `count` memories inside one angular sector, in rings.
 *
 * A single ring would put fifty memories of one thread on top of each other,
 * so the ring's capacity is derived from its own arc length: however many
 * fit at `MEMORY_SPACING` apart, the rest go one ring further out. This is
 * what keeps the picture legible at 500 memories without a cap and without
 * dropping anything.
 */
function placeInSector(count: number, start: number, sweep: number): { x: number; y: number }[] {
  const out: { x: number; y: number }[] = [];
  let placed = 0;
  let ring = 0;

  while (placed < count) {
    const radius = MEMORY_ORBIT + ring * RING_STEP;
    const capacity = Math.max(1, Math.floor((sweep * radius) / MEMORY_SPACING));
    const onThisRing = Math.min(capacity, count - placed);

    for (let i = 0; i < onThisRing; i++) {
      // Centre the row inside the sector: `(i + 0.5) / onThisRing` spreads
      // them evenly with half a step of padding at each edge, so adjacent
      // sectors never touch.
      const a = start + (sweep * (i + 0.5)) / onThisRing;
      out.push({ x: Math.cos(a) * radius, y: Math.sin(a) * radius });
    }
    placed += onThisRing;
    ring += 1;
  }
  return out;
}

function boundsOf(nodes: BrainNode[]) {
  let minX = 0;
  let minY = 0;
  let maxX = 0;
  let maxY = 0;
  for (const n of nodes) {
    minX = Math.min(minX, n.x - n.r);
    minY = Math.min(minY, n.y - n.r);
    maxX = Math.max(maxX, n.x + n.r);
    maxY = Math.max(maxY, n.y + n.r);
  }
  return {
    x: minX - MARGIN,
    y: minY - MARGIN,
    width: maxX - minX + MARGIN * 2,
    height: maxY - minY + MARGIN * 2,
  };
}
