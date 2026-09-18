/**
 * The Palace module shell.
 *
 * ── The index is the map now ───────────────────────────────────────────
 * It used to redirect to the Library, which was the honest shape of "that
 * surface does not exist yet". It exists, so the redirect is gone and
 * nothing else in this file changed — which is what the redirect was for.
 *
 * Two projections of one domain, each at its own address:
 *
 *	/palace                the space. "I want to see what is there"
 *	/palace/library        the rows. "I know what I am looking for"
 *
 * ── Why the module declares its own routes ─────────────────────────────
 * Same reasoning Agents uses: the module has an internal hierarchy (a
 * Library with three views, plus a read-by-id for each entity) and that
 * shape belongs with the module rather than in the app shell. One lazy
 * chunk still covers all of it.
 */

import { Navigate, Route, Routes } from "react-router-dom";

import { PalaceMap } from "./PalaceMap";
import { RoomView } from "./RoomView";
import {
  ArtifactDetailPage,
  MemoryDetailPage,
  RoomDetailPage,
} from "./library/Details";
import { LibraryHome } from "./library/LibraryHome";

export function PalacePage() {
  return (
    <Routes>
      <Route index element={<PalaceMap />} />
      <Route path="rooms/:roomId" element={<RoomView />} />
      <Route path="library">
        <Route index element={<LibraryHome />} />
        <Route path="rooms/:roomId" element={<RoomDetailPage />} />
        <Route path="artifacts/:artifactId" element={<ArtifactDetailPage />} />
        <Route path="memories/:memoryId" element={<MemoryDetailPage />} />
      </Route>
      {/* Anything else under the module is not a surface yet. */}
      <Route path="*" element={<Navigate to="/app/modules/palace/library" replace />} />
    </Routes>
  );
}
