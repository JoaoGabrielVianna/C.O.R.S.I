import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { AuthContext, type AuthStatus, type User } from "./context";
import { apiFetch, onUnauthorized } from "@/lib/api/client";

/**
 * AuthProvider — the real, server-backed session.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE SESSION IS A COOKIE THIS CODE CANNOT READ, AND THAT IS THE POINT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * What this replaced did `void password`, invented a user, and wrote it to
 * `localStorage` — so "logged in" was a string the browser owned and any
 * script on the page could forge or read. The session now lives in an
 * HttpOnly cookie the backend sets: JavaScript cannot see it, cannot copy
 * it, and cannot fabricate one.
 *
 * The consequence for this file is that there is NO client-side session
 * state to persist. `user` here is a cache of what the server said, not the
 * credential, and losing it costs one request. On boot the provider asks
 * `GET /auth/session` and believes the answer; a 401 is not an error, it is
 * the answer "nobody is logged in".
 *
 * ── Why a 401 anywhere logs the UI out ─────────────────────────────────
 * Because the session can end while the tab is open — it has an absolute
 * expiry, and a logout in another tab revokes it server-side. Without this
 * the operator would sit in a fully drawn app where every panel failed, and
 * nothing on screen would say why. `onUnauthorized` is registered with the
 * API client so the first refused request flips this provider to
 * unauthenticated, and `RequireAuth` sends them to the login screen.
 */

type SessionResponse = { email: string; expires_at: string };

/**
 * The one subject. The backend returns an address and an expiry and
 * nothing else — there is no profile to fetch and no role to branch on —
 * so the display name is derived here rather than invented by the server.
 */
function toUser(s: SessionResponse): User {
  return {
    id: "operator",
    email: s.email,
    name: s.email.split("@")[0] ?? s.email,
    roles: ["operator"],
    provider: "session",
  };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  // Starts as `loading`, never as `unauthenticated`. Starting unauthenticated
  // would make RequireAuth redirect to /login on every hard refresh before
  // the session check came back — a logged-in operator watching their own
  // app bounce them out once per reload.
  const [status, setStatus] = useState<AuthStatus>("loading");

  const clear = useCallback(() => {
    setUser(null);
    setStatus("unauthenticated");
  }, []);

  // Bootstrap: ask the server who we are.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const s = await apiFetch<SessionResponse>("/auth/session");
        if (!cancelled) {
          setUser(toUser(s));
          setStatus("authenticated");
        }
      } catch {
        // Any failure — 401, network, an API that is down — resolves to
        // "not logged in". Showing the app to someone whose session could
        // not be confirmed is the one outcome that must not happen.
        if (!cancelled) clear();
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [clear]);

  // A refused request anywhere in the app ends the session here too.
  useEffect(() => onUnauthorized(clear), [clear]);

  const signIn = useCallback(async (email: string, password: string) => {
    setStatus("loading");
    try {
      const s = await apiFetch<SessionResponse>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
      });
      setUser(toUser(s));
      setStatus("authenticated");
    } catch (err) {
      setUser(null);
      setStatus("unauthenticated");
      throw err;
    }
  }, []);

  const signOut = useCallback(async () => {
    try {
      // Server-side revocation: the row is deleted, so the cookie is dead
      // even for a copy of it held anywhere else.
      await apiFetch<void>("/auth/logout", { method: "POST" });
    } catch {
      // A logout that could not reach the server still clears the local
      // view. The cookie may survive until it expires, which is worse than
      // a clean logout and better than a UI that refuses to let go.
    }
    clear();
  }, [clear]);

  const value = useMemo(
    () => ({
      status,
      user,
      isAuthenticated: status === "authenticated" && user !== null,
      provider: "session" as const,
      signIn,
      signOut,
    }),
    [status, user, signIn, signOut],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
