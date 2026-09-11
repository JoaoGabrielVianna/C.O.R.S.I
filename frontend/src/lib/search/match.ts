/**
 * Text matching for every list a person filters by typing.
 *
 * ── Why this moved here ────────────────────────────────────────────────
 * It was a private helper inside CommandPalette. The composer's command
 * menu needs exactly the same behaviour — case-insensitive, accent-blind,
 * every token has to appear somewhere — and a second copy would have been
 * the kind of duplication that only shows up when someone fixes one of them.
 *
 * ── Why no fuzzy search ────────────────────────────────────────────────
 * A dependency, a scoring model to tune, and results a person cannot
 * predict, in exchange for tolerating typos in lists that are a handful of
 * items long. Substring matching over normalized text is what these lists
 * need, and it is explainable: what you typed is in there, or it is not.
 */

/** Lowercases and strips diacritics, so `memória` matches `memoria`. */
export function normalizeForSearch(s: string): string {
  return s
    .toLowerCase()
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "");
}

/**
 * True when every whitespace-separated token of `query` appears somewhere
 * in `haystacks`. An empty query matches everything, because a filter
 * nobody has typed into is not a filter.
 */
export function matchesQuery(haystacks: readonly string[], query: string): boolean {
  const tokens = query.split(/\s+/).filter(Boolean).map(normalizeForSearch);
  if (tokens.length === 0) return true;
  const blob = normalizeForSearch(haystacks.join(" "));
  return tokens.every((t) => blob.includes(t));
}
