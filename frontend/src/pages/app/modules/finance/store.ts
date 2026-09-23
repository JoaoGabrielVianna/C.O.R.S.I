import { useCallback, useEffect, useMemo, useState } from "react";
import {
  EMPTY_FILTERS,
  type Account,
  type Category,
  type CreditCard,
  type FinanceFilters,
  type FinanceState,
  type RecurringEntry,
  type Person,
  type PurchasePlan,
  type Transaction,
} from "./types";
import { buildSeed, SEED_FLAG_KEY } from "./seed";
import { useCategories } from "@/modules/finance/hooks/useCategories";
import { useCards } from "@/modules/finance/hooks/useCards";
import { listArchivedCards } from "@/modules/finance/api/cardArchive";
import { useTransactions } from "@/modules/finance/hooks/useTransactions";
import { usePurchasePlans } from "@/modules/finance/hooks/usePurchasePlans";
import { usePersons } from "@/modules/finance/hooks/usePersons";
import {
  useCreateRecurringEntry,
  useDeleteRecurringEntry,
  useRecurringEntries,
  useUpdateRecurringEntry,
} from "@/modules/finance/hooks/useRecurringEntries";

/**
 * Finance store — partly backend-backed, partly localStorage.
 *
 * Categories and Cards: served by TanStack Query
 * against `/finance/categories` and `/finance/cards`. The `state.*` and
 * `*ById` selectors here are a thin compatibility facade for read-only
 * consumers that haven't been migrated yet. Mutations live in the
 * `useCreateCategory` / `useUpdateCategory` / `useDeleteCategory` and
 * `useCreateCard` / `useUpdateCard` / `useArchiveCard` hooks under
 * `@/modules/finance/hooks/`.
 *
 * Cards: backend v0.1's `GET /finance/cards` filters out archived rows
 * and has no `?include_archived` flag, so a local snapshot store
 * (`cardArchive.v1`) shadows archived cards. `state.creditCards` is
 * merged from `useCards().data` ∪ `listArchivedCards()`. Drop the
 * shadow once backend v0.2 grows the param.
 *
 * Everything else (people, transactions, fixed expenses, accounts,
 * purchase plans) still lives in localStorage and will migrate in
 * later phases. TODO backend-v0.2.
 *
 * localStorage persistence key: `corsi.module.finance.v0`. The
 * `categories` and `creditCards` arrays inside that blob are read-only
 * fallbacks for label resolution on pre-Phase-2 seeded transactions.
 * Seed flag at `corsi.module.finance.seed.v1`.
 */

const STORAGE_KEY = "corsi.module.finance.v0";

/**
 * Opt-in switch for the demo fixtures.
 *
 * ── Why the fixtures stopped being automatic ───────────────────────────
 * They used to build themselves on the first visit whenever
 * `import.meta.env.DEV` was true and no blob existed yet — which is every
 * fresh browser profile a developer opens. Twenty-four transactions, eight
 * fixed expenses and two people appeared in `localStorage` and were merged
 * into the same arrays the screens aggregate, beside the real rows from
 * Postgres.
 *
 * Nothing was written to the backend, so it was invisible from the API
 * side, and it looked exactly like data. The clearest symptom was the
 * Categories tab: a non-zero monthly total over rows that all read "0 tx",
 * because the fixtures' `categoryId` values are `c_food`-style strings and
 * the real categories are UUIDs, so the money landed under keys nothing
 * rendered. A person reading that screen had no way to tell which figure
 * was theirs.
 *
 * ── Why a flag rather than deleting the fixtures ───────────────────────
 * They are genuinely useful for working on the screens without inventing a
 * month of data by hand. What was wrong was that they were the DEFAULT.
 * So they stay, behind a switch that a person has to set on purpose:
 *
 *     localStorage.setItem("corsi.module.finance.demo", "1")   // then reload
 *
 * Off is the zero value, off is what a fresh profile gets, and off is the
 * only possible state outside development — the DEV check remains, so a
 * production build cannot reach the fixtures even with the key set.
 */
