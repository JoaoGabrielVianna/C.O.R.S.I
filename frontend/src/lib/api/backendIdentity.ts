/**
 * Is the API behind this frontend actually C.O.R.S.I.?
 *
 * ── The afternoon this exists to give back ─────────────────────────────
 * The dev proxy targets `localhost:8080`. Another project's API was found
 * sitting there, answering `200 OK` on `/health/live` and a clean `404` on
 * every C.O.R.S.I. route beneath it. Nothing crashed. The Job Radar board
 * came up empty, the Agents list came up empty, and each screen reported
 * its own plausible little failure — which is the most expensive shape a
 * bug can take, because it sends you reading code that never ran.
 *
 * It is worse than "someone took the port", and the worse version is the
 * one that actually happened: `localhost` resolves to both `127.0.0.1` and
 * `::1`, so two products held :8080 at the same time, one per family,
 * neither failing to bind. Which one a request reached depended on the
 * resolver. A port is not an identity, so we ask the service instead.
 *
 * ── What this is not ───────────────────────────────────────────────────
 * Not authentication, and it must never be reached for as any. The value
 * it reads is an unauthenticated string that anything could copy; it
 * answers "am I pointed at the right service", never "is this service to
 * be trusted". It is a development ergonomic with a security-shaped
 * silhouette, and the difference matters enough to say twice.
 */

/** The identity the backend reports. Must match `health.Application`. */
export const EXPECTED_APPLICATION = "corsi";

export type BackendIdentity =
  /** It answered and it is us. Nothing to do. */
  | { kind: "ok" }
  /** It answered and it is something else. This is the loud one. */
  | { kind: "mismatch"; received: string; target: string }
  /**
   * Nothing answered.
   *
   * Deliberately NOT an error state. The overwhelmingly common cause is a
   * backend that has not been started yet, and a frontend that refused to
   * render until the API was up would be a worse tool than one that lets
   * you look at the UI while `make dev` finishes compiling. The app boots
   * and its own request failures say what they always said.
   */
  | { kind: "unreachable"; target: string };

/**
 * Where the guard looks, for the message. Empty base means same origin.
 *
 * The `window` access is guarded rather than assumed so this module stays
 * importable outside a browser — a test runner in node, and eventually any
 * tooling that wants to reuse the same rule. A module that throws on import
 * in the wrong environment is a module people copy instead of reuse.
 */
export function identityTarget(apiBase: string): string {
  if (apiBase) return `${apiBase}/health/live`;
  const origin = typeof window === "undefined" ? "" : window.location.origin;
  return `${origin}/health/live`;
}

/**
 * Asks the backend who it is.
 *
 * `/health/live` and not `/health/ready`: liveness needs no database, so a
 * C.O.R.S.I. whose Postgres is down still identifies itself instead of
 * being mistaken for a foreign API — which is exactly the moment somebody
 * is already confused enough.
 *
 * The timeout is short because this sits in front of first paint. A slow
 * answer resolves as `unreachable`, which lets the app render.
 */
export async function checkBackendIdentity(
  apiBase: string,
  timeoutMs = 2500,
): Promise<BackendIdentity> {
  const target = identityTarget(apiBase);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(`${apiBase}/health/live`, { signal: controller.signal });
    const text = await res.text();
    let application = "";
    try {
      application = (JSON.parse(text) as { application?: string }).application ?? "";
    } catch {
      // A non-JSON body is emphatically not us. The most likely author is
      // the Vite SPA fallback returning index.html because no proxy rule
      // matched — which is its own bug and deserves the same loud
      // treatment, not a shrug.
      application = "";
    }
    if (application === EXPECTED_APPLICATION) return { kind: "ok" };
    return { kind: "mismatch", received: application || "unidentified", target };
  } catch {
    return { kind: "unreachable", target };
  } finally {
    clearTimeout(timer);
  }
}
