/**
 * Turning the read model into a LayoutInput.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE NARROWING HAPPENS HERE, AND IT IS THE POINT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The API returns titles, bodies, statuses, sensitivities, counts and
 * timestamps. Two fields per object reach the geometry: `id` and
 * `createdAt`. The rest stops at this line, and the layout's types would
 * refuse it anyway.
 *
 * `memory_count` becomes `hasMemories`, a boolean, right here. The number
 * exists on the wire because a room card shows it; it must not travel one
 * step further, because a count in the geometry is a count that eventually
 * decides something.
 *
 * ── What this hook does NOT do ─────────────────────────────────────────
 * Filter. Sort. Hide. Reveal. Everything it receives is already eligible:
 * the visibility rules live in SQL and were settled before the response
 * was written. There is no second opinion here and there must not be one.
 */

import { useMemo } from "react";

import { LAYOUT_VERSION, layout, type LayoutContainer, type LayoutOutput } from "../layout/engine";
import { LAYOUT_KINDS } from "../layout/kinds";
import { usePalaceArtifacts, usePalaceRoom } from "./usePalace";
import type { ArtifactKind, RoomDetail } from "../api/types";

/**
 * How many artifacts one room contributes to its scene.
 *
 * The listing's ceiling. A room with more than this shows the first page
 * of them, which is a known limit rather than a designed one: see the
 * readback's debts.
 */
const SCENE_ARTIFACT_LIMIT = 100;

export interface RoomScene {
  room: RoomDetail | undefined;
  layout: LayoutOutput;
  /** Titles by artifact id, for accessible names. Never reaches geometry. */
  titles: Map<string, string>;
  isPending: boolean;
  error: Error | null;
  refetch: () => void;
}

export function useRoomScene(roomId: string | undefined): RoomScene {
  const roomQuery = usePalaceRoom(roomId);
  const artifactsQuery = usePalaceArtifacts({
    room_id: roomId,
    status: "active",
    limit: SCENE_ARTIFACT_LIMIT,
    offset: 0,
  });

  const artifacts = artifactsQuery.data?.items;
  const hasMemories = (roomQuery.data?.memory_count ?? 0) > 0;

  const built = useMemo(() => {
    const byKind = new Map<ArtifactKind, LayoutContainer>();
    for (const kind of LAYOUT_KINDS) byKind.set(kind, { kind, objects: [] });

    const titles = new Map<string, string>();
    for (const artifact of artifacts ?? []) {
      titles.set(artifact.artifact_id, artifact.title);
      const container = byKind.get(artifact.kind);
      if (!container) continue;
      byKind.set(artifact.kind, {
        kind: artifact.kind,
        // Two fields. The compiler refuses the rest.
        objects: [
          ...container.objects,
          { id: artifact.artifact_id, createdAt: artifact.created_at },
        ],
      });
    }

    return {
      titles,
      layout: layout({
        roomId: roomId ?? "",
        layoutVersion: LAYOUT_VERSION,
        containers: [...byKind.values()],
        hasMemories,
      }),
    };
  }, [artifacts, hasMemories, roomId]);

  return {
    room: roomQuery.data,
    layout: built.layout,
    titles: built.titles,
    isPending: roomQuery.isPending || artifactsQuery.isPending,
    error: roomQuery.error ?? artifactsQuery.error,
    refetch: () => {
      void roomQuery.refetch();
      void artifactsQuery.refetch();
    },
  };
}
