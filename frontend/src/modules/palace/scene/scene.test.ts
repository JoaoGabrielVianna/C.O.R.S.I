import { describe, expect, it } from "vitest";

import { LAYOUT_VERSION, VISIBLE_SLOTS, layout, type LayoutInput } from "../layout/engine";
import { LAYOUT_KINDS, MEMORY_SURFACE_CELL } from "../layout/kinds";
import { project } from "../layout/iso";
import type { ArtifactKind } from "../api/types";

import { decorationFor, FLOOR_PATTERNS, PROP_SETS, WALL_TONES } from "./decoration";
import { MIN_TARGET_PX, RESTORE_TARGET_PX, fitScene, toContainerPoint } from "./fit";
import { AFFORDANCES, MIN_HIT_SCENE, anchorPointOf } from "./presentation";
import { sceneBounds } from "./sceneBounds";
import { focusOrder, groupsOf, paintOrder, sceneItems } from "./sceneModel";

// The three sources, for the structural scans.
import presentationSource from "./presentation.ts?raw";
import decorationSource from "./decoration.ts?raw";
import sceneModelSource from "./sceneModel.ts?raw";
import sceneBoundsSource from "./sceneBounds.ts?raw";

/**
 * The scene, proved without opening a picture.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   EVERYTHING HERE IS ABOUT A BOUNDARY, NOT ABOUT HOW IT LOOKS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Whether a desk is handsome is a matter of taste and nobody should write
 * a test for it. Whether the desk sits where the geometry put it, whether
 * focus order is semantic rather than spatial, and whether a count can
 * leak into the decoration are not matters of taste at all.
 */

function codeOf(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/.*$/gm, "$1");
}

function objects(count: number, prefix = "a") {
  return Array.from({ length: count }, (_, i) => ({
    id: `${prefix}-${String(i).padStart(3, "0")}`,
    createdAt: `2026-09-${String(1 + i).padStart(2, "0")}T00:00:00Z`,
  }));
}

function input(
  counts: Partial<Record<ArtifactKind, number>>,
  hasMemories = false,
): LayoutInput {
  return {
    roomId: "room-1",
    layoutVersion: LAYOUT_VERSION,
    hasMemories,
    containers: LAYOUT_KINDS.map((kind) => ({
      kind,
      objects: objects(counts[kind] ?? 0, kind),
    })),
  };
}

/* ══════════════════════════════════════════════════════════════════════
   The geometry boundary
   ══════════════════════════════════════════════════════════════════════ */

