import { createContext } from "react";

/**
 * Auth — provider-agnostic shape.
 *
 * Today this is fulfilled by `MockAuthProvider` (localStorage flag, no real
 * validation). When Keycloak is wired in a separate repo, drop in
 * `KeycloakAuthProvider` here without touching consumers — same context, same
 * hook surface, same User shape.
 *
 * Future Keycloak integration plan:
 *   1. Add `@react-keycloak/web` (or equivalent) in this workspace.
 *   2. Replace `MockAuthProvider` in `main.tsx` with `KeycloakAuthProvider`.
 *   3. `signIn(email, password)` becomes `keycloak.login()` (redirect flow).
 *   4. `signOut()` becomes `keycloak.logout()`.
 *   5. `user` derives from `keycloak.tokenParsed` (sub, email, name, roles).
 *   6. `provider` flips from `"mock"` to `"keycloak"`.
 */

export type AuthProviderKind = "mock" | "keycloak";

export type User = {
  id: string;
  email: string;
  name: string;
  roles: readonly string[];
  provider: AuthProviderKind;
};

export type AuthStatus = "loading" | "authenticated" | "unauthenticated";

export type AuthContextValue = {
  status: AuthStatus;
  user: User | null;
  isAuthenticated: boolean;
  provider: AuthProviderKind;
  /** Mock today. Becomes `keycloak.login()` redirect under Keycloak. */
  signIn: (email: string, password: string) => Promise<void>;
  /** Mock today. Becomes `keycloak.logout()` under Keycloak. */
  signOut: () => Promise<void>;
};

export const AuthContext = createContext<AuthContextValue | null>(null);
