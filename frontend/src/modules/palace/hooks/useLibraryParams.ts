/**
 * The Library's state, kept in the URL.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE URL IS NAVIGATION STATE. IT IS NOT SEMANTIC STATE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Which tab, what was typed, which filters, which page. All of it survives
 * a refresh, a back button and a pasted link, because all of it is in the
 * address bar and nowhere else.
 *
 * ── Why not `useState`, and why not `localStorage` ─────────────────────
 * `useState` loses the view on reload and makes the back button do nothing
 * useful, which on a list-and-filter screen is the difference between a
 * tool and a toy. `localStorage` would be worse than useless: it would be
 * a SECOND place that remembers something about the operator's knowledge,
 * surviving in the browser, invisible to the backend, and diverging the
 * moment two tabs disagree. This version exists to have one source of
 * truth, and a persisted filter is a small one but it is still one.
 *
 * ── What the URL may never carry ───────────────────────────────────────
 * A sensitivity opt-in. There is no key for it here, and the API client
 * could not serialise it if there were: `Query` in `api/palace.ts` is a
 * closed union that does not contain it. A bookmark cannot smuggle one in,
 * because nothing reads an unknown key and passes it on.
 */

import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

import type {
  ArtifactKind,
  MemoryKind,
  PalaceStatus,
} from "../api/types";
import { UNFILED } from "../api/palace";

export type LibraryTab = "rooms" | "artifacts" | "memories";

export const LIBRARY_TABS: readonly LibraryTab[] = ["rooms", "artifacts", "memories"];

/** How many rows a page shows. The backend lowers anything above 100. */
export const PAGE_SIZE = 25;

const ARTIFACT_KINDS: readonly ArtifactKind[] = ["project", "list", "plan", "note"];
const MEMORY_KINDS: readonly MemoryKind[] = [
  "fact",
  "preference",
  "idea",
  "decision",
  "learning",
  "reflection",
];

export interface LibraryParams {
  tab: LibraryTab;
  /** The search box. Empty means no text filter. */
  q: string;
  /**
   * Lifecycle. `active` is the default and the URL carries it only when
   * the reader chose it, so a bare `/library` is the active space.
   */
  status: PalaceStatus;
  artifactKind: ArtifactKind | undefined;
  memoryKind: MemoryKind | undefined;
  /** A room id, the `none` sentinel, or undefined for every room. */
  room: string | undefined;
  /** A memory's subject. Only meaningful on the memories tab. */
  artifact: string | undefined;
  /** 0 means no floor. */
  minImportance: number;
  offset: number;
}

/**
 * Reads the URL into a typed shape, dropping anything it does not
 * recognise.
 *
 * ── Why unknown values are dropped rather than passed through ──────────
 * Because the URL is whatever a bookmark, a typo or somebody's curiosity
 * put there, and forwarding an unrecognised value to the API would make
 * the address bar an input to the backend. A `kind` that is not in the
 * closed vocabulary is not a filter: it is noise, and the honest response
 * is to ignore it and show the unfiltered view.
 */
export function useLibraryParams(): {
  params: LibraryParams;
  setParams: (next: Partial<LibraryParams>) => void;
} {
  const [search, setSearch] = useSearchParams();

  const params = useMemo<LibraryParams>(() => {
    const tab = search.get("tab");
    const status = search.get("status");
    const room = search.get("room")?.trim();
    const artifact = search.get("artifact")?.trim();

    return {
      tab: LIBRARY_TABS.includes(tab as LibraryTab) ? (tab as LibraryTab) : "rooms",
      q: search.get("q") ?? "",
      status: status === "archived" ? "archived" : "active",
      artifactKind: oneOf(search.get("kind"), ARTIFACT_KINDS),
      memoryKind: oneOf(search.get("kind"), MEMORY_KINDS),
      room: room ? room : undefined,
      artifact: artifact ? artifact : undefined,
      minImportance: clampImportance(search.get("importance")),
      offset: Math.max(0, Number.parseInt(search.get("offset") ?? "", 10) || 0),
    };
  }, [search]);

  const setParams = useCallback(
    (next: Partial<LibraryParams>) => {
      const merged = { ...params, ...next };
      const url = new URLSearchParams();

      // Only non-default values are written, so the common view has a
      // clean address and a shared link says what it means.
      if (merged.tab !== "rooms") url.set("tab", merged.tab);
      if (merged.q.trim()) url.set("q", merged.q.trim());
      if (merged.status !== "active") url.set("status", merged.status);

      const kind = merged.tab === "memories" ? merged.memoryKind : merged.artifactKind;
      if (kind && merged.tab !== "rooms") url.set("kind", kind);

      if (merged.room && merged.tab !== "rooms") url.set("room", merged.room);
      if (merged.artifact && merged.tab === "memories") url.set("artifact", merged.artifact);
      if (merged.minImportance > 0 && merged.tab === "memories") {
        url.set("importance", String(merged.minImportance));
      }
      if (merged.offset > 0) url.set("offset", String(merged.offset));

      setSearch(url, { replace: false });
    },
    [params, setSearch],
  );

  return { params, setParams };
}

/**
 * Any change to a filter sends the reader back to the first page.
 *
 * Staying on page four after narrowing a search is how somebody concludes
 * their Palace is empty: the rows exist, they are just all on page one.
 */
export function resetPage<T extends Partial<LibraryParams>>(next: T): T & { offset: number } {
  return { ...next, offset: 0 };
}

function oneOf<T extends string>(raw: string | null, allowed: readonly T[]): T | undefined {
  return raw && allowed.includes(raw as T) ? (raw as T) : undefined;
}

function clampImportance(raw: string | null): number {
  const parsed = Number.parseInt(raw ?? "", 10);
  if (!Number.isFinite(parsed) || parsed < 1) return 0;
  return Math.min(parsed, 5);
}

export { UNFILED };