export const DEMO_FIXTURES_KEY = "corsi.module.finance.demo";

function demoFixturesEnabled(): boolean {
  // Two conditions, and the build one is not redundant: the flag lives in
  // storage a user can edit, and "the operator typed a key into their own
  // console" must never be a way to get fake money into a production
  // screen.
  if (!import.meta.env.DEV) return false;
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(DEMO_FIXTURES_KEY) === "1";
  } catch {
    return false;
  }
}

// Categories are backend-served — never stored locally. We keep the field
// in the shape for compat with `FinanceState` typing, populated at runtime
// from the React Query cache (see `useFinance` below).
const INITIAL_STATE: FinanceState = {
  people: [],
  categories: [],
  transactions: [],
  creditCards: [],
  recurringEntries: [],
  accounts: [],
  purchasePlans: [],
  filters: EMPTY_FILTERS,
};

function makeId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`;
}

function readStored(): FinanceState {
  if (typeof window === "undefined") return INITIAL_STATE;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const seeded = window.localStorage.getItem(SEED_FLAG_KEY) === "1";

    if (!raw) {
      // No stored blob. The fixtures are built ONLY when someone asked for
      // them; the default for a fresh profile is an empty local store and
      // whatever the backend returns.
      if (seeded || !demoFixturesEnabled()) return INITIAL_STATE;
      const seed = buildSeed(Date.now());
      try {
        window.localStorage.setItem(SEED_FLAG_KEY, "1");
      } catch {
        /* ignore */
      }
      return { ...INITIAL_STATE, ...seed };
    }

    const parsed = JSON.parse(raw) as Partial<FinanceState>;
    const people = parsed.people ?? [];
    const fallbackOwnerId = people[0]?.id ?? "";
    const creditCards = (parsed.creditCards ?? []).map((c) => ({
      ...c,
      ownerId: c.ownerId ?? fallbackOwnerId,
    }));
    const accounts = parsed.accounts ?? [];
    let transactions = parsed.transactions ?? [];
    let purchasePlans = parsed.purchasePlans ?? [];

    const migration = migrateLegacy({
      transactions,
      accounts,
      purchasePlans,
      creditCards,
      fallbackOwnerId,
    });
    transactions = migration.transactions;
    purchasePlans = migration.purchasePlans;

    return {
      people,
      // Categories and creditCards live in the backend now; both fields on
      // FinanceState are hydrated at runtime from React Query (+ a local
      // archive snapshot for cards) inside useFinance(). The pre-Phase-2
      // arrays in localStorage stay there as read-only fallbacks for
      // `categoriesById` / `cardsById` so seeded transactions still resolve.
      categories:    [],
      transactions,
      creditCards:   [],
      // Backfill `startsAt` on fixed expenses stored before the history model.
      // Legacy entries get `startsAt: 0` — treated as "always been active".
      recurringEntries: (parsed.recurringEntries ?? []).map((fx) => ({
        ...fx,
        startsAt: fx.startsAt ?? 0,
      })),
      accounts:      migration.accounts,
      purchasePlans,
      filters:       { ...EMPTY_FILTERS, ...(parsed.filters ?? {}) },
    };
  } catch {
    return INITIAL_STATE;
  }
}

/**
 * One-shot read-side migration for data written by older versions:
 *   · transfer transactions with `fromAccount`/`toAccount` strings get
 *     synthesized bank `Account` rows and `fromAccountId`/`toAccountId`
 *     references.
 *   · installment transactions sharing a `purchaseId` get a synthesized
 *     `PurchasePlan` parent and `planId`/`installmentNumber` refs.
 *
 * Idempotent: rows already carrying the new ids are passed through.
 */
function migrateLegacy(input: {
  transactions: Transaction[];
  accounts: Account[];
  purchasePlans: PurchasePlan[];
  creditCards: CreditCard[];
  fallbackOwnerId: string;
}) {
  const accounts = [...input.accounts];
  const purchasePlans = [...input.purchasePlans];
  const cardNameToId = new Map<string, string>();
  for (const c of input.creditCards) cardNameToId.set(c.name.toLowerCase().trim(), c.id);
  const bankNameToId = new Map<string, string>();
  for (const a of accounts) {
    if (a.type === "bank") bankNameToId.set(a.name.toLowerCase().trim(), a.id);
  }

  const resolveAccount = (label: string): string => {
    const key = label.toLowerCase().trim();
    if (cardNameToId.has(key)) return cardNameToId.get(key)!;
    if (bankNameToId.has(key)) return bankNameToId.get(key)!;
    const id = `acc_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`;
    accounts.push({ id, name: label, ownerId: input.fallbackOwnerId, type: "bank" });
    bankNameToId.set(key, id);
    return id;
  };

  const transactions = input.transactions.map((tx) => {
    let next: Transaction = tx;
    if (tx.type === "transfer") {
      if (!next.fromAccountId && tx.fromAccount) {
        next = { ...next, fromAccountId: resolveAccount(tx.fromAccount) };
      }
      if (!next.toAccountId && tx.toAccount) {
        next = { ...next, toAccountId: resolveAccount(tx.toAccount) };
      }
    }
    return next;
  });

  const haveByPurchase = new Map<string, string>();
  for (const p of purchasePlans) {
    const matches = transactions.filter((t) => t.planId === p.id);
    if (matches[0]?.purchaseId) haveByPurchase.set(matches[0].purchaseId, p.id);
  }

  const groups = new Map<string, Transaction[]>();
  for (const tx of transactions) {
    if (tx.planId || !tx.purchaseId || !tx.installment) continue;
    const arr = groups.get(tx.purchaseId) ?? [];
    arr.push(tx);
    groups.set(tx.purchaseId, arr);
  }

  for (const [purchaseId, rows] of groups) {
    if (haveByPurchase.has(purchaseId)) continue;
    const sample = rows.find((r) => r.installment) ?? rows[0];
    const totalInstallments = Math.max(...rows.map((r) => r.installment?.total ?? 1));
    const originalAmount = rows.reduce((sum, r) => sum + r.amount, 0);
    const perInstallment = Math.round(originalAmount / totalInstallments);
    const purchasedAt = Math.min(...rows.map((r) => r.date));
    const accountId = sample.accountId ?? "";
    if (!accountId) continue; // can't synthesize a plan with no card binding
    const planId = `plan_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
    purchasePlans.push({
      id: planId,
      description: sample.description || "—",
      originalAmount,
      totalInstallments,
      perInstallment,
      accountId,
      personId: sample.personId,
      categoryId: sample.categoryId ?? "",
      purchasedAt,
      status: "active",
      notes: "",
      createdAt: purchasedAt,
      updatedAt: purchasedAt,
    });
    for (const r of rows) {
      const idx = transactions.indexOf(r);
      if (idx >= 0) {
        transactions[idx] = {
          ...r,
          planId,
          installmentNumber: r.installment?.current,
        };
      }
    }
  }

  return { transactions, accounts, purchasePlans };
}

