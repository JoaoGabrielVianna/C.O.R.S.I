import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { LandingPage } from "@/pages/Landing";
import { LoginPage } from "@/pages/Login";
import { RequireAuth } from "@/components/auth/RequireAuth";
import { WorkspaceLayout } from "@/components/workspace/WorkspaceLayout";
import { DashboardPage } from "@/pages/app/Dashboard";
import { IntelligencePage } from "@/pages/app/modules/Intelligence";
import { ContentPage } from "@/pages/app/modules/Content";
import { SettingsLayout } from "@/pages/app/settings/SettingsLayout";
import { ProfileSettingsPage } from "@/pages/app/settings/Profile";
import { SecuritySettingsPage } from "@/pages/app/settings/Security";
import { PreferencesSettingsPage } from "@/pages/app/settings/Preferences";
import { ModulesSettingsPage } from "@/pages/app/settings/Modules";
import { IntegrationsSettingsPage } from "@/pages/app/settings/Integrations";
import { DEFAULT_APP_ROUTE } from "@/lib/navVisibility";

/**
 * Heavy module pages are loaded on demand so they don't sit in the main
 * bundle. When a user navigates to /app/modules/job-radar (or finance), Vite
 * fetches the matching chunk; subsequent visits are cached.
 */
const JobRadarPage = lazy(() =>
  import("@/pages/app/modules/job-radar").then((m) => ({ default: m.JobRadarPage })),
);
const FinancePage = lazy(() =>
  import("@/pages/app/modules/finance").then((m) => ({ default: m.FinancePage })),
);
const PersonDetailPage = lazy(() =>
  import("@/pages/app/PersonDetail").then((m) => ({ default: m.PersonDetailPage })),
);
const AgentsPage = lazy(() =>
  import("@/pages/app/modules/agents").then((m) => ({ default: m.AgentsPage })),
);
// Release history owns its own nested routes, so one lazy chunk covers the
// overview, the per-module timeline and a single release.
const ReleasesPage = lazy(() =>
  import("@/pages/app/releases").then((m) => ({ default: m.ReleasesPage })),
);

/**
 * App — router shell.
 *
 * Public:
 *   `/`              → Landing (the lab)
 *   `/login`         → Private auth (single email/password form)
 *
 * Authenticated (gated by RequireAuth + wrapped in WorkspaceLayout):
 *   `/app`                           → Navigate to DEFAULT_APP_ROUTE
 *   `/app/dashboard`                 → Dashboard
 *   `/app/modules/job-radar`         → Job Radar      (lazy chunk)
 *   `/app/modules/finance`           → Finance        (lazy chunk)
 *   `/app/modules/agents/*`          → Agents         (lazy chunk, owns its
 *                                      own routes — see the module's index)
 *   `/app/modules/news`              → Market Intelligence
 *   `/app/modules/content`           → Content Engine
 *   `/app/settings`                  → SettingsLayout (shell + persistent
 *                                      section nav), index → profile
 *   `/app/settings/profile`          → Profile
 *   `/app/settings/integrations`     → Integrations
 *   `/app/settings/preferences`      → Preferences
 *   `/app/settings/modules`          → Modules
 *   `/app/settings/security`         → Security
 *   `/app/account`                   → redirect to /app/settings/profile
 *
 * Anything else → redirect to `/`.
 */
function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<LandingPage />} />
        <Route path="/login" element={<LoginPage />} />

        <Route path="/app" element={<RequireAuth />}>
          <Route element={<WorkspaceLayout />}>
            <Route index element={<Navigate to={DEFAULT_APP_ROUTE} replace />} />
            <Route path="dashboard" element={<DashboardPage />} />

            <Route path="modules">
              <Route
                path="job-radar"
                element={
                  <Suspense fallback={<RouteFallback />}>
                    <JobRadarPage />
                  </Suspense>
                }
              />
              <Route
                path="finance"
                element={
                  <Suspense fallback={<RouteFallback />}>
                    <FinancePage />
                  </Suspense>
                }
              />
              {/* Agents declares its own routes below this point: the
                  module has an internal hierarchy (agent → conversations →
                  one conversation, plus its settings and capability
                  surfaces) and that shape belongs with the module, not in
                  the app shell. One lazy chunk still covers all of it. */}
              <Route
                path="agents/*"
                element={
                  <Suspense fallback={<RouteFallback />}>
                    <AgentsPage />
                  </Suspense>
                }
              />
              <Route path="news"      element={<IntelligencePage />} />
              <Route path="content"   element={<ContentPage />} />
            </Route>

            {/* Every section renders inside `SettingsLayout`, which draws
                the area's one header and the nav that lists the others.
                Before this the five were siblings with no shell, so each
                one was a dead end. */}
            <Route path="settings" element={<SettingsLayout />}>
              <Route index element={<Navigate to="profile" replace />} />
              <Route path="profile"      element={<ProfileSettingsPage />} />
              <Route path="integrations" element={<IntegrationsSettingsPage />} />
              <Route path="preferences"  element={<PreferencesSettingsPage />} />
              <Route path="modules"      element={<ModulesSettingsPage />} />
              <Route path="security"     element={<SecuritySettingsPage />} />
              {/* An unknown section lands on the first one rather than
                  leaving the shell drawn around an empty outlet. */}
              <Route path="*" element={<Navigate to="/app/settings/profile" replace />} />
            </Route>

            <Route
              path="releases/*"
              element={
                <Suspense fallback={<RouteFallback />}>
                  <ReleasesPage />
                </Suspense>
              }
            />

            {/* `/app/account` was a second identity page: name, email,
                roles and provider under the title "Account", beside a
                "Profile" showing name and email. Profile absorbed it, and
                the address still resolves so an old link or a ⌘K history
                entry does not dead-end. */}
            <Route path="account" element={<Navigate to="/app/settings/profile" replace />} />
            <Route
              path="people/:id"
              element={
                <Suspense fallback={<RouteFallback />}>
                  <PersonDetailPage />
                </Suspense>
              }
            />
          </Route>
        </Route>

        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

function RouteFallback() {
  return (
    <div className="flex h-full min-h-[200px] items-center justify-center">
      <div className="h-1 w-32 overflow-hidden rounded-full bg-(--color-muted)">
        <div className="h-full w-1/3 animate-pulse rounded-full bg-(--color-brand-500)" />
      </div>
    </div>
  );
}

export default App;
