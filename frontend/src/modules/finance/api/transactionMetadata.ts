/**
 * Local-only metadata for **backend transactions** — fields the v0.2
 * backend doesn't model yet.
 *
 * The backend covers id, type, category_id, amount_cents, description,
 * occurred_at, status, payment_method, source, notes, account_id (UUID),
 * person_id (UUID — but unused; see note below).
 *
 * Sidecar covers:
 *   - `personId`: frontend Person ids are `p_<...>` strings, not UUIDs,
 *     so they can't go into `person_id`. Keep them local until the
 *     Persons aggregate lands in backend v0.3.
 *   - `splitAmong`: list of person ids the cost is divided across.
 *   - `accountId`: when the original FE accountId points at a legacy
 *     (non-UUID) card from the pre-Phase-2 seed. Backend-issued card
 *     UUIDs are sent to `account_id` directly and never appear here.
 *
 * Transfers, planId, installmentNumber NEVER reach a backend transaction
 * — those rows live entirely in localStorage on the FinanceStore.
 *
 * TODO backend-v0.3: move persons + accounts to the backend; delete
 * this sidecar.
 */

const KEY = "corsi.finance.transactions.metadata.v1";

export interface TransactionMetadata {
  personId?: string;
  splitAmong?: string[];
  accountId?: string;
}

type Store = Record<string, TransactionMetadata>;

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

export function getTransactionMetadata(id: string): TransactionMetadata {
  return read()[id] ?? {};
}

export function setTransactionMetadata(id: string, m: TransactionMetadata): void {
  const s = read();
  s[id] = { ...s[id], ...m };
  write(s);
}

export function clearTransactionMetadata(id: string): void {
  const s = read();
  delete s[id];
  write(s);
}