// Read-only fallbacks — keep pre-Phase-2 seeded data resolvable so
// transactions on Overview / Transactions / etc still find a label
// after Categories and Cards moved to the backend.
function readLegacyCategories(): Category[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as Partial<FinanceState>;
    return Array.isArray(parsed.categories) ? parsed.categories : [];
  } catch {
    return [];
  }
}

function readLegacyCreditCards(): CreditCard[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as Partial<FinanceState>;
    return Array.isArray(parsed.creditCards) ? parsed.creditCards : [];
  } catch {
    return [];
  }
}

function writeStored(state: FinanceState) {
  if (typeof window === "undefined") return;
  try {
    // Categories and creditCards are backend-owned now, but the legacy
    // arrays already in localStorage are read-only fallbacks that keep
    // seeded transactions resolvable. Preserve them byte-for-byte instead
    // of overwriting with [] on every render tick.
    const existingRaw = window.localStorage.getItem(STORAGE_KEY);
    const existingParsed = existingRaw
      ? (JSON.parse(existingRaw) as Partial<FinanceState>)
      : null;
    const existingCategories: Category[] = Array.isArray(existingParsed?.categories)
      ? existingParsed!.categories
      : [];
    const existingCreditCards: CreditCard[] = Array.isArray(existingParsed?.creditCards)
      ? existingParsed!.creditCards
      : [];
    /* eslint-disable @typescript-eslint/no-unused-vars */
    const { categories: _dropCats, creditCards: _dropCards, ...persistable } = state;
    /* eslint-enable @typescript-eslint/no-unused-vars */
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        ...persistable,
        categories: existingCategories,
        creditCards: existingCreditCards,
      }),
    );
  } catch {
    /* ignore */
  }
}

