import { useMemo, type ReactNode } from "react";
import { AuthContext, type AuthContextValue, type AuthStatus, type User } from "./context";

/**
 * AuthFixture — a signed-in shell for tests that are not about signing in.
 *
 * ── Why this exists, and why it is not "seed localStorage" ─────────────
 * It used to be. `AuthProvider` read a fake user out of `localStorage`
 * during `useState` initialisation, so a test could hand itself a session
 * by writing a JSON blob. The session is now a cookie the server issues and
 * this code cannot read, and the provider asks the API who it is — which is
 * correct, and which means a storage key can no longer forge a login.
 *
 * Without a fixture, every shell test that merely needs a name in the
 * sidebar would have to stub `fetch` and model an endpoint that has nothing
 * to do with its subject. `I18nFixture` solves the same problem for
 * language, and this is the same shape for the same reason: supply the
 * CONTEXT directly, and leave the provider to the tests that are actually
 * about it.
 *
 * This is test-only. It is not exported from `lib/auth/index.ts`, so no
 * screen can reach for it by accident.
 */
export function AuthFixture({
  children,
  status = "authenticated",
  user = {
    id: "operator",
    email: "joao@corsi.dev",
    name: "João Corsi",
    roles: ["operator"],
    provider: "session",
  },
  signIn = async () => {},
  signOut = async () => {},
}: {
  children: ReactNode;
  status?: AuthStatus;
  user?: User | null;
  signIn?: AuthContextValue["signIn"];
  signOut?: AuthContextValue["signOut"];
}) {
  const value = useMemo<AuthContextValue>(
    () => ({
      status,
      user,
      isAuthenticated: status === "authenticated" && user !== null,
      provider: "session",
      signIn,
      signOut,
    }),
    [status, user, signIn, signOut],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
