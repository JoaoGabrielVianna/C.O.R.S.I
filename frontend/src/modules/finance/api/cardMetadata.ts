/**
 * Local-only metadata for cards — fields the backend doesn't model in v0.1.
 *
 * - `ownerId`: who pays the bill (FK to Person, which doesn't exist on the
 *   backend yet). Required by frontend UI.
 * - `color`: Tailwind token used by `<CardPreview />` for the gradient
 *   surface. Pure display.
 *
 * Stored in localStorage so it survives reload. Cleared on archive. When
 * backend grows owner/color columns, delete this file and inline the
 * fields into ApiCard + the hooks layer.
 *
 * TODO backend-v0.2: move `ownerId` and `color` to the backend.
 */

const KEY = "corsi.finance.cards.metadata.v1";

export interface CardMetadata {
  ownerId?: string;
  color?: string;
}

type Store = Record<string, CardMetadata>;

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

export function getCardMetadata(id: string): CardMetadata {
  return read()[id] ?? {};
}

export function setCardMetadata(id: string, m: CardMetadata): void {
  const s = read();
  s[id] = { ...s[id], ...m };
  write(s);
}

export function clearCardMetadata(id: string): void {
  const s = read();
  delete s[id];
  write(s);
}
