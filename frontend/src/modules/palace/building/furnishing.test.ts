import { describe, expect, it } from "vitest";

import { buildingBounds } from "./bounds";
import { cameraForRoom } from "./camera";
import { fitBuilding } from "./fit";
import {
  DECOR_PIECES,
  DECOR_SLOT_ORDER,
  MAX_DECOR_PIECES,
  decorateRoom,
  RUG_STYLES,
} from "./furnishing";
import { houseItems } from "./houseModel";
import {
  furnishRoom,
  MEMORY_SLOT,
  PILE_SLOT,
  VISIBLE_OBJECTS,
  type InteriorArtifact,
  type InteriorOutput,
} from "./interior";
import { BUILDING_VERSION, placeBuilding } from "./placement";

/**
 * The furnishing system, on its own.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THESE ASK WHETHER ATMOSPHERE CAN DISTURB MEANING. IT MUST NOT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Not one of them asks whether a plant is well drawn — that is the human
 * gate's job and no test can do it. They ask the questions a picture cannot
 * answer for itself: whether the same room is furnished the same way
 * tomorrow, whether an edit redecorates, whether a chair can take a
 * cabinet's place, whether the camera or the bounds or the hit targets
 * notice any of this happening.
 */

const ROOM = "aaaaaaaa-1111-1111-1111-111111111111";
const OTHER = "bbbbbbbb-2222-2222-2222-222222222222";

function artifacts(n: number, from = 1): InteriorArtifact[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `art-${from + i}`,
    kind: (["list", "note", "plan", "project"] as const)[i % 4],
    createdAt: `2026-09-${String(from + i).padStart(2, "0")}T00:00:00Z`,
  }));
}

function room(
  count: number,
  { total, memories = false }: { total?: number; memories?: boolean } = {},
): InteriorOutput {
  return furnishRoom({
    roomId: ROOM,
    artifacts: artifacts(count),
    artifactTotal: total ?? count,
    hasMemories: memories,
  });
}

/* ══════════════════════════════════════════════════════════════════════
   A · B · C. Deterministic, blind to meaning, varied by identity
   ══════════════════════════════════════════════════════════════════════ */

