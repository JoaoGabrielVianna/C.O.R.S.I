/**
 * TanStack Query hooks for the Palace read surface.
 *
 * ── Why one file and not six ───────────────────────────────────────────
 * Because they are six lines each and they share one key factory. Split
 * across files, the factory becomes an import that somebody eventually
 * bypasses by writing a key literal, and a key literal is a cache entry
 * that never gets invalidated with the others.
 *
 * ── Workspace in the key ───────────────────────────────────────────────
 * Same pattern as `useCards`: every key starts with the current workspace
 * id, so switching workspace cannot serve another one's rows from cache.
 * Nothing here mutates, so there is no invalidation to get wrong.
 *
 * ── No local state ─────────────────────────────────────────────────────
 * These hooks read. They do not merge anything from `localStorage`, they
 * do not default a missing field, and they do not keep an optimistic copy
 * of anything. The server is the only source of truth for the operator's
 * knowledge, and a cache that enriched it would be a second one.
 *
 * ── Freshness: 30 seconds, and what that costs ─────────────────────────
 * Every one of these had no `staleTime`, which means zero: leaving the Map
 * for a room and coming back re-ran the same read, because the component
 * remounted — not because anything changed. Measured at 120ms RTT it was a
 * full request on every return, forever.
 *
 * 30s is chosen against a specific risk rather than as a default. The only
 * writer to Palace is an agent holding the `palace.*` grants, working
 * through chat; this frontend has no mutation, so there is no local write
 * whose invalidation we could hang the refresh on. The honest tradeoff is
 * therefore a WINDOW, and a short one: for up to 30 seconds a Memory the
 * agent just wrote may not be on this screen yet.
 *
 * That is accepted, and it is not papered over with a poll: polling would
 * spend a request per interval on the far more common case of nothing
 * having changed, and an invented cross-channel invalidation would be a
 * mechanism claiming a guarantee the product cannot make.
 */

import { keepPreviousData, useQuery, type UseQueryResult } from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";

import {
  getArtifact,
  getOverview,
  getMemory,
  getRoom,
  listArtifacts,
  listMemories,
  listRooms,
  type GetArtifactParams,
  type ListArtifactsParams,
  type ListMemoriesParams,
  type ListRoomsParams,
} from "../api/palace";
import type {
  ArtifactDetail,
  Overview,
  ArtifactRow,
  MemoryDetail,
  MemoryRow,
  PalacePage,
  RoomDetail,
  RoomRow,
} from "../api/types";

/** The root key. Everything Palace caches hangs off it. */
const rootKey = (workspaceId: string) => ["palace", workspaceId] as const;

/**
 * How long a Palace read stays fresh. See the header: the window is the
 * price of having no local writer to invalidate on.
 */
const FRESH = 30_000;

/**
 * Listings keep the previous page on screen while the next one loads.
 *
 * ── What this is for ───────────────────────────────────────────────────
 * Typing in the Library's search box changes the query key on every
 * keystroke, and without this the rows a person is reading are replaced by
 * a skeleton between each one: measured at 118ms locally, more than 250ms
 * at a real round trip. They were reading those rows.
 *
 * ── What it does NOT claim ─────────────────────────────────────────────
 * That the rows still match. `isPlaceholderData` is true for exactly as
 * long as they are the OLD result, and the surface says so out loud — see
 * `LibraryRefreshing`. Nothing here decides how it is presented; it only
 * keeps the data alive long enough for the surface to be honest about it.
 *
 * ── Why only the LISTINGS ──────────────────────────────────────────────
 * A detail read is by id. Holding the previous one would draw one
 * artifact's title, sensitivity and body under another artifact's address,
 * which is not a stale list — it is the wrong object.
 */
const LIST_CONTINUITY = { placeholderData: keepPreviousData } as const;

export function usePalaceOverview(): UseQueryResult<Overview, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "overview"],
    queryFn: () => getOverview(),
    staleTime: FRESH,
  });
}

export function usePalaceRooms(
  params: ListRoomsParams,
): UseQueryResult<PalacePage<RoomRow>, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "rooms", params],
    queryFn: () => listRooms(params),
    staleTime: FRESH,
    ...LIST_CONTINUITY,
  });
}

export function usePalaceRoom(
  roomId: string | undefined,
): UseQueryResult<RoomDetail, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "room", roomId],
    queryFn: () => getRoom(roomId as string),
    staleTime: FRESH,
    enabled: Boolean(roomId),
  });
}

export function usePalaceArtifacts(
  params: ListArtifactsParams,
): UseQueryResult<PalacePage<ArtifactRow>, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "artifacts", params],
    queryFn: () => listArtifacts(params),
    staleTime: FRESH,
    ...LIST_CONTINUITY,
  });
}

export function usePalaceArtifact(
  artifactId: string | undefined,
  params: GetArtifactParams = {},
): UseQueryResult<ArtifactDetail, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "artifact", artifactId, params],
    queryFn: () => getArtifact(artifactId as string, params),
    staleTime: FRESH,
    enabled: Boolean(artifactId),
  });
}

export function usePalaceMemories(
  params: ListMemoriesParams,
): UseQueryResult<PalacePage<MemoryRow>, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "memories", params],
    queryFn: () => listMemories(params),
    staleTime: FRESH,
    ...LIST_CONTINUITY,
  });
}

export function usePalaceMemory(
  memoryId: string | undefined,
): UseQueryResult<MemoryDetail, Error> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...rootKey(workspaceId), "memory", memoryId],
    queryFn: () => getMemory(memoryId as string),
    staleTime: FRESH,
    enabled: Boolean(memoryId),
  });
}
