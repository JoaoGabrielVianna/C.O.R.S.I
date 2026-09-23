// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n";
import { RecurringEntries } from "./RecurringEntries";
import type { ApiMonthlyCommitment } from "@/modules/finance/api/monthlyCommitment";
import type { FinanceStore } from "./store";
import type { RecurringEntry } from "./types";

/**
 * The recurring-definition form, after the month arrived above it.
 *
 * Three things are new and each is a rule the domain holds: an annual
 * recurrence must say WHICH month it falls in, an amount may be declared
 * variable without becoming optional, and an edit may be carried into the
 * current month only when the backend would accept it.
 *
 * The form is reached through the tab, not rendered directly, because the
 * `apply_to_period` control depends on the month the tab is showing — and
 * a test that mounted the form alone would be testing a component that
 * cannot exist in the product.
 */

const getMonthlyCommitment = vi.fn();

vi.mock("@/modules/finance/api/monthlyCommitment", async (orig) => ({
  ...(await orig<Record<string, unknown>>()),
  getMonthlyCommitment: (...a: unknown[]) => getMonthlyCommitment(...a),
  markOccurrencePaid: vi.fn().mockResolvedValue(undefined),
  markOccurrencePending: vi.fn().mockResolvedValue(undefined),
  setOccurrenceAmount: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/lib/api/workspace", () => ({ getApiWorkspaceId: () => "ws-test" }));

/* ── fixtures ────────────────────────────────────────────────────────── */

const RENT: RecurringEntry = {
  id: "entry-rent",
  description: "Aluguel",
  amount: 250000,
  categoryId: "cat-home",
  personId: "",
  dueDay: 5,
  recurrence: "monthly",
  status: "active",
  startsAt: Date.UTC(2025, 0, 1),
  notes: "",
};

function month(over: Partial<ApiMonthlyCommitment> = {}): ApiMonthlyCommitment {
  return {
    period: "2026-09",
    is_projection: false,
    today: "2026-09-21",
    time_zone: "America/Sao_Paulo",
    committed_cents: 250000,
    paid_cents: 0,
    remaining_cents: 250000,
    estimated_cents: 0,
    occurrence_count: 1,
    paid_count: 0,
    pending_count: 1,
    estimated_count: 0,
    overdue_count: 0,
    items: [{
      recurring_entry_id: "entry-rent",
      occurrence_id: "occ-rent",
      period: "2026-09",
      description: "Aluguel",
      category_id: "cat-home",
      category: "Casa",
      due_on: "2026-09-05",
      amount_cents: 250000,
      amount_estimated: false,
      status: "pending",
      overdue: false,
    }],
    ...over,
  };
}

type Recorded = { id: string; patch: Record<string, unknown>; applyToPeriod?: string };

function fakeStore(
  recorded: { create: Record<string, unknown>[]; update: Recorded[] },
  selectedMonth = "2026-09",
): FinanceStore {
  return {
    state: {
      filters: { month: selectedMonth },
      recurringEntries: [RENT],
      categories: [{ id: "cat-home", name: "Casa", type: "expense", color: "slate", icon: "home" }],
      people: [],
    },
    categoriesById: new Map([["cat-home", { id: "cat-home", name: "Casa", color: "slate", icon: "home" }]]),
    peopleById: new Map(),
    addRecurringEntry: (input: Record<string, unknown>) => { recorded.create.push(input); return ""; },
    updateRecurringEntry: (id: string, patch: Record<string, unknown>, applyToPeriod?: string) =>
      recorded.update.push({ id, patch, applyToPeriod }),
    endRecurringEntry: () => {},
    removeRecurringEntry: () => {},
  } as unknown as FinanceStore;
}

function renderTab(store: FinanceStore) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nFixture lang="pt">
      <QueryClientProvider client={qc}>
        <RecurringEntries store={store} />
      </QueryClientProvider>
    </I18nFixture>,
  );
}

/** Opens the form on the existing definition. */
async function openEdit(user: ReturnType<typeof userEvent.setup>) {
  const rows = await screen.findAllByText("Aluguel");
  // The definitions section renders the description as a clickable row;
  // the month above renders it too, so the LAST one is the definition.
  await user.click(rows[rows.length - 1]);
  return screen.findByRole("dialog");
}

let recorded: { create: Record<string, unknown>[]; update: Recorded[] };

beforeEach(() => {
  vi.clearAllMocks();
  recorded = { create: [], update: [] };
  getMonthlyCommitment.mockResolvedValue(month());
});
afterEach(cleanup);

/* ── the tab's shape ─────────────────────────────────────────────────── */

describe("recurring tab · hierarchy", () => {
  it("puts the month above the definitions", async () => {
    const { container } = renderTab(fakeStore(recorded));
    // Waited on copy only the MONTH renders: the definitions row below
    // shows the same amount and resolves immediately, so awaiting the
    // figure would assert on a tree the month had not reached yet.
    await screen.findByText("Falta pagar");

    const text = container.textContent ?? "";
    // "Falta pagar" is the month's headline; "Recorrentes" heads the
    // definitions section below it.
    expect(text.indexOf("Falta pagar")).toBeGreaterThanOrEqual(0);
    expect(text.indexOf("Falta pagar")).toBeLessThan(text.indexOf("Recorrentes"));
  });
});

/* ── due_month ───────────────────────────────────────────────────────── */

describe("recurring form · annual due month", () => {
  it("asks for a due month only when the recurrence is annual", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    // Monthly: the control is absent, so the invalid combination the
    // domain refuses is not reachable from this form.
    expect(screen.queryByLabelText("Mês de vencimento")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Anual" }));
    expect(screen.getByLabelText("Mês de vencimento")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Mensal" }));
    expect(screen.queryByLabelText("Mês de vencimento")).toBeNull();
  });

  it("sends the chosen month for an annual recurrence", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    await user.click(screen.getByRole("button", { name: "Anual" }));
    await user.selectOptions(screen.getByLabelText("Mês de vencimento"), "1");
    await user.click(screen.getByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(recorded.update).toHaveLength(1));
    expect(recorded.update[0].patch).toMatchObject({ recurrence: "annual", dueMonth: 1 });
  });

  it("sends no due month for a monthly recurrence", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    // Pick annual, then go back: a due month chosen and abandoned must not
    // ride along on a monthly entry, which the domain refuses.
    await user.click(screen.getByRole("button", { name: "Anual" }));
    await user.selectOptions(screen.getByLabelText("Mês de vencimento"), "4");
    await user.click(screen.getByRole("button", { name: "Mensal" }));
    await user.click(screen.getByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(recorded.update).toHaveLength(1));
    expect(recorded.update[0].patch.dueMonth).toBeUndefined();
    expect(recorded.update[0].patch).toMatchObject({ recurrence: "monthly" });
  });
});