describe("the seed", () => {
  it("furnishes the same room identically, every time", () => {
    expect(decorateRoom(ROOM, room(1))).toEqual(decorateRoom(ROOM, room(1)));
  });

  it("cannot be told a title, a description or an edit time", () => {
    // I-A for the furnishing layer, and it is the signature that enforces
    // it: `decorateRoom` takes an id and an interior, and the interior
    // carries `{artifactId, kind, slot, local}` and nothing a person wrote.
    const interior = room(2);
    for (const object of interior.objects) {
      expect(Object.keys(object).sort()).toEqual(["artifactId", "kind", "local", "slot"]);
    }
    // And the same room furnished from artifacts with different ids but the
    // same shape is furnished the same way: nothing about the CONTENT
    // reaches the decoration.
    const renamed = furnishRoom({
      roomId: ROOM,
      artifacts: artifacts(2).map((a) => ({ ...a, kind: "project" as const })),
      artifactTotal: 2,
      hasMemories: false,
    });
    expect(decorateRoom(ROOM, renamed)).toEqual(decorateRoom(ROOM, interior));
  });

  it("gives different rooms deterministically different furniture", () => {
    const here = decorateRoom(ROOM, room(1));
    const there = decorateRoom(OTHER, room(1));
    expect(here).not.toEqual(there);
    // Stable, not random: twice more, same answers.
    expect(decorateRoom(OTHER, room(1))).toEqual(there);
  });

  it("stays inside its own closed vocabulary", () => {
    for (const id of Array.from({ length: 200 }, (_, i) => `room-${i}`)) {
      const decor = decorateRoom(id, room(0));
      expect(RUG_STYLES).toContain(decor.rug);
      for (const placement of decor.pieces) {
        expect(DECOR_PIECES).toContain(placement.piece);
      }
    }
  });

  it("uses no clock and no randomness", () => {
    // A property, not a promise: the same call across a stubbed clock.
    const before = decorateRoom(ROOM, room(3));
    const real = Date.now;
    try {
      Date.now = () => 0;
      expect(decorateRoom(ROOM, room(3))).toEqual(before);
    } finally {
      Date.now = real;
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   I. Meaning has priority over atmosphere
   ══════════════════════════════════════════════════════════════════════ */

describe("priority", () => {
  it("never puts furniture where an object, a pile or a surface stands", () => {
    for (let count = 0; count <= VISIBLE_OBJECTS; count += 1) {
      for (const memories of [false, true]) {
        const interior = room(count, { total: count + 2, memories });
        const taken = new Set([
          ...interior.objects.map((o) => o.slot),
          ...(interior.pile ? [interior.pile.slot] : []),
          ...(interior.memory ? [interior.memory.slot] : []),
        ]);
        for (const placement of decorateRoom(ROOM, interior).pieces) {
          expect(taken.has(placement.slot)).toBe(false);
        }
      }
    }
  });

  it("yields, piece by piece, as the room fills up", () => {
    const counts = [0, 1, 3, 5, 7].map(
      (n) => decorateRoom(ROOM, room(n, { total: n })).pieces.length,
    );
    // Monotonically non-increasing: a fuller room is never more decorated.
    for (let i = 1; i < counts.length; i += 1) expect(counts[i]).toBeLessThanOrEqual(counts[i - 1]);
    expect(counts[0]).toBe(MAX_DECOR_PIECES);
  });

  it("leaves a full room bare rather than displacing anything", () => {
    // Seven artifacts, a pile and a memory surface: every position spoken
    // for. Decoration gets none, which is the correct answer.
    const full = room(VISIBLE_OBJECTS, { total: VISIBLE_OBJECTS + 4, memories: true });
    expect(full.pile?.slot).toBe(PILE_SLOT);
    expect(full.memory?.slot).toBe(MEMORY_SLOT);
    expect(decorateRoom(ROOM, full).pieces).toHaveLength(0);
  });

  it("holds the ceiling even in an empty room", () => {
    // Nine free positions and still three pieces: the room has to stay
    // walkable and legible, not become a showroom.
    expect(decorateRoom(ROOM, room(0)).pieces.length).toBe(MAX_DECOR_PIECES);
  });

  it("claims positions in the fixed order, corners before the centre", () => {
    const pieces = decorateRoom(ROOM, room(0)).pieces.map((p) => p.slot);
    expect(pieces).toEqual([...DECOR_SLOT_ORDER].slice(0, MAX_DECOR_PIECES));
    expect(pieces).not.toContain(4);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   G · H. Adding an Artifact moves no architecture and no furniture
   ══════════════════════════════════════════════════════════════════════ */

describe("growth", () => {
  it("does not move a semantic object when the room gains one", () => {
    const before = room(2);
    const after = furnishRoom({
      roomId: ROOM,
      artifacts: artifacts(3),
      artifactTotal: 3,
      hasMemories: false,
    });
    for (const object of before.objects) {
      const still = after.objects.find((o) => o.artifactId === object.artifactId)!;
      expect(still.slot).toBe(object.slot);
      expect(still.local).toEqual(object.local);
    }
  });

  it("can only REMOVE decoration, never change what stays", () => {
    // ══════════════════════════════════════════════════════════════════
    //   THE REASON THE PIECE IS A FUNCTION OF (ROOM, SLOT) ALONE
    // ══════════════════════════════════════════════════════════════════
    //
    // Dealing pieces out without repetition would make each one depend on
    // which OTHER positions were free, so adding one artifact would
    // silently swap the furniture standing somewhere else.
    for (let n = 0; n < VISIBLE_OBJECTS; n += 1) {
      const before = decorateRoom(ROOM, room(n, { total: n }));
      const after = decorateRoom(ROOM, room(n + 1, { total: n + 1 }));
      const kept = new Map(after.pieces.map((p) => [p.slot, p.piece]));
      for (const placement of before.pieces) {
        if (!kept.has(placement.slot)) continue;
        expect(kept.get(placement.slot)).toBe(placement.piece);
      }
      // And the rug is untouched by contents entirely.
      expect(after.rug).toBe(before.rug);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   E · F. The world and the camera do not notice
   ══════════════════════════════════════════════════════════════════════ */

describe("the world", () => {
  const layout = placeBuilding({
    rooms: [
      { id: ROOM, createdAt: "2026-09-01T00:00:00Z" },
      { id: OTHER, createdAt: "2026-09-02T00:00:00Z" },
    ],
    buildingVersion: BUILDING_VERSION,
    hasUnfiled: false,
  });
  const interiors = new Map<string, InteriorOutput>([
    [ROOM, room(2)],
    [OTHER, room(1)],
  ]);

  it("keeps bounds, items and the camera computable without any decoration", () => {
    // The proof is structural and it is the strongest one available: none
    // of these three functions can be handed a `RoomDecor`. They are the
    // same calls, with the same arguments, that ran before C3 existed.
    const bounds = buildingBounds(layout, interiors);
    const fit = fitBuilding(bounds, { width: 1088, height: 611 }, "spatial", false);

    expect(buildingBounds(layout, interiors)).toEqual(bounds);
    expect(houseItems(layout, interiors)).toEqual(houseItems(layout, interiors));
    expect(cameraForRoom(layout.rooms[0], bounds, fit)).toEqual(
      cameraForRoom(layout.rooms[0], bounds, fit),
    );

    // And nothing decorative is an item: every key belongs to a real thing.
    for (const item of houseItems(layout, interiors)) {
      expect(item.key.startsWith("decor:")).toBe(false);
      expect(["artifact", "pile", "memory", "unfiled"]).toContain(item.type);
    }
  });

  it("furnishes a room without producing anything the hit layer could use", () => {
    const decor = decorateRoom(ROOM, interiors.get(ROOM)!);
    for (const placement of decor.pieces) {
      expect(Object.keys(placement).sort()).toEqual(["local", "piece", "slot"]);
      // No id, no title, no artifact, nothing addressable.
      expect(JSON.stringify(placement)).not.toContain("art-");
    }
  });
});
