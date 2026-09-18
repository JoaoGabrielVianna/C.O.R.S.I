/**
 * Wire types for the Palace read surface.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THESE MIRROR THE BACKEND DTOs 1:1. THEY ARE NOT DOMAIN TYPES
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Nothing here is adapted, renamed, defaulted or enriched. The backend
 * decides what a Room is; this file only says what arrives on the wire so
 * TypeScript can check the call sites.
 *
 * ── What is deliberately absent ────────────────────────────────────────
 * Any notion of "include highly sensitive". There is no field for it, no
 * parameter for it, and no function that could carry it. The most withheld
 * content does not exist for this surface, and the type system is the
 * first place that is true: a component cannot ask for it because there is
 * nothing to type.
 *
 * `sensitivity` is therefore `"normal" | "private"` and not the domain's
 * three levels. That is not a guess about the backend — it is the
 * backend's guarantee, written down where a developer reads it.
 */

/** Lifecycle, as the surface exposes it. */
export type PalaceStatus = "active" | "archived";

/**
 * The levels this surface can ever return.
 *
 * `highly_sensitive` is absent on purpose. If it ever appeared in a
 * response, this type would be wrong — and that is the right failure,
 * because the response would be the bug.
 */
export type PalaceSensitivity = "normal" | "private";

export type ArtifactKind = "project" | "list" | "plan" | "note";

export type MemoryKind =
  | "fact"
  | "preference"
  | "idea"
  | "decision"
  | "learning"
  | "reflection";

export type MemoryConfidence = "low" | "medium" | "high";

/** The envelope every Palace listing returns. */
export interface PalacePage<T> {
  items: T[];
  /** Unbounded count under the same predicate as `items`. */
  total: number;
  limit: number;
  offset: number;
}

/* ── the map ─────────────────────────────────────────────────────────── */

/**
 * One room on the map.
 *
 * No `status`: the map is the active space, so a field that always read
 * `active` would be one a client could start branching on.
 *
 * `archived_count` IS on the wire, because the room's own read uses it.
 * The map does not draw it: archived content has its own address.
 */
export interface OverviewRoom {
  room_id: string;
  name: string;
  description: string;
  sensitivity: PalaceSensitivity;
  created_at: string;
  updated_at: string;
  artifact_count: number;
  memory_count: number;
  archived_count: number;
}

export interface Overview {
  rooms: OverviewRoom[];
  /** Eligible active rooms, ignoring the ceiling `rooms` was cut to. */
  room_total: number;
  unfiled: { artifact_count: number; archived_count: number };
}

/* ── rooms ───────────────────────────────────────────────────────────── */

export interface RoomRow {
  room_id: string;
  name: string;
  description: string;
  status: PalaceStatus;
  sensitivity: PalaceSensitivity;
  /** Stable ordering key. Not `updated_at`, which moves on every edit. */
  created_at: string;
  updated_at: string;
}

export interface RoomDetail extends RoomRow {
  artifact_count: number;
  memory_count: number;
  archived_count: number;
}

/* ── artifacts ───────────────────────────────────────────────────────── */

export interface ArtifactRow {
  artifact_id: string;
  kind: ArtifactKind;
  title: string;
  status: PalaceStatus;
  sensitivity: PalaceSensitivity;
  room_id: string | null;
  item_count: number;
  item_done_count: number;
  body_excerpt: string;
  body_truncated: boolean;
  created_at: string;
  updated_at: string;
}

export interface ArtifactItem {
  item_id: string;
  position: number;
  text: string;
  done: boolean;
}

export interface ArtifactRoomRef {
  room_id: string;
  name: string;
}

export interface ArtifactDetail {
  artifact_id: string;
  kind: ArtifactKind;
  title: string;
  body: string;
  status: PalaceStatus;
  sensitivity: PalaceSensitivity;
  room_id: string | null;
  /** Null exactly when the artifact is unfiled. Never a withheld room. */
  room: ArtifactRoomRef | null;
  created_at: string;
  updated_at: string;
  items: ArtifactItem[];
  item_total: number;
  item_limit: number;
  item_offset: number;
}

/* ── memories ────────────────────────────────────────────────────────── */

export interface MemoryRow {
  memory_id: string;
  kind: MemoryKind;
  importance: number;
  confidence: MemoryConfidence;
  status: PalaceStatus;
  sensitivity: PalaceSensitivity;
  occurred_at: string | null;
  room_id: string | null;
  artifact_id: string | null;
  summary: string;
  content_excerpt: string;
  content_truncated: boolean;
  created_at: string;
  updated_at: string;
}

export interface MemoryDetail {
  memory_id: string;
  kind: MemoryKind;
  content: string;
  summary: string;
  importance: number;
  confidence: MemoryConfidence;
  status: PalaceStatus;
  sensitivity: PalaceSensitivity;
  occurred_at: string | null;
  room_id: string | null;
  artifact_id: string | null;
  created_at: string;
  updated_at: string;
}
