import { createContext } from "react";

/**
 * Auth — the shape consumers see.
 *
 * Fulfilled by `AuthProvider`, which is backed by a real server session: an
 * HttpOnly cookie this code cannot read, issued by `POST /auth/login` and
 * revoked by `POST /auth/logout`.
 *
 * ── What this used to say, and why it is gone ──────────────────────────
 * It described a Keycloak swap-in, with a six-step plan, alongside a
 * provider that did `void password`. Neither half was real: the plan was
 * never executed and the provider authenticated nobody. A note describing
 * work that is not happening reads, to a later session, as work that is
 * already designed.
 *
 * There is ONE user and there is no second one coming. Signup, password
 * reset, OAuth and roles are out of scope by decision, not by omission —
 * `roles` survives only because the shell already renders it as a label.
 */

/** The only provider kind. A server-issued session cookie. */
export type AuthProviderKind = "session";

export type User = {
  id: string;
  email: string;
  name: string;
  roles: readonly string[];
  provider: AuthProviderKind;
};

/**
 * `loading` is the BOOT state, not an error state. The session lives in a
 * cookie the browser holds and this code cannot read, so on every load
 * there is a window where the answer is genuinely unknown and the guard
 * must wait rather than redirect.
 */
export type AuthStatus = "loading" | "authenticated" | "unauthenticated";

export type AuthContextValue = {
  status: AuthStatus;
  user: User | null;
  isAuthenticated: boolean;
  provider: AuthProviderKind;
  /**
   * Throws on failure. The login screen catches it and shows one generic
   * message: the backend deliberately does not say which half was wrong.
   */
  signIn: (email: string, password: string) => Promise<void>;
  /** Revokes the session server-side and clears the cookie. */
  signOut: () => Promise<void>;
};

export const AuthContext = createContext<AuthContextValue | null>(null);
