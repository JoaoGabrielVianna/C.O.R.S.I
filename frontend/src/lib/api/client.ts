/**
 * HTTP client for the C.O.R.S.I backend.
 *
 * One fetch wrapper for the whole frontend: applies the X-Workspace-Id
 * header, JSON-encodes bodies, enforces a request timeout, and converts
 * non-2xx responses to a typed `ApiError` so TanStack Query's error
 * handling sees a consistent shape.
 *
 * Base URL is env-driven via `VITE_API_URL`:
 *   - empty (default) → relative paths → Vite dev proxy locally, same-origin
 *     in any reverse-proxied production setup.
 *   - absolute (e.g. https://api.corsi.dev) → cross-origin requests when
 *     the frontend and backend are deployed on different hosts.
 * The value is baked into the bundle at `npm run build` time; rebuild the
 * image to change it.
 */

import { getApiWorkspaceId } from "./workspace";

/**
 * Exported because `apiFetch` is not the only caller: the chat stream
 * reader (`@/modules/agents/api/stream`) drives `fetch` directly so it can
 * consume the response body incrementally, and it must resolve the same
 * base URL rather than keep a second copy of this rule.
 */
export const API_BASE = import.meta.env.VITE_API_URL?.trim() ?? "";

/** Mirrors the backend's `apierror` wire shape. */
export interface ApiErrorBody {
  error: { code: string; message: string };
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, body: ApiErrorBody | null, fallback: string) {
    super(body?.error.message ?? fallback);
    this.status = status;
    this.code = body?.error.code ?? "unknown";
    this.name = "ApiError";
  }
}

/** Default per-request timeout. Hangs above this are aborted client-side. */
const DEFAULT_TIMEOUT_MS = 15_000;

export interface ApiFetchInit extends RequestInit {
  /** Override the default timeout in ms. Pass `0` to disable. */
  timeoutMs?: number;
}

export async function apiFetch<T>(
  path: string,
  init: ApiFetchInit = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Workspace-Id", getApiWorkspaceId());
  if (init.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  // Compose the abort signal: caller signal + timeout.
  const timeoutMs = init.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  let timeoutController: AbortController | undefined;
  let timeoutId: ReturnType<typeof setTimeout> | undefined;
  if (timeoutMs > 0) {
    timeoutController = new AbortController();
    timeoutId = setTimeout(() => timeoutController!.abort(), timeoutMs);
  }
  const signal = mergeSignals(init.signal ?? null, timeoutController?.signal ?? null);

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, { ...init, headers, signal });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      // Distinguish caller cancellation from timeout. If the caller's own
      // signal aborted, propagate as-is; otherwise it was our timeout.
      if (init.signal?.aborted) throw err;
      throw new ApiError(0, null, `request timed out after ${timeoutMs}ms`);
    }
    throw err;
  } finally {
    if (timeoutId) clearTimeout(timeoutId);
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      // Still an error, and emphatically so: a body this layer cannot read
      // is never a success, whatever it contains. HTML from a proxy
      // fallback, a plain-text 404, a gateway's own error page — none of
      // them are data, and accepting any of them would hand callers a
      // `null` they would go on to read fields off.
      //
      // What changed is what it SAYS. It used to report only
      // "non-JSON response from server", which threw away the single most
      // diagnostic fact in the response: the status. A 404 from an API that
      // does not have the route, a 502 from a dead upstream and an HTML
      // page from a missing proxy entry all read identically, and none of
      // them told the reader anything to act on.
      throw new ApiError(res.status, null, describeUnreadableBody(res, text));
    }
  }

  if (!res.ok) {
    const body =
      data && typeof data === "object" && data !== null && "error" in data
        ? (data as ApiErrorBody)
        : null;
    throw new ApiError(res.status, body, res.statusText);
  }
  return data as T;
}

/**
 * What to say about a response body this layer could not read.
 *
 * Three facts, in the order a person needs them: the status, what the body
 * looked like, and — when it is the one case with a known cause — what to
 * do about it.
 *
 * ── Why HTML gets a sentence of its own ────────────────────────────────
 * Because it has a single overwhelmingly likely cause and a single fix. A
 * request that leaves the app expecting JSON and comes back with a page is
 * a request that never reached the API: in development, a prefix missing
 * from the Vite proxy so the SPA fallback answered it. That happened to
 * Releases once and it cost hours, because the message said nothing.
 *
 * Nothing here inspects the body beyond that, and nothing here is quoted
 * back: a failing upstream can put anything in a body, and an error banner
 * is not a place to render it.
 */
function describeUnreadableBody(res: Response, text: string): string {
  const contentType = res.headers.get("content-type") ?? "";
  const looksLikeHTML =
    contentType.includes("text/html") || text.trimStart().startsWith("<");

  if (looksLikeHTML) {
    return (
      `the server answered ${res.status} with an HTML page where JSON was expected — ` +
      `the request probably never reached the API`
    );
  }
  return `the server answered ${res.status} with a body that is not JSON`;
}

export interface PageEnvelope<T> {
  items: T[];
  limit: number;
  offset: number;
}

/**
 * Merge two AbortSignals: the returned signal aborts if either source
 * aborts. We avoid `AbortSignal.any` (Safari 17.4+) for broader support.
 */
function mergeSignals(a: AbortSignal | null, b: AbortSignal | null): AbortSignal | undefined {
  if (!a) return b ?? undefined;
  if (!b) return a;
  const merged = new AbortController();
  const onAbort = (src: AbortSignal) => () => merged.abort(src.reason);
  if (a.aborted) merged.abort(a.reason);
  else a.addEventListener("abort", onAbort(a), { once: true });
  if (b.aborted) merged.abort(b.reason);
  else b.addEventListener("abort", onAbort(b), { once: true });
  return merged.signal;
}