describe("the scene never invents a coordinate", () => {
  it("places every floor item exactly where S5 projected it", () => {
    const out = layout(input({ project: 3, list: 5, plan: 2 }));
    for (const item of sceneItems(out)) {
      if (item.plane !== "floor") continue;
      const container = out.containers.find((c) => c.kind === item.kind);
      const source =
        item.type === "pile"
          ? container?.pile?.cell
          : container?.slots.find((s) => s.objectId === item.objectId)?.cell;
      const cell = item.type === "memory" ? out.memorySurface?.cell : source;
      expect(item.at).toEqual(project(cell!));
    }
  });

  it("uses the memory cell S5.1 reserved, and has no anchor of its own", () => {
    const out = layout(input({ list: 2 }, true));
    const memory = sceneItems(out).find((i) => i.type === "memory");
    expect(memory?.at).toEqual(project(MEMORY_SURFACE_CELL));

    // The tripwire: a second anchor anywhere in the scene would be S6
    // choosing a place, which is the boundary this micro-slice restored.
    const source = codeOf(presentationSource) + codeOf(sceneModelSource);
    expect(source).not.toMatch(/MEMORY_ANCHOR|memoryAnchor|MEMORY_CELL\s*=/);
    expect(codeOf(sceneModelSource)).toContain("memorySurface.cell");
  });

  it("emits no memory item when the geometry emitted no surface", () => {
    expect(sceneItems(layout(input({ list: 2 }, false))).some((i) => i.type === "memory")).toBe(
      false,
    );
  });

  it("carries S5's z through untouched", () => {
    const out = layout(input({ project: 4, note: 3 }));
    for (const item of sceneItems(out)) {
      const container = out.containers.find((c) => c.kind === item.kind);
      const slot = container?.slots.find((s) => s.objectId === item.objectId);
      if (slot) expect(item.z).toBe(slot.z);
    }
  });

  it("does not read title, count, status or viewport anywhere in the scene", () => {
    const source =
      codeOf(presentationSource) + codeOf(sceneModelSource) + codeOf(sceneBoundsSource);
    for (const forbidden of ["title", "item_count", "status", "sensitivity", "viewport", "innerWidth"]) {
      expect(source, `the scene reads ${forbidden}`).not.toContain(forbidden);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Presentation planes
   ══════════════════════════════════════════════════════════════════════ */

describe("presentation planes", () => {
  it("puts notes on the wall and everything else on the floor", () => {
    expect(AFFORDANCES.note.plane).toBe("backWall");
    for (const kind of ["project", "list", "plan"] as const) {
      expect(AFFORDANCES[kind].plane).toBe("floor");
    }
  });

  it("does not change slot identity or ordering when mounting on the wall", () => {
    const out = layout(input({ note: 6 }));
    const container = out.containers.find((c) => c.kind === "note")!;
    const items = sceneItems(out).filter((i) => i.kind === "note");

    // Same objects, same order, same z. Only the point differs.
    expect(items.map((i) => i.objectId)).toEqual(container.slots.map((s) => s.objectId));
    expect(items.map((i) => i.z)).toEqual(container.slots.map((s) => s.z));
  });

  it("is injective: no two notes land on the same spot", () => {
    const out = layout(input({ note: VISIBLE_SLOTS + 3 }));
    const points = sceneItems(out)
      .filter((i) => i.kind === "note")
      .map((i) => `${i.at.x},${i.at.y}`);
    expect(new Set(points).size).toBe(points.length);
  });

  it("is a pure function of the cell and the anchor", () => {
    const cell = { u: 7, v: 8 };
    const anchor = { u: 6, v: 6 };
    expect(anchorPointOf(cell, anchor, "backWall")).toEqual(
      anchorPointOf(cell, anchor, "backWall"),
    );
    expect(anchorPointOf(cell, anchor, "floor")).toEqual(project(cell));
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Painting order and focus order are different questions
   ══════════════════════════════════════════════════════════════════════ */

describe("ordering", () => {
  it("paints walls behind the floor, and by z inside each plane", () => {
    const painted = paintOrder(sceneItems(layout(input({ note: 4, list: 4 }, true))));
    const planes = painted.map((i) => i.plane);
    // Every wall item comes before every floor item.
    expect(planes.lastIndexOf("backWall")).toBeLessThan(planes.indexOf("floor"));

    for (let i = 1; i < painted.length; i += 1) {
      if (painted[i].plane !== painted[i - 1].plane) continue;
      expect(painted[i].z).toBeGreaterThanOrEqual(painted[i - 1].z);
    }
  });

  it("focuses in the vocabulary's order, not the geometric one", () => {
    const out = layout(input({ note: 2, project: 2, list: 2 }, true));
    const focus = focusOrder(sceneItems(out));

    // project, list, note — the domain's declaration order — then memory.
    expect(focus.map((i) => i.kind ?? "memory")).toEqual([
      "project",
      "project",
      "list",
      "list",
      "note",
      "note",
      "memory",
    ]);
  });

  it("is a different order from the painting order", () => {
    // If these ever coincided by construction, the separation would be an
    // accident rather than a design, and the next depth change would break
    // the reading order without anyone noticing.
    const items = sceneItems(layout(input({ note: 3, project: 3 }, true)));
    expect(paintOrder(items).map((i) => i.key)).not.toEqual(
      focusOrder(items).map((i) => i.key),
    );
  });

  it("gives each group one tab stop and the pile the last place in its group", () => {
    const out = layout(input({ list: VISIBLE_SLOTS + 3 }));
    const groups = groupsOf(sceneItems(out));
    expect(groups.size).toBe(1);
    const only = [...groups.values()][0];
    expect(only[only.length - 1].type).toBe("pile");
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Scene bounds
   ══════════════════════════════════════════════════════════════════════ */

describe("scene bounds", () => {
  it("is deterministic and takes no viewport", () => {
    const out = layout(input({ project: 3, list: 4 }, true));
    expect(sceneBounds(out)).toEqual(sceneBounds(out));
    // One argument: the layout. There is nowhere to pass a size, which is
    // what keeps the viewBox a property of the room rather than of the
    // screen. `SceneBounds` has a width and a height because it IS a box;
    // the guarantee is about what goes in.
    expect(sceneBounds.length).toBe(1);
  });

  it("covers the whole room even when it is empty", () => {
    const empty = sceneBounds(layout(input({})));
    expect(empty.width).toBeGreaterThan(0);
    expect(empty.height).toBeGreaterThan(0);
  });

  it("grows to contain every slot, pile and the memory surface", () => {
    const small = sceneBounds(layout(input({ list: 1 })));
    const large = sceneBounds(layout(input({ list: VISIBLE_SLOTS + 5, note: 8 }, true)));
    expect(large.width).toBeGreaterThanOrEqual(small.width);
    expect(large.height).toBeGreaterThanOrEqual(small.height);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   D5
   ══════════════════════════════════════════════════════════════════════ */

describe("D5 · the touch-size rule", () => {
  const bounds = sceneBounds(layout(input({ project: 3, list: 3 }, true)));

  const widthFor = (targetPx: number) =>
    (targetPx / MIN_HIT_SCENE) * bounds.width;

  it("hands over to the Library when the smallest target falls below 44px", () => {
    const container = { width: widthFor(40), height: 100000 };
    expect(fitScene(bounds, container, "spatial", false).mode).toBe("library");
  });

  it("stays spatial at exactly 44px", () => {
    const container = { width: widthFor(MIN_TARGET_PX), height: 100000 };
    expect(fitScene(bounds, container, "spatial", false).mode).toBe("spatial");
  });

  it("does not return to the room until 52px, so the boundary has width", () => {
    const between = { width: widthFor(48), height: 100000 };
    // Coming from the Library, 48 is not enough to go back.
    expect(fitScene(bounds, between, "library", false).mode).toBe("library");
    // Coming from the room, 48 is enough to stay.
    expect(fitScene(bounds, between, "spatial", false).mode).toBe("spatial");
    // And 52 restores it from either side.
    const restore = { width: widthFor(RESTORE_TARGET_PX), height: 100000 };
    expect(fitScene(bounds, restore, "library", false).mode).toBe("spatial");
  });

  it("obeys the reader when they force the spatial view", () => {
    const tiny = { width: widthFor(8), height: 100000 };
    expect(fitScene(bounds, tiny, "library", true).mode).toBe("spatial");
  });

  it("is deterministic in its four inputs", () => {
    const c = { width: 900, height: 600 };
    expect(fitScene(bounds, c, "spatial", false)).toEqual(fitScene(bounds, c, "spatial", false));
  });

  it("keeps the overlay aligned with the drawing", () => {
    const fit = fitScene(bounds, { width: 900, height: 600 }, "spatial", false);
    // The scene's top-left corner maps to the letterbox offset exactly.
    expect(toContainerPoint({ x: bounds.minX, y: bounds.minY }, bounds, fit)).toEqual({
      left: fit.offsetX,
      top: fit.offsetY,
    });
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Decoration
   ══════════════════════════════════════════════════════════════════════ */

describe("decoration", () => {
  it("depends on the room id and on nothing else", () => {
    // The signature is the guarantee, and this is the proof: the same id
    // with wildly different content is the same decoration.
    expect(decorationFor("11111111-1111-1111-1111-111111111111")).toEqual(
      decorationFor("11111111-1111-1111-1111-111111111111"),
    );
    expect(decorationFor("a")).not.toEqual(decorationFor("a-different-id-entirely"));
  });

  it("names no content, count, status or timestamp in its source", () => {
    const source = codeOf(decorationSource);
    for (const forbidden of [
      "name",
      "description",
      "count",
      "sensitivity",
      "status",
      "updatedAt",
      "updated_at",
      "artifact",
      "memory",
    ]) {
      expect(source, `decoration reads ${forbidden}`).not.toContain(forbidden);
    }
  });

  it("picks only from closed, equally weighted sets", () => {
    const seen = { floor: new Set(), wall: new Set(), props: new Set() };
    for (let i = 0; i < 400; i += 1) {
      const d = decorationFor(`room-${i}`);
      seen.floor.add(d.floor);
      seen.wall.add(d.wall);
      seen.props.add(d.props);
      expect(FLOOR_PATTERNS).toContain(d.floor);
      expect(WALL_TONES).toContain(d.wall);
      expect(PROP_SETS).toContain(d.props);
    }
    // Every option is reachable, so no combination is a rarity a reader
    // could read as meaning something.
    expect(seen.floor.size).toBe(FLOOR_PATTERNS.length);
    expect(seen.wall.size).toBe(WALL_TONES.length);
    expect(seen.props.size).toBe(PROP_SETS.length);
  });

  it("has constant density: it takes no argument that could vary with it", () => {
    expect(decorationFor.length).toBe(1);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Nothing counts what was withheld
   ══════════════════════════════════════════════════════════════════════ */

describe("privacy", () => {
  it("emits no count except the pile's, which is of objects it was given", () => {
    const out = layout(input({ list: VISIBLE_SLOTS + 4 }, true));
    const encoded = JSON.stringify(sceneItems(out));
    for (const forbidden of ["hidden", "omitted", "withheld", "total"]) {
      expect(encoded).not.toContain(forbidden);
    }
    const pile = sceneItems(out).find((i) => i.type === "pile");
    expect(pile?.count).toBe(4);
  });

  it("has no sensitivity opt-in anywhere in the scene", () => {
    const source =
      codeOf(presentationSource) +
      codeOf(sceneModelSource) +
      codeOf(sceneBoundsSource) +
      codeOf(decorationSource);
    expect(source.toLowerCase()).not.toContain("highly_sensitive");
    expect(source.toLowerCase()).not.toContain("include_highly");
  });
});

/* ══════════════════════════════════════════════════════════════════════
   The touch floor comes from the footprints
   ══════════════════════════════════════════════════════════════════════ */

describe("footprints", () => {
  it("derives the minimum target from the smallest affordance", () => {
    const all = Object.values(AFFORDANCES).flatMap((a) => [a.width, a.height]);
    expect(MIN_HIT_SCENE).toBeLessThanOrEqual(Math.min(...all));
  });

  it("covers every kind", () => {
    expect(Object.keys(AFFORDANCES).sort()).toEqual([...LAYOUT_KINDS].sort());
  });
});
