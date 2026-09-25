/**
 * Resolves a closet asset id to something an `<img>` can render.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   WHY THE URL IS NOT SIMPLY PUT IN `<img src>`
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Because `/closet/assets/{id}` sits behind the workspace middleware, like
 * every other route in the product, and a browser cannot attach
 * `X-Workspace-Id` to an `<img>` tag. There were three ways out and two of
 * them were worse:
 *
 *   - Exempt the asset route from the middleware. That would make the ONE
 *     address returning raw bytes the one address with weaker scoping,
 *     which is precisely backwards.
 *   - Put the workspace in the path. Same authority, different spelling,
 *     and now two ways to say which tenant a request is for.
 *   - Fetch the bytes and render a blob. Costs this file, and nothing else
 *     in the system has to bend.
 *
 * ── The caching this does NOT throw away ───────────────────────────────
 * `fetch` goes through the browser's HTTP cache. The server sends
 * `Cache-Control: private, max-age=31536000, immutable` and an ETag, and an
 * asset is immutable by construction — replacing a photograph creates a new
 * asset and repoints the row — so a revisited wardrobe re-reads from disk
 * rather than the network. The in-memory map below is a second, smaller
 * cache that also skips the blob decode.
 *
 * ── Why the cache is module-level and bounded ──────────────────────────
 * Module-level because the same piece appears in the selector, in the look
 * being built and on a gallery card, and three components mounting should
 * not be three downloads. Bounded because every live object URL pins its
 * blob in memory: an unbounded map browsing a large wardrobe is a leak that
 * only shows up on the machine of whoever owns the most clothes.
 */

import { useEffect, useState } from "react";

import { fetchAssetBlob } from "../api/closet";

/**
 * How many decoded images stay resident.
 *
 * A category page draws a few dozen; a look uses at most nine. This is
 * several screens' worth, which is what makes going back feel instant, and
 * it is bounded, which is what keeps it from growing for the whole session.
 */
const MAX_RESIDENT = 120;

/**
 * assetId → object URL. Insertion-ordered, which is what makes the eviction
 * below least-recently-INSERTED; a hit re-inserts, so it is effectively
 * least-recently-used.
 */
const resident = new Map<string, string>();

/** In-flight requests, so two components mounting cause one download. */
const inFlight = new Map<string, Promise<string>>();

function retain(assetId: string, url: string): void {
  resident.set(assetId, url);
  while (resident.size > MAX_RESIDENT) {
    const oldest = resident.keys().next();
    if (oldest.done) break;
    const evicted = resident.get(oldest.value);
    resident.delete(oldest.value);
    // Revoking is what actually frees the blob. Without it the Map shrinks
    // and the memory does not.
    if (evicted) URL.revokeObjectURL(evicted);
  }
}

async function load(assetId: string): Promise<string> {
  const cached = resident.get(assetId);
  if (cached) {
    // Re-insert so a piece being looked at is not the next one evicted.
    resident.delete(assetId);
    resident.set(assetId, cached);
    return cached;
  }
  const pending = inFlight.get(assetId);
  if (pending) return pending;

  const promise = fetchAssetBlob(assetId)
    .then((blob) => {
      const url = URL.createObjectURL(blob);
      retain(assetId, url);
      return url;
    })
    .finally(() => {
      inFlight.delete(assetId);
    });

  inFlight.set(assetId, promise);
  return promise;
}

export type AssetState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; url: string }
  | { status: "error" };

/** What a given asset id starts as, before anything is fetched. */
function initialState(assetId: string | undefined): AssetState {
  if (!assetId) return { status: "idle" };
  const cached = resident.get(assetId);
  return cached ? { status: "ready", url: cached } : { status: "loading" };
}

/**
 * Resolves one asset. Passing `undefined` is the normal case for a piece
 * with no photographs, and it settles as `idle` rather than as an error.
 *
 * ── Why the id change is handled during render and not in an effect ────
 * An effect that reset the state would render ONE frame showing the
 * previous asset under the new id — a grid re-sorting would flash the wrong
 * garment in every tile. Adjusting state during render is React's own
 * answer for "this state is derived from a prop that changed", and it is
 * why nothing below calls setState synchronously inside an effect.
 *
 * The object URL is deliberately NOT revoked on unmount: it belongs to the
 * module cache, and revoking it here would break the other components
 * rendering the same piece. Eviction owns its lifetime.
 */
export function useAssetObjectURL(assetId: string | undefined): AssetState {
  const [state, setState] = useState<AssetState>(() => initialState(assetId));
  const [resolvedFor, setResolvedFor] = useState(assetId);

  if (resolvedFor !== assetId) {
    setResolvedFor(assetId);
    setState(initialState(assetId));
  }

  useEffect(() => {
    if (!assetId || resident.has(assetId)) return;

    let live = true;
    load(assetId)
      .then((url) => {
        if (live) setState({ status: "ready", url });
      })
      .catch(() => {
        // A failed image is a failed image. It is shown as the piece's name
        // on a plain surface rather than as an error banner: one photograph
        // that did not load is not a broken closet.
        if (live) setState({ status: "error" });
      });
    return () => {
      live = false;
    };
  }, [assetId]);

  return state;
}

/**
 * Drops everything. Exported for tests, which would otherwise leak object
 * URLs between cases and see a cached image where they expected a fetch.
 */
export function __resetAssetCache(): void {
  for (const url of resident.values()) URL.revokeObjectURL(url);
  resident.clear();
  inFlight.clear();
}
