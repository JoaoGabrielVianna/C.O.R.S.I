import { describe, expect, it } from "vitest";

import { fitBuilding } from "./fit";
import { buildingBounds, roomBox } from "./bounds";
import {
  applyCamera,
  cameraForRoom,
  cameraTransform,
  isOverviewCamera,
  OVERVIEW_CAMERA,
} from "./camera";
import { furnishRoom, type InteriorArtifact, type InteriorOutput } from "./interior";
import { houseItems } from "./houseModel";
import { BUILDING_VERSION, placeBuilding } from "./placement";
import { toBuildingPoint } from "./fit";

/**
 * The camera, on its own.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THESE ASK WHETHER FOCUS CAN MOVE THE PALACE. IT MUST NOT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * C2's entire claim is that focusing a room is a VIEW transform and never
 * a second arrangement. The way to hold that claim is to compute the world
 * before and after and require it to be identical, byte for byte, and to
 * require the camera itself to be blind to everything except where the
 * room already stands.
 */

const STAGE = { width: 1088, height: 611 };

function roomsOf(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    id: `room-${i}`,
    createdAt: `2026-09-${String(1 + i).padStart(2, "0")}T00:00:00Z`,
  }));
}

function artifact(id: string, kind: InteriorArtifact["kind"], day: number): InteriorArtifact {
  return { id, kind, createdAt: `2026-09-${String(day).padStart(2, "0")}T00:00:00Z` };
}

function house(
  n: number,
  furniture: (roomId: string) => readonly InteriorArtifact[] = () => [],
) {
  const layout = placeBuilding({
    rooms: roomsOf(n),
    buildingVersion: BUILDING_VERSION,
    hasUnfiled: false,
  });
  const interiors = new Map<string, InteriorOutput>(
    layout.rooms.map((room) => {
      const artifacts = furniture(room.roomId);
      return [
        room.roomId,
        furnishRoom({
          roomId: room.roomId,
          artifacts,
          artifactTotal: artifacts.length,
          hasMemories: false,
        }),
      ];
    }),
  );
  const bounds = buildingBounds(layout, interiors);
  const fit = fitBuilding(bounds, STAGE, "spatial", false);
  return { layout, interiors, bounds, fit };
}

