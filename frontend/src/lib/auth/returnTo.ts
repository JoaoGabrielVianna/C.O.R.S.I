/**
 * Where to send someone after they log in.
 *
 * ── The bug this exists to close ───────────────────────────────────────
 * `RequireAuth` used to hand the login page `location.pathname` and the
 * login page ignored it, always landing on `/app`. Both halves were
 * harmless until an OAuth callback arrived: Meta redirects back to
 * `/app/settings/integrations?code=…&state=…`, and if the session had
 * lapsed the guard threw away the query string on the way to `/login` and
 * the code was gone. The operator would see a login form, then an
 * Integrations page that still said "not connected", with nothing
 * anywhere explaining why.
 *
 * So the guard now captures pathname AND search, and the login page reads
 * it back.
 *
 * ── Why the value is validated rather than trusted ─────────────────────
 * It reaches the login page through router state, which a crafted link can
 * populate. Passing it to `navigate()` unchecked would make this an open
 * redirect: `//evil.example` is a PROTOCOL-RELATIVE URL, and both the
 * router and the browser would treat it as another origin.
 *
 * The rule is therefore allow-list shaped, not deny-list shaped: one
 * leading slash, no second slash, no backslash, no scheme. Anything else
 * falls back to the default route — a login that lands somewhere safe is a
 * small annoyance, and one that lands on an attacker's page is a
 * credential harvest.
 */

/** Where a login with no remembered destination goes. */
export const DEFAULT_AFTER_LOGIN = "/app";

/**
 * Narrows an untrusted `from` to an internal path, or refuses it.
 *
 * The authorization code, when there is one, is carried in the query
 * string and nowhere else: it is never copied into storage, never logged,
 * and lives only as long as the navigation that restores it.
 */
export function safeReturnTo(from: unknown): string {
  if (typeof from !== "string" || from === "") return DEFAULT_AFTER_LOGIN;
  // Must be an absolute path on this origin.
  if (!from.startsWith("/")) return DEFAULT_AFTER_LOGIN;
  // `//host` and `/\host` are read as another origin by the browser.
  if (from.startsWith("//") || from.startsWith("/\\")) return DEFAULT_AFTER_LOGIN;
  // A control character would let the value be smuggled past a log or a
  // header. Checked by code point rather than by regex: a regex spelling
  // this needs the control characters written INTO the pattern, which is
  // what `no-control-regex` exists to stop, and suppressing that rule here
  // would hide the next one somebody adds for real.
  for (let i = 0; i < from.length; i++) {
    const code = from.charCodeAt(i);
    if (code < 0x20 || code === 0x7f) return DEFAULT_AFTER_LOGIN;
  }
  return from;
}