/* ── amount_varies ───────────────────────────────────────────────────── */

describe("recurring form · variable amount", () => {
  it("changes what the amount means without making it optional", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    const toggle = screen.getByRole("checkbox", { name: /valor muda todo mês/i });
    expect((toggle as HTMLInputElement).checked).toBe(false);
    expect(screen.getByText(/Para contas de valor fixo/)).toBeTruthy();

    await user.click(toggle);
    // The hint changes to explain the new meaning of the field above.
    expect(screen.getByText(/vira uma estimativa/)).toBeTruthy();
    // And the amount field is still there and still required.
    expect(screen.getByDisplayValue("2500")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(recorded.update).toHaveLength(1));
    expect(recorded.update[0].patch).toMatchObject({ amountVaries: true, amount: 250000 });
  });
});

/* ── apply_to_period ─────────────────────────────────────────────────── */

describe("recurring form · apply to the current month", () => {
  it("offers the option for a pending occurrence in the current month", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    const box = screen.getByRole("checkbox", { name: /Aplicar também em/ });
    // OFF by default: this form submits every field on every save, so a
    // default-on box would let an edit to the description silently push
    // the definition's amount over a figure confirmed against a real bill.
    expect((box as HTMLInputElement).checked).toBe(false);
  });

  it("sends the period only when the box is ticked", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    await user.click(screen.getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(recorded.update).toHaveLength(1));
    expect(recorded.update[0].applyToPeriod).toBeUndefined();

    cleanup();
    recorded = { create: [], update: [] };
    renderTab(fakeStore(recorded));
    await openEdit(user);
    await user.click(screen.getByRole("checkbox", { name: /Aplicar também em/ }));
    await user.click(screen.getByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(recorded.update).toHaveLength(1));
    expect(recorded.update[0].applyToPeriod).toBe("2026-09");
  });

  it("does not offer it for a past month", async () => {
    getMonthlyCommitment.mockResolvedValue(month({
      period: "2026-07", today: "2026-09-21",
      items: [{ ...month().items[0], period: "2026-07", due_on: "2026-07-05" }],
    }));
    const user = userEvent.setup();
    renderTab(fakeStore(recorded, "2026-07"));
    await openEdit(user);

    expect(screen.queryByRole("checkbox", { name: /Aplicar também em/ })).toBeNull();
  });

  it("does not offer it for a future month", async () => {
    getMonthlyCommitment.mockResolvedValue(month({
      period: "2026-10", today: "2026-09-21", is_projection: true,
      items: [{ ...month().items[0], period: "2026-10", due_on: "2026-10-05", occurrence_id: undefined }],
    }));
    const user = userEvent.setup();
    renderTab(fakeStore(recorded, "2026-10"));
    await openEdit(user);

    expect(screen.queryByRole("checkbox", { name: /Aplicar também em/ })).toBeNull();
  });

  it("does not offer it when the current month is already paid", async () => {
    getMonthlyCommitment.mockResolvedValue(month({
      paid_count: 1, pending_count: 0, paid_cents: 250000, remaining_cents: 0,
      items: [{ ...month().items[0], status: "paid", paid_on: "2026-09-03" }],
    }));
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    expect(screen.queryByRole("checkbox", { name: /Aplicar também em/ })).toBeNull();
  });

  it("does not offer it when the month holds no occurrence for the entry", async () => {
    getMonthlyCommitment.mockResolvedValue(month({
      occurrence_count: 0, pending_count: 0, committed_cents: 0, remaining_cents: 0,
      items: [],
    }));
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await openEdit(user);

    expect(screen.queryByRole("checkbox", { name: /Aplicar também em/ })).toBeNull();
  });

  it("does not offer it when creating a new definition", async () => {
    const user = userEvent.setup();
    renderTab(fakeStore(recorded));
    await screen.findByText("Falta pagar");
    await user.click(screen.getByRole("button", { name: "Novo recorrente" }));
    await screen.findByRole("dialog");

    expect(screen.queryByRole("checkbox", { name: /Aplicar também em/ })).toBeNull();
  });
});
