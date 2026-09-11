/**
 * Local-only metadata for persons — fields the backend doesn't model.
 *
 * - `role`: free-form display label (operator, partner, child, shared).
 *   The backend only stores `name` and `notes`.
 * - `active`: when false, the person is hidden from "default picker"
 *   logic but still appears in pickers and historical aggregations.
 * - `whatsappNumber`: reserved for the WhatsApp ingestion normalizer.
 *
 * Pattern mirrors `cardMetadata.ts` / `transactionMetadata.ts`. Stored
 * in localStorage so it survives reload. When the backend grows these
 * columns, delete this file and inline them into ApiPerson + the hooks.
 *
 * TODO backend-v0.3: promote role / active / whatsapp to wire fields.
 */

const KEY = "corsi.finance.persons.metadata.v1";

export interface PersonMetadata {
  role?: string;
  active?: boolean;
  whatsappNumber?: string;
}

type Store = Record<string, PersonMetadata>;

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

export function getPersonMetadata(id: string): PersonMetadata {
  return read()[id] ?? {};
}

export function setPersonMetadata(id: string, m: PersonMetadata): void {
  const s = read();
  s[id] = { ...s[id], ...m };
  write(s);
}

export function clearPersonMetadata(id: string): void {
  const s = read();
  delete s[id];
  write(s);
}
