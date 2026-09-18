/**
 * Typed REST client for the Palace read surface.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   EVERY FUNCTION HERE IS A GET. THERE IS NO WRITE CLIENT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Writing to the Palace is a conversation with an authorized agent. This
 * module reads the operator's own record for the operator's own screens,
 * and it cannot create, change or delete anything: there is no function
 * that would.
 *
 * ── The one rule this file exists to make structural ───────────────────
 * No parameter named `include_highly_sensitive` is built here, and none
 * can be: `query()` accepts a closed set of keys and the type of that set
 * does not contain it. A component cannot pass it through, a URL cannot
 * carry it into a call, and a future function cannot forget to default it,
 * because there is nothing to default.
 *
 * The backend is the authority on visibility and this layer does not
 * reimplement any of it. What it does is make the wrong request
 * inexpressible on the way out.
 *
 * ── No adaptation ──────────────────────────────────────────────────────
 * No codec, no local metadata, no `localStorage`, no defaulting of missing
 * fields. Anything this layer invented would be a second source of truth
 * about the operator's own knowledge, which is the one thing this version
 * exists to avoid. If the backend says it does not exist, the Library does
 * not draw it.
 */

import { apiFetch } from "@/lib/api/client";

import type {
  ArtifactDetail,
  Overview,
  ArtifactKind,
  ArtifactRow,
  MemoryDetail,
  MemoryKind,
  MemoryRow,
  PalacePage,
  PalaceStatus,
  RoomDetail,
  RoomRow,
} from "./types";

/**
 * The sentinel that asks for the rows filed in no room.
 *
 * Exported because the URL state and the filter control both need to spell
 * it, and two literals would be one rename away from disagreeing. It is
 * the backend's word, not an invention: see `room_id=none`.
 */
export const UNFILED = "none";

/**
 * The closed set of query keys this client will serialise.
 *
 * ── Why a type and not `Record<string, unknown>` ───────────────────────
 * Because a permissive bag is how `include_highly_sensitive` eventually
 * gets passed: somebody spreads a params object from a URL, and the URL is
 * whatever a bookmark happened to carry. A closed union means the compiler
 * refuses the key, and nothing at runtime has to notice.
 */
const QUERY_KEYS = [
  "status",
  "search",
  "kind",
  "room_id",
  "artifact_id",
  "min_importance",
  "limit",
  "offset",
  "item_limit",
  "item_offset",
] as const;

type QueryKey = (typeof QUERY_KEYS)[number];

type Query = Partial<Record<QueryKey, string | number | undefined>>;

/**
 * Serialises ONLY the keys in `QUERY_KEYS`, by reading them out one at a
 * time rather than iterating the object.
 *
 * ── Why not `Object.entries` ───────────────────────────────────────────
 * Because the type is a compile-time promise and the object is a runtime
 * value, and those come apart exactly where it matters: a params object
 * spread from a bookmarked URL, or passed through an `any`, carries
 * whatever keys it happens to have. `Object.entries` would forward them.
 * Reading from a fixed list means an unrecognised key has nowhere to go,
 * whatever the caller believed it was passing.
 *
 * A test drives this with a smuggled `include_highly_sensitive` and
 * asserts it never reaches the wire. It reached it before this was a
 * list.
 */
function query(params: Query): string {
  const search = new URLSearchParams();
  for (const key of QUERY_KEYS) {
    const value = params[key];
    if (value === undefined || value === "") continue;
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : "";
}

/* ── the map ─────────────────────────────────────────────────────────── */

/**
 * The Palace Map's one read: every eligible active room, with what it
 * holds, plus the unfiled bucket. No parameters, because the map has no
 * filters: it IS the active space.
 */
export function getOverview(): Promise<Overview> {
  return apiFetch<Overview>("/palace/overview");
}

/* ── rooms ───────────────────────────────────────────────────────────── */

export interface ListRoomsParams {
  /** Omitted means the backend's default, which is `active`. */
  status?: PalaceStatus;
  search?: string;
  limit?: number;
  offset?: number;
}

export function listRooms(params: ListRoomsParams = {}): Promise<PalacePage<RoomRow>> {
  return apiFetch<PalacePage<RoomRow>>(`/palace/rooms${query(params)}`);
}

export function getRoom(roomId: string): Promise<RoomDetail> {
  return apiFetch<RoomDetail>(`/palace/rooms/${roomId}`);
}

/* ── artifacts ───────────────────────────────────────────────────────── */

export interface ListArtifactsParams {
  status?: PalaceStatus;
  search?: string;
  kind?: ArtifactKind;
  /** A room id, or `UNFILED`. Omitted means every room and the unfiled. */
  room_id?: string;
  limit?: number;
  offset?: number;
}

export function listArtifacts(
  params: ListArtifactsParams = {},
): Promise<PalacePage<ArtifactRow>> {
  return apiFetch<PalacePage<ArtifactRow>>(`/palace/artifacts${query(params)}`);
}

export interface GetArtifactParams {
  item_limit?: number;
  item_offset?: number;
}

export function getArtifact(
  artifactId: string,
  params: GetArtifactParams = {},
): Promise<ArtifactDetail> {
  return apiFetch<ArtifactDetail>(`/palace/artifacts/${artifactId}${query(params)}`);
}

/* ── memories ────────────────────────────────────────────────────────── */

export interface ListMemoriesParams {
  status?: PalaceStatus;
  search?: string;
  kind?: MemoryKind;
  room_id?: string;
  artifact_id?: string;
  min_importance?: number;
  limit?: number;
  offset?: number;
}

export function listMemories(
  params: ListMemoriesParams = {},
): Promise<PalacePage<MemoryRow>> {
  return apiFetch<PalacePage<MemoryRow>>(`/palace/memories${query(params)}`);
}

export function getMemory(memoryId: string): Promise<MemoryDetail> {
  return apiFetch<MemoryDetail>(`/palace/memories/${memoryId}`);
}
