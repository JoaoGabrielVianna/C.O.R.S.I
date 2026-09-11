import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { AuthContext, type AuthStatus, type User } from "./context";

/**
 * MockAuthProvider — local-only auth stand-in.
 *
 * Persists a fake session in localStorage so reloads keep the workspace
 * accessible during development. No password is actually checked — any input
 * that passes the Login form's client-side validation succeeds.
 *
 * Keycloak insertion point: swap this provider out in `main.tsx` for a
 * `KeycloakAuthProvider` that fulfills the same `AuthContextValue` contract.
 */

const SESSION_KEY = "corsi.session";

const MOCK_USER: User = {
  id: "u_joao_corsi",
  email: "joao@corsi.dev",
  name: "João Corsi",
  roles: ["operator"],
  provider: "mock",
};

function readStoredUser(): User | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as User;
    if (parsed && typeof parsed.email === "string") return parsed;
  } catch {
    /* corrupted or unavailable — fall through to null */
  }
  return null;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(() => readStoredUser());
  const [status, setStatus] = useState<AuthStatus>(() =>
    readStoredUser() ? "authenticated" : "unauthenticated",
  );

  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      if (user) window.localStorage.setItem(SESSION_KEY, JSON.stringify(user));
      else window.localStorage.removeItem(SESSION_KEY);
    } catch {
      /* ignore */
    }
  }, [user]);

  const signIn = useCallback(async (email: string, password: string) => {
    // Mock provider ignores the password. Keycloak swap-in will read it (or
    // skip it entirely in favor of an OIDC redirect flow).
    void password;
    setStatus("loading");
    await new Promise((r) => window.setTimeout(r, 250));
    const next: User = { ...MOCK_USER, email: email || MOCK_USER.email };
    setUser(next);
    setStatus("authenticated");
  }, []);

  const signOut = useCallback(async () => {
    setUser(null);
    setStatus("unauthenticated");
  }, []);

  const value = useMemo(
    () => ({
      status,
      user,
      isAuthenticated: status === "authenticated" && user !== null,
      provider: "mock" as const,
      signIn,
      signOut,
    }),
    [status, user, signIn, signOut],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