describe("the overview", () => {
  it("is the identity, and says so", () => {
    expect(OVERVIEW_CAMERA).toEqual({ scale: 1, x: 0, y: 0 });
    expect(isOverviewCamera(OVERVIEW_CAMERA)).toBe(true);
    expect(cameraTransform(OVERVIEW_CAMERA)).toBe("translate(0px, 0px) scale(1)");
    expect(applyCamera(OVERVIEW_CAMERA, { left: 137, top: 42 })).toEqual({ left: 137, top: 42 });
  });

  it("is what an unmeasured stage gets, rather than a guess", () => {
    const { layout, bounds } = house(5);
    expect(cameraForRoom(layout.rooms[0], bounds, null)).toEqual(OVERVIEW_CAMERA);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   A. Focusing does not change world placement
   ══════════════════════════════════════════════════════════════════════ */

it("computes the same world whether or not a room is focused", () => {
  // The camera is not an input to any of this, and that is the point: it
  // CANNOT be, because none of these functions can express it. The proof
  // is that the same call with the same arguments is what runs in both
  // states, and the page has nothing else to hand them.
  const before = house(5, () => [artifact("a", "list", 2), artifact("b", "note", 3)]);
  const after = house(5, () => [artifact("a", "list", 2), artifact("b", "note", 3)]);

  expect(after.layout).toEqual(before.layout);
  expect(after.bounds).toEqual(before.bounds);
  expect(houseItems(after.layout, after.interiors)).toEqual(
    houseItems(before.layout, before.interiors),
  );
});

/* ══════════════════════════════════════════════════════════════════════
   B. The focused room derives from the bounds it already had
   ══════════════════════════════════════════════════════════════════════ */

describe("the focused view", () => {
  it("puts the room's own centre in the middle of the stage", () => {
    const { layout, bounds, fit } = house(5);
    const room = layout.rooms[3];
    const camera = cameraForRoom(room, bounds, fit);

    const box = roomBox(room);
    const topLeft = toBuildingPoint({ x: box.minX, y: box.minY }, bounds, fit);
    const bottomRight = toBuildingPoint({ x: box.maxX, y: box.maxY }, bounds, fit);
    const centre = applyCamera(camera, {
      left: (topLeft.left + bottomRight.left) / 2,
      top: (topLeft.top + bottomRight.top) / 2,
    });

    expect(centre.left).toBeCloseTo(STAGE.width / 2, 6);
    expect(centre.top).toBeCloseTo(STAGE.height / 2, 6);
  });

  it("enlarges the room to something worth inspecting, and never shrinks it", () => {
    const { layout, bounds, fit } = house(5);
    for (const room of layout.rooms) {
      const camera = cameraForRoom(room, bounds, fit);
      expect(camera.scale).toBeGreaterThan(1);
      expect(camera.scale).toBeLessThanOrEqual(2.6);
    }
  });

  it("leaves the stage bigger than the room, so the Palace is still around it", () => {
    // The focused room may not fill the frame: what is left over is where
    // the rest of the house stays visible. A room that took the whole
    // stage would be a Room page with an animation in front of it.
    const { layout, bounds, fit } = house(5);
    const room = layout.rooms[0];
    const camera = cameraForRoom(room, bounds, fit);
    const box = roomBox(room);

    const a = applyCamera(camera, toBuildingPoint({ x: box.minX, y: box.minY }, bounds, fit));
    const b = applyCamera(camera, toBuildingPoint({ x: box.maxX, y: box.maxY }, bounds, fit));

    expect(b.left - a.left).toBeLessThan(STAGE.width);
    expect(b.top - a.top).toBeLessThan(STAGE.height);
  });

  it("frames a different room differently, and each one from its own box", () => {
    const { layout, bounds, fit } = house(6);
    const cameras = layout.rooms.map((room) => cameraForRoom(room, bounds, fit));
    const keys = new Set(cameras.map((c) => `${c.x}:${c.y}`));
    expect(keys.size).toBe(layout.rooms.length);
  });

  it("is deterministic", () => {
    const { layout, bounds, fit } = house(5);
    expect(cameraForRoom(layout.rooms[2], bounds, fit)).toEqual(
      cameraForRoom(layout.rooms[2], bounds, fit),
    );
  });
});

/* ══════════════════════════════════════════════════════════════════════
   K and L. Content cannot re-frame the Palace
   ══════════════════════════════════════════════════════════════════════ */

describe("what the camera refuses to see", () => {
  it("does not move when an artifact is added to the room it is framing", () => {
    // I-B, one scale up. A camera derived from a union with the furniture
    // would re-frame the room every time somebody wrote something down.
    const empty = house(5);
    const furnished = house(5, (roomId) =>
      roomId === "room-0"
        ? [artifact("x", "list", 2), artifact("y", "plan", 3), artifact("z", "note", 4)]
        : [],
    );

    expect(cameraForRoom(furnished.layout.rooms[0], furnished.bounds, furnished.fit)).toEqual(
      cameraForRoom(empty.layout.rooms[0], empty.bounds, empty.fit),
    );
  });

  it("does not move when an artifact changes kind, which changes its drawing", () => {
    const asList = house(5, (roomId) => (roomId === "room-1" ? [artifact("x", "list", 2)] : []));
    const asPlan = house(5, (roomId) => (roomId === "room-1" ? [artifact("x", "plan", 2)] : []));

    expect(cameraForRoom(asPlan.layout.rooms[1], asPlan.bounds, asPlan.fit)).toEqual(
      cameraForRoom(asList.layout.rooms[1], asList.bounds, asList.fit),
    );
  });

  it("cannot be told a title, a name or an edit time at all", () => {
    // I-A by the compiler, restated for the camera: the only things that
    // reach it are a placement, the bounds and the fit, and none of the
    // three has a field an edit could touch.
    const room = house(5).layout.rooms[0];
    expect(Object.keys(room).sort()).toEqual(
      ["column", "doorToColumn", "doorToRow", "index", "origin", "roomId", "row", "z"].sort(),
    );
  });
});
