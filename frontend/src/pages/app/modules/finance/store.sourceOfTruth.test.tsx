// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { DEMO_FIXTURES_KEY, useFinance } from "./store";
import { SEED_FLAG_KEY } from "./seed";

/**
 * Finance's source-of-truth invariant, at the seam where it used to break.
 *
 * ── What was broken ────────────────────────────────────────────────────
 * `useFinance` returned `state.transactions` as `backend ∪ localStorage`.
 * The KPI tiles read `GET /finance/transactions/totals`, which can only see
 * Postgres; every chart, table and per-category rollup aggregated the union.
 * So a row that existed only in the browser moved half the screen and not
 * the other half, and nothing said which number to believe. Two things made
 * that easy to hit: the demo fixtures built themselves on first visit in
 * dev, and their `categoryId` values are `c_food`-style strings rather than
 * the backend's UUIDs — so their money landed under keys no row rendered,
 * producing a non-zero monthly total over categories all reading "0 tx".
 *
 * ── What these tests pin ───────────────────────────────────────────────
 *  1. A fresh profile seeds nothing.
 *  2. The fixtures still exist, behind an explicit opt-in.
 *  3. Even with the fixtures ON — the hostile case — no local row reaches
 *     `state.transactions`, which is what every aggregate reads.
 *  4. Local rows are counted and not destroyed.
 *
 * (3) is the load-bearing one. (1) and (2) are about how easy the trap is
 * to fall into; (3) is about whether the trap exists at all.
 */

const STORAGE_KEY = "corsi.module.finance.v0";

// The backend answers with nothing, so anything appearing in
// `state.transactions` can only have come from localStorage.
vi.mock("@/modules/finance/hooks/useTransactions", () => ({
  useTransactions: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/modules/finance/hooks/useCategories", () => ({
  useCategories: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/modules/finance/hooks/useCards", () => ({
  useCards: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/modules/finance/hooks/usePurchasePlans", () => ({
  usePurchasePlans: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/modules/finance/hooks/usePersons", () => ({
  usePersons: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/modules/finance/hooks/useRecurringEntries", () => ({
  useRecurringEntries: () => ({ data: [], isLoading: false }),
  useRecurringSummary: () => ({ data: undefined, isLoading: false }),
  useCreateRecurringEntry: () => ({ mutate: () => {} }),
  useUpdateRecurringEntry: () => ({ mutate: () => {} }),
  useDeleteRecurringEntry: () => ({ mutate: () => {} }),
}));
vi.mock("@/modules/finance/api/cardArchive", () => ({ listArchivedCards: () => [] }));

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/** One browser-only transaction, shaped the way the store stores them. */
function legacyRow(id: string, amount: number) {
  return {
    id, type: "expense", personId: "p_owner", amount,
    description: "local row", categoryId: "c_food",
    paymentMethod: "pix", date: Date.now(), status: "paid",
    source: "manual", notes: "", createdAt: Date.now(), updatedAt: Date.now(),
  };
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

describe("finance · a fresh browser is not seeded", () => {
  it("writes no fixtures when nobody asked for them", async () => {
    const { result } = renderHook(() => useFinance(), { wrapper });
    await waitFor(() => expect(result.current.state).toBeDefined());

    expect(result.current.state.transactions).toHaveLength(0);
    expect(result.current.state.recurringEntries).toHaveLength(0);
    expect(result.current.state.people).toHaveLength(0);
    // The flag is the fixtures' own record that they ran. It must not be
    // set, or turning the opt-in on later would find the work "already
    // done" and produce an empty store forever.
    expect(window.localStorage.getItem(SEED_FLAG_KEY)).toBeNull();
  });

  it("builds the fixtures when the opt-in is set", async () => {
    window.localStorage.setItem(DEMO_FIXTURES_KEY, "1");
    const { result } = renderHook(() => useFinance(), { wrapper });
    await waitFor(() => expect(window.localStorage.getItem(SEED_FLAG_KEY)).toBe("1"));
    // They land in the LOCAL store — which is the next test's subject.
    const raw = window.localStorage.getItem(STORAGE_KEY);
    expect(JSON.parse(raw ?? "{}").recurringEntries.length).toBeGreaterThan(0);
    expect(result.current).toBeDefined();
  });
});

describe("finance · local rows never become money", () => {
  it("keeps browser-only transactions out of the aggregated array", async () => {
    // The hostile case: rows already sitting in storage, backend empty.
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({
      people: [], categories: [], creditCards: [], accounts: [], purchasePlans: [],
      recurringEntries: [],
      transactions: [legacyRow("tx_legacy_1", 12_345), legacyRow("tx_legacy_2", 6_789)],
      filters: {},
    }));

    const { result } = renderHook(() => useFinance(), { wrapper });
    await waitFor(() => expect(result.current.state).toBeDefined());

    // `state.transactions` is what Overview's charts, the Transactions
    // table, the Categories rollup and People all aggregate. The backend
    // returned nothing, so a single row here is a row that came from the
    // browser — and that is the bug.
    expect(result.current.state.transactions).toHaveLength(0);
  });

  it("counts them instead of destroying them", async () => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({
      people: [], categories: [], creditCards: [], accounts: [], purchasePlans: [],
      recurringEntries: [],
      transactions: [legacyRow("tx_legacy_1", 12_345), legacyRow("tx_legacy_2", 6_789)],
      filters: {},
    }));

    const { result } = renderHook(() => useFinance(), { wrapper });
    await waitFor(() => expect(result.current.localOnlyTransactions).toBe(2));

    // Still on disk, byte for byte. Tidying a storage key is not a reason
    // to delete records somebody may not have copies of.
    const stored = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "{}");
    expect(stored.transactions).toHaveLength(2);
  });

  it("reports zero when the browser holds nothing extra", async () => {
    const { result } = renderHook(() => useFinance(), { wrapper });
    await waitFor(() => expect(result.current.state).toBeDefined());
    expect(result.current.localOnlyTransactions).toBe(0);
  });
});
