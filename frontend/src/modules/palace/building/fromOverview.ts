/**
 * The seam between the wire and the arrangement.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE ORDER THE BACKEND SENDS IS NOT THE ORDER THE BUILDING USES
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `GET /palace/overview` returns rooms in `updated_at DESC, id`
 * (`repo/rooms.go:82`). That is a good order for a listing and a
 * catastrophic one for a building: a Palace that trusted it would
 * rearrange itself completely the moment the operator edited anything,
 * and would do it silently, some time later, with nothing on screen
 * connecting the edit to the upheaval.
 *
 * This function is the one place that could get that wrong, so it is the
 * one place that is allowed to know both shapes. It does exactly one
 * thing: it throws away every field the arrangement must not see, keeping
 * the id and the creation instant. `placeBuilding` then sorts canonically.
 *
 * ── Why the narrowing happens HERE and not in the engine ───────────────
 * Because the engine's input type is the guarantee. If `placeBuilding`
 * took `OverviewRoom[]`, then `name`, `updated_at` and three counts would
 * be in scope inside it, and I-A would be a promise somebody keeps rather
 * than a thing the compiler checks. Narrowing at the boundary means the
 * engine cannot be handed a title even by accident.
 *
 * ── Why it does not sort ───────────────────────────────────────────────
 * Sorting here would put the canonical order in two places: this file and
 * the engine. Two implementations of one rule is how they come to
 * disagree. The engine sorts, always, whatever it is given — which is also
 * what makes the "shuffled wire" test meaningful rather than decorative.
 */

import type { ArtifactRow, OverviewRoom } from "../api/types";
import type { InteriorArtifact } from "./interior";
import type { BuildingRoom } from "./placement";

/**
 * Narrows wire rooms to what the arrangement may know.
 *
 * Deliberately NOT a spread. `{ ...room }` would carry every field on the
 * wire into the engine's input and would keep compiling as the DTO grew,
 * which is the failure this file exists to make impossible. Naming the two
 * fields means a new wire field arrives inert.
 */
export function buildingRoomsOf(rooms: readonly OverviewRoom[]): BuildingRoom[] {
  return rooms.map((room) => ({ id: room.room_id, createdAt: room.created_at }));
}

/**
 * Groups wire artifacts by the room they are filed in, narrowed to what
 * the furniture may know.
 *
 * ── Unfiled rows are dropped, not bucketed ─────────────────────────────
 * An artifact with `room_id === null` is in no room, so there is no room
 * to draw it in. It is reachable through the tray at the entrance, which
 * is where it already was. Inventing a room for it would be the surface
 * deciding where the operator's unfiled work belongs.
 *
 * ── Archived rows never arrive ─────────────────────────────────────────
 * The caller asks for `status: "active"`, and the backend applies the
 * filter. Nothing here re-checks it: a second status test in the frontend
 * would be a second opinion about eligibility, and the moment the two
 * disagreed the wrong one would be the one on screen.
 */
export function interiorArtifactsByRoom(
  artifacts: readonly ArtifactRow[],
): Map<string, InteriorArtifact[]> {
  const byRoom = new Map<string, InteriorArtifact[]>();

  for (const artifact of artifacts) {
    if (artifact.room_id === null) continue;
    const bucket = byRoom.get(artifact.room_id) ?? [];
    bucket.push({
      id: artifact.artifact_id,
      kind: artifact.kind,
      createdAt: artifact.created_at,
    });
    byRoom.set(artifact.room_id, bucket);
  }

  return byRoom;
}
