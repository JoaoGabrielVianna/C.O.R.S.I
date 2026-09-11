/**
 * Local snapshot store for archived cards.
 *
 * Backend v0.1's `GET /finance/cards` filters out soft-deleted rows and
 * has no `?include_archived` flag. Without a local shadow, archived
 * cards would disappear from the UI after reload — breaking the "Show
 * archived" toggle and the historical transactions that still need to
 * resolve a card label.
 *
 * The shadow is write-once on archive: we snapshot the card (with
 * `deletedAt` set) before the backend DELETE, persist to localStorage,
 * and the store merges these snapshots back into `state.creditCards` on
 * every render.
 *
 * TODO backend-v0.2: add `?include_archived=true` to /finance/cards and
 * delete this file.
 */

import type { CreditCard } from "@/pages/app/modules/finance/types";

const KEY = "corsi.finance.cards.archive.v1";

type Store = Record<string, CreditCard>;

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

export function listArchivedCards(): CreditCard[] {
  const s = read();
  return Object.values(s);
}

export function addArchivedCard(card: CreditCard): void {
  const s = read();
  s[card.id] = { ...card, deletedAt: card.deletedAt ?? Date.now() };
  write(s);
}

export function removeArchivedCard(id: string): void {
  const s = read();
  delete s[id];
  write(s);
}