export type NewTransactionInput = Omit<Transaction, "id" | "createdAt" | "updatedAt">;

export function useFinance() {
  const [localState, setState] = useState<FinanceState>(() => readStored());

  useEffect(() => {
    writeStored(localState);
  }, [localState]);

  // Backend-served slices merged into the legacy FinanceState shape so
  // existing read-only consumers keep working.
  const categoriesQuery = useCategories();
  const cardsQuery = useCards();
  const txQuery = useTransactions();
  const plansQuery = usePurchasePlans();
  const personsQuery = usePersons();
  // Recurring entries moved to Postgres. They used to be the last operational
  // financial state that existed only in this browser — the screens could
  // project a monthly figure the agent had no way to read.
  const recurringQuery = useRecurringEntries();
  const state: FinanceState = useMemo(() => {
    // Cards: live (backend) + archived snapshots (local). De-duplicate
    // by id so a card in flight doesn't show twice.
    const archived = listArchivedCards();
    const liveCards = cardsQuery.data ?? [];
    const liveCardIds = new Set(liveCards.map((c) => c.id));
    const mergedCards: CreditCard[] = [
      ...liveCards,
      ...archived.filter((c) => !liveCardIds.has(c.id)),
    ];
    // ── Transactions: the backend, and only the backend ─────────────
    //
    // This array used to be `backend ∪ leftover local rows`. That union is
    // what made two different answers possible on one screen: the KPI tiles
    // read `GET /finance/transactions/totals`, which can only see Postgres,
    // while every chart, table and per-category rollup aggregates THIS
    // array — so anything sitting in `localStorage` moved the charts and
    // not the tiles, with nothing saying which number to believe. It is
    // also the array `finance.summary.get` can never see, which is the same
    // divergence viewed from Ledger's side.
    //
    // A transaction is money. There is one record of it, and it is the one
    // both the screens and the agent read.
    //
    // Local rows are NOT deleted — see `localOnlyTransactions` below and
    // the note in useFinance's return. They are simply not money until
    // something puts them in the ledger.
    const liveTx = [...(txQuery.data ?? [])]
      .sort((a, b) => b.date - a.date || b.createdAt - a.createdAt);
    // PurchasePlans + People: backend is the single source of truth.
    // Legacy local plans synthesized by `migrateLegacy` and legacy
    // seeded persons are intentionally dropped here so the dashboard
    // cannot show ghost rows.
    return {
      ...localState,
      categories:    categoriesQuery.data ?? [],
      creditCards:   mergedCards,
      transactions:  liveTx,
      purchasePlans: plansQuery.data ?? [],
      people:        personsQuery.data ?? [],
      // Backend-owned, like the rest. Whatever is still in the local blob
      // is a leftover and is deliberately not merged — same rule as
      // transactions.
      recurringEntries: recurringQuery.data ?? [],
    };
  }, [localState, categoriesQuery.data, cardsQuery.data, txQuery.data, plansQuery.data,
      personsQuery.data, recurringQuery.data]);

  /* ── Transactions ─────────────────────────────────────────────── */
  const addTransaction = useCallback((input: NewTransactionInput): string => {
    const now = Date.now();
    const tx: Transaction = { ...input, id: makeId("tx"), createdAt: now, updatedAt: now };
    setState((prev) => ({ ...prev, transactions: [tx, ...prev.transactions] }));
    return tx.id;
  }, []);

  const updateTransaction = useCallback((id: string, patch: Partial<Omit<Transaction, "id" | "createdAt">>) => {
    setState((prev) => ({
      ...prev,
      transactions: prev.transactions.map((t) =>
        t.id === id ? { ...t, ...patch, updatedAt: Date.now() } : t,
      ),
    }));
  }, []);

  const removeTransaction = useCallback((id: string) => {
    setState((prev) => ({ ...prev, transactions: prev.transactions.filter((t) => t.id !== id) }));
  }, []);

  /* ── People ──────────────────────────────────────────────────── */
  // People are backend-owned. Mutate through `useCreatePerson`,
  // `useUpdatePerson`, `useDeletePerson` from
  // `@/modules/finance/hooks/usePersons` directly. Read paths still
  // resolve through `state.people` / `peopleById`, hydrated from
  // React Query above.

  /* ── Categories ──────────────────────────────────────────────── */
  // Removed from the store — categories are backend-owned.
  // Use `useCreateCategory`, `useUpdateCategory`, `useDeleteCategory`
  // from `@/modules/finance/hooks/useCategories` directly. Read paths
  // continue to use `state.categories` / `categoriesById` below, which
  // are hydrated from React Query.

  /* ── Credit cards ────────────────────────────────────────────── */
  // Removed — cards are backend-owned. Use `useCreateCard`,
  // `useUpdateCard`, `useArchiveCard` from
  // `@/modules/finance/hooks/useCards` directly. Read paths still go
  // through `state.creditCards` / `cardsById` / `activeCreditCards` /
  // `allAccountsById` below, which are now hydrated from React Query
  // plus the local archive snapshot.

  /* ── Recurring entries ───────────────────────────────────────── */
  //
  // Backend-owned since 2026-08-26. These four wrappers keep the signatures
  // the screens already call and route them at the API, so the components
  // did not have to be rewritten to move the entity to Postgres.
  //
  // `endRecurringEntry` is a CANCELLATION and not a delete: it stamps an end
  // date and the row stays, so "what was I paying in March" keeps a true
  // answer. `removeRecurringEntry` is for a record that should never have
  // existed. The domain makes the same distinction — see
  // backend/internal/finance/app/fixedexpenses.go.
  const createRecurringMut = useCreateRecurringEntry();
  const updateRecurringMut = useUpdateRecurringEntry();
  const deleteRecurringMut = useDeleteRecurringEntry();

  // ── Why these four return a Promise and no longer fire-and-forget ──
  //
  // Because `mutate()` reports failure nowhere a person can see. F8 proved
  // the exact shape: the backend answered 400, the dialog closed, the
  // operator was shown nothing, and the database kept the old values — a UI
  // that VISUALLY IMPLIED SUCCESS while persistence had failed. That is
  // worse than an error, because the operator walks away believing the
  // record moved.
  //
  // `mutateAsync` hands the rejection to the caller, so the form can stay
  // open, say what happened and keep what was typed. The callers are the
  // only place that knows what a failure should look like on screen; the
  // store's job is to stop hiding it.
  const addRecurringEntry = useCallback((input: Omit<RecurringEntry, "id">) => {
    return createRecurringMut.mutateAsync({
      description: input.description,
      amount: input.amount,
      categoryId: input.categoryId,
      personId: input.personId,
      dueDay: input.dueDay,
      recurrence: input.recurrence,
      dueMonth: input.dueMonth,
      amountVaries: input.amountVaries,
      notes: input.notes,
    });
  }, [createRecurringMut]);

  // `applyToPeriod` is a separate argument rather than part of the patch
  // because it is not a property of the recurrence: it is an instruction
  // about ONE month, and the backend refuses it for anything but the
  // current, still-pending one.
  const updateRecurringEntry = useCallback((
    id: string,
    patch: Partial<Omit<RecurringEntry, "id">>,
    applyToPeriod?: string,
  ) => {
    return updateRecurringMut.mutateAsync({
      id,
      patch: {
        description: patch.description,
        amount_cents: patch.amount,
        category_id: patch.categoryId,
        due_day: patch.dueDay,
        recurrence: patch.recurrence,
        // An entry that is (or becomes) monthly must carry no due month,
        // and the domain refuses one that does. Cleared explicitly so
        // "omitted" can keep meaning "unchanged".
        due_month: patch.recurrence === "annual" ? patch.dueMonth : undefined,
        clear_due_month: patch.recurrence === "monthly" ? true : undefined,
        amount_varies: patch.amountVaries,
        status: patch.status,
        notes: patch.notes,
        apply_to_period: applyToPeriod,
      },
    });
  }, [updateRecurringMut]);

  const endRecurringEntry = useCallback((id: string, when: number = Date.now()) => {
    return updateRecurringMut.mutateAsync({
      id,
      patch: { ends_at: new Date(when).toISOString(), status: "paused" },
    });
  }, [updateRecurringMut]);

  const removeRecurringEntry = useCallback((id: string) => {
    return deleteRecurringMut.mutateAsync(id);
  }, [deleteRecurringMut]);

  /* ── Accounts (bank-only; card accounts are derived) ─────────── */
  const addAccount = useCallback((input: Omit<Account, "id">) => {
    const a: Account = { ...input, id: makeId("acc") };
    setState((prev) => ({ ...prev, accounts: [...prev.accounts, a] }));
    return a.id;
  }, []);

  const updateAccount = useCallback((id: string, patch: Partial<Omit<Account, "id">>) => {
    setState((prev) => ({
      ...prev,
      accounts: prev.accounts.map((a) => (a.id === id ? { ...a, ...patch } : a)),
    }));
  }, []);

  /** Soft-delete. Mirrors `removeCreditCard` semantics. */
  const removeAccount = useCallback((id: string, when: number = Date.now()) => {
    setState((prev) => ({
      ...prev,
      accounts: prev.accounts.map((a) => (a.id === id ? { ...a, deletedAt: when } : a)),
    }));
  }, []);

  /* ── Purchase plans ───────────────────────────────────────────── */
  // Plans are backend-owned. Create through `useCreatePurchasePlan`
  // (POST /finance/purchase-plans) and cancel through
  // `useCancelPurchasePlan` (POST /finance/purchase-plans/:id/cancel).
  // No local generation of `plan_*` ids; `state.purchasePlans` is
  // populated solely by the TanStack Query cache above.

  /* ── Filters ──────────────────────────────────────────────────── */
  const setFilters = useCallback((patch: Partial<FinanceFilters>) => {
    setState((prev) => ({ ...prev, filters: { ...prev.filters, ...patch } }));
  }, []);

  const clearFilters = useCallback(() => {
    setState((prev) => ({ ...prev, filters: EMPTY_FILTERS }));
  }, []);

  /* ── Derived selectors ───────────────────────────────────────── */
  const peopleById = useMemo(() => {
    const m = new Map<string, Person>();
    for (const p of state.people) m.set(p.id, p);
    return m;
  }, [state.people]);

  // Categories live in the backend now; the lookup includes a one-time
  // localStorage fallback so transactions seeded before the backend move still
  // resolve their category names. Legacy entries never appear in the
  // Categories list (state.categories), only in this lookup.
  // TODO backend-v0.2: drop the fallback once transactions migrate too.
  const categoriesById = useMemo(() => {
    const m = new Map<string, Category>();
    for (const c of readLegacyCategories()) m.set(c.id, c);
    for (const c of state.categories) m.set(c.id, c);
    return m;
  }, [state.categories]);

  // cardsById merges legacy localStorage cards (pre-Phase-2 seed) UNDER
  // backend + archived snapshots so seeded transactions still resolve a
  // label. Legacy entries do NOT appear in `state.creditCards`.
  // TODO backend-v0.2: drop the fallback once transactions migrate too.
  const cardsById = useMemo(() => {
    const m = new Map<string, CreditCard>();
    for (const c of readLegacyCreditCards()) m.set(c.id, c);
    for (const c of state.creditCards) m.set(c.id, c);
    return m;
  }, [state.creditCards]);

  /** Cards that should appear in pickers and active lists. Excludes soft-deleted. */
  const activeCreditCards = useMemo(
    () => state.creditCards.filter((c) => !c.deletedAt),
    [state.creditCards],
  );

  /**
   * Unified Account lookup spanning real bank accounts and the
   * card-as-account derivation. Transfer endpoints reference ids that may
   * point at either; consumers should always resolve through this map.
   */
  const allAccountsById = useMemo(() => {
    const m = new Map<string, Account>();
    for (const a of state.accounts) m.set(a.id, a);
    for (const c of state.creditCards) {
      m.set(c.id, {
        id: c.id,
        name: c.name,
        ownerId: c.ownerId,
        type: "card",
        deletedAt: c.deletedAt,
      });
    }
    return m;
  }, [state.accounts, state.creditCards]);

  /** Active accounts (bank + card) for pickers. */
  const activeAccounts = useMemo<Account[]>(() => {
    const out: Account[] = [];
    for (const a of state.accounts) if (!a.deletedAt) out.push(a);
    for (const c of state.creditCards) {
      if (!c.deletedAt) {
        out.push({ id: c.id, name: c.name, ownerId: c.ownerId, type: "card" });
      }
    }
    return out;
  }, [state.accounts, state.creditCards]);

  const purchasePlansById = useMemo(() => {
    const m = new Map<string, PurchasePlan>();
    for (const p of state.purchasePlans) m.set(p.id, p);
    return m;
  }, [state.purchasePlans]);

  /**
   * How many transactions are sitting in `localStorage` that the backend
   * does not have.
   *
   * ── Why this is counted instead of merged, or deleted ──────────────
   * Merging them is what this sprint removed: it let a browser-only row
   * move a chart while the KPI tiles, which read Postgres, disagreed —
   * silently, with no way for a person to tell the two apart.
   *
   * Deleting them would be worse. These rows are almost always leftovers:
   * demo fixtures, or transfers and plan installments written before those
   * moved to the backend. "Almost always" is not "always", and destroying
   * somebody's records to tidy a storage key is not a trade this module
   * gets to make on its own.
   *
   * So they stay exactly where they are, they count for nothing, and the
   * screen says how many there are. Not merged, not deleted, not hidden.
   */
  const localOnlyTransactions = useMemo(() => {
    const backendIds = new Set(state.transactions.map((t) => t.id));
    const txLeftovers = localState.transactions.filter((t) => !backendIds.has(t.id)).length;
    // Recurring entries joined the backend after transactions did, so the
    // same leftovers can exist for them. Counted together because the badge
    // answers one question: how much of what is on this screen is not in
    // the database.
    const fixedIds = new Set(state.recurringEntries.map((f) => f.id));
    const fxLeftovers = localState.recurringEntries.filter((f) => !fixedIds.has(f.id)).length;
    return txLeftovers + fxLeftovers;
  }, [localState.transactions, localState.recurringEntries, state.transactions, state.recurringEntries]);

  return {
    state,
    localOnlyTransactions,
    peopleById,
    categoriesById,
    cardsById,
    activeCreditCards,
    allAccountsById,
    activeAccounts,
    purchasePlansById,

    addTransaction,
    updateTransaction,
    removeTransaction,

    addAccount,
    updateAccount,
    removeAccount,

    addRecurringEntry,
    updateRecurringEntry,
    endRecurringEntry,
    removeRecurringEntry,

    setFilters,
    clearFilters,
  };
}

export type FinanceStore = ReturnType<typeof useFinance>;
