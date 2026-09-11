import { Route, Routes } from "react-router-dom";

import { ModuleTimelinePage } from "./ModuleTimeline";
import { ReleaseDetailPage } from "./ReleaseDetail";
import { ReleasesHomePage } from "./ReleasesHome";

/**
 * Release History — the area's own router.
 *
 *   /app/releases                      every versioned module
 *   /app/releases/:moduleKey           one module, current + timeline
 *   /app/releases/:moduleKey/:version  one recorded snapshot
 *
 * ── Why this sits at /app/releases and not under /app/modules ──────────
 * It is not a module. It describes the installation: which modules exist,
 * what each one shipped, and when. Nesting it under `modules/` would make
 * it look like a fourth peer of Agents and Finance, and its route would
 * then have to answer "which module is this page about?" before the page
 * has picked one.
 */
export function ReleasesPage() {
  return (
    <Routes>
      <Route index element={<ReleasesHomePage />} />
      <Route path=":moduleKey" element={<ModuleTimelinePage />} />
      <Route path=":moduleKey/:version" element={<ReleaseDetailPage />} />
    </Routes>
  );
}
