import type { useT } from "@/lib/i18n";

/**
 * Identifier → label, for the four vocabularies the server owns.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A MISSING TRANSLATION RENDERS THE IDENTIFIER, NEVER NOTHING
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The lists come from `GET /closet/catalog`, so the frontend learns about a
 * new category the moment the backend ships one — which is the whole point
 * of a vocabulary that costs one line of Go to extend. The dictionaries in
 * `lib/i18n` are how each identifier is SPELLED, and they will always lag
 * by exactly one change.
 *
 * So the fallback matters: a rail button showing `hats` is a translation
 * task somebody will notice, and a blank rail button is a bug somebody will
 * report as a broken screen.
 *
 * ── Why this file is not inside a component ────────────────────────────
 * Because a module that exports both components and plain functions breaks
 * fast refresh — the lint rule that caught it is right, and four helpers
 * imported by five components have no business living in the rail.
 */

type T = ReturnType<typeof useT>;

function lookup(dictionary: Record<string, string>, key: string): string {
  return dictionary[key] ?? key;
}

export function categoryLabel(t: T, category: string): string {
  return lookup(t.app.modules.closet.categoryNames as Record<string, string>, category);
}

export function slotLabel(t: T, slot: string): string {
  return lookup(t.app.modules.closet.slotNames as Record<string, string>, slot);
}

export function viewLabel(t: T, view: string): string {
  return lookup(t.app.modules.closet.viewNames as Record<string, string>, view);
}

export function occasionLabel(t: T, occasion: string): string {
  return lookup(t.app.modules.closet.occasionNames as Record<string, string>, occasion);
}
