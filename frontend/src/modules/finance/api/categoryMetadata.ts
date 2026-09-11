/**
 * Local-only metadata for categories — fields the backend doesn't model
 * in v0.1. Currently just `budget` (Cents, null = unset).
 *
 * Stored in localStorage so it survives reload. Cleared on
 * `deleteCategory`. When backend grows a `budget` column, delete this
 * file and inline the field into ApiCategory + the hooks layer.
 *
 * TODO backend-v0.2: move `budget` to the backend.
 */

const KEY = "corsi.finance.categories.metadata.v1";

export interface CategoryMetadata {
  budget?: number | null;
}

type Store = Record<string, CategoryMetadata>;

function read(): Store {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as Store) : {};
  } catch {
    return {};
  }
}

function write(s: Store): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
}

export function getCategoryMetadata(id: string): CategoryMetadata {
  return read()[id] ?? {};
}

export function setCategoryMetadata(id: string, m: CategoryMetadata): void {
  const s = read();
  s[id] = { ...s[id], ...m };
  write(s);
}

export function clearCategoryMetadata(id: string): void {
  const s = read();
  delete s[id];
  write(s);
}
