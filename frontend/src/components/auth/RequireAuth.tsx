import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "@/lib/auth";

/**
 * RequireAuth — gates `/app/*` behind an authenticated session.
 *
 * Today the guard reads the mock `AuthProvider`. When Keycloak lands, the
 * same hook surface holds — only the provider implementation changes, so this
 * guard does not. Loading state shows a thin skeleton bar to avoid flashing
 * the redirect during session hydration.
 */
export function RequireAuth() {
  const { isAuthenticated, status } = useAuth();
  const location = useLocation();

  if (status === "loading") {
    return (
      <div className="grid h-dvh place-items-center bg-(--color-background)">
        <div className="h-1 w-40 overflow-hidden rounded-full bg-(--color-muted)">
          <div className="h-full w-1/3 animate-pulse rounded-full bg-(--color-brand-500)" />
        </div>
      </div>
    );
  }

  if (!isAuthenticated) {
    // pathname AND search. The query string is not decoration here: an
    // OAuth provider redirects back to `/app/settings/integrations?code=…`,
    // and dropping the query would discard a single-use authorization code
    // with nothing on screen to explain the silence. See lib/auth/returnTo.
    return (
      <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
    );
  }

  return <Outlet />;
}
