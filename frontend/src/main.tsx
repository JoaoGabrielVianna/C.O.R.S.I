import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "./index.css";
import App from "./App.tsx";
import { I18nProvider } from "@/lib/i18n";
import { ThemeProvider } from "@/lib/theme";
import { AuthProvider } from "@/lib/auth";
import { API_BASE } from "@/lib/api/client";
import { checkBackendIdentity } from "@/lib/api/backendIdentity";
import { BackendMismatchScreen } from "@/lib/api/BackendMismatchScreen";

// One QueryClient per app. Defaults are conservative — finance data is
// per-workspace and changes slowly; 30s staleTime is tuned per query in
// the hooks themselves.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

const root = createRoot(document.getElementById("root")!);

const app = (
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <I18nProvider>
          {/*
            AuthProvider — currently MockAuthProvider. When external auth
            lands, swap the implementation in `@/lib/auth/AuthProvider.tsx`;
            this tree and every consumer (`useAuth`, RequireAuth, Sidebar,
            UserMenu) keeps working against the same context contract.
          */}
          <AuthProvider>
            <App />
          </AuthProvider>
        </I18nProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>
);

/**
 * In development, confirm the API behind us is C.O.R.S.I. before rendering.
 *
 * ── Why only in development ────────────────────────────────────────────
 * Locally the API is whatever happens to hold a port, and getting that
 * wrong costs an afternoon of debugging a product that was never running.
 * In production the frontend and the API are served behind the same
 * upstream, so a failed identity check would not mean "wrong backend", it
 * would mean "the probe hiccuped" — and blanking a working product over
 * that would be an outage this guard invented rather than prevented.
 *
 * ── Why `unreachable` still renders ────────────────────────────────────
 * Because the usual cause is a backend that has not finished starting, and
 * refusing to draw the UI until the API is up would be a worse tool than
 * the one we have. Only a POSITIVE identification of something else stops
 * the app.
 */
if (import.meta.env.DEV) {
  void checkBackendIdentity(API_BASE).then((identity) => {
    if (identity.kind === "mismatch") {
      root.render(
        <BackendMismatchScreen received={identity.received} target={identity.target} />,
      );
      return;
    }
    root.render(app);
  });
} else {
  root.render(app);
}
