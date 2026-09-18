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
import {
  AgentsPage,
  FinancePage,
  JobRadarPage,
  PalacePage,
  PersonDetailPage,
  ReleasesPage,
} from "@/lib/lazyRoutes";

/**
 * Heavy module pages are loaded on demand so they don't sit in the main
 * bundle. When a user navigates to /app/modules/job-radar (or finance), Vite
 * fetches the matching chunk; subsequent visits are cached.
 *
 * The `lazy()` calls moved to `lib/lazyRoutes`, unchanged, so that the
 * sidebar's hover-preload and the route below can name the SAME `import()`.
 * Two copies of a specifier drift silently: everything keeps working while
 * the hover warms a chunk the click does not use.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   NO ROUTE DECLARES ITS OWN SUSPENSE BOUNDARY. THERE IS EXACTLY ONE,
 *   IN `WorkspaceLayout`, AROUND THE `<Outlet />`
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Every lazy route below used to be wrapped in its own
 * `<Suspense fallback={<RouteFallback />}>`. Six sibling boundaries, and
 * the position of the boundary in the element tree changed with the route:
 * `settings/profile` sits one level deeper than `modules/finance`, and
 * `releases/*` is a splat where `finance` is static. When the position
 * changes, React does not reconcile the old boundary with the new one — it
 * MOUNTS a new one, and a boundary that mounts already suspended has no
 * previous content to keep, so it draws the fallback and then holds it for
 * React's reveal throttle.
 *
 * Measured in Chrome on the production build, with network latency emulated
 * at ZERO and the chunk served in 1 to 3ms: 17 frames, 277 to 284ms, of the
 * route area replaced by a loading bar. Not network — a floor. That is what
 * reads as "the app reloaded" when you switch modules.
 *
 * One boundary above the `<Outlet />` is stable across every navigation, so
 * it is reconciled rather than remounted. React Router runs navigation in
 * `startTransition`, and a transition that suspends against an ALREADY
 * MOUNTED boundary keeps the previous content on screen until the new one
 * is ready. Nothing disappears.
 *
 * Adding a `<Suspense>` back here, per route or per group, reintroduces the
 * defect. `App.suspense.test.tsx` fails if this file mentions one.
 */

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
              <Route path="job-radar" element={<JobRadarPage />} />
              <Route path="finance"   element={<FinancePage />} />
              {/* Agents declares its own routes below this point: the
                  module has an internal hierarchy (agent → conversations →
                  one conversation, plus its settings and capability
                  surfaces) and that shape belongs with the module, not in
                  the app shell. One lazy chunk still covers all of it. */}
              <Route path="agents/*"  element={<AgentsPage />} />
              <Route path="palace/*"  element={<PalacePage />} />
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

            <Route path="releases/*" element={<ReleasesPage />} />

            {/* `/app/account` was a second identity page: name, email,
                roles and provider under the title "Account", beside a
                "Profile" showing name and email. Profile absorbed it, and
                the address still resolves so an old link or a ⌘K history
                entry does not dead-end. */}
            <Route path="account" element={<Navigate to="/app/settings/profile" replace />} />
            <Route path="people/:id" element={<PersonDetailPage />} />
          </Route>
        </Route>

        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
