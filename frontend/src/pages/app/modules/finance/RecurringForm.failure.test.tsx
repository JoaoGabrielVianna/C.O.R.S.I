// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n";
import { ApiError } from "@/lib/api/client";
import { RecurringEntries } from "./RecurringEntries";
import {
  useCreateRecurringEntry,
  useDeleteRecurringEntry,
  useRecurringEntries,
  useUpdateRecurringEntry,
} from "@/modules/finance/hooks/useRecurringEntries";
import type { ApiMonthlyCommitment } from "@/modules/finance/api/monthlyCommitment";
import type { FinanceStore } from "./store";
import type { RecurringEntry } from "./types";

/**
 * The silent save.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A CLOSED DIALOG IS A CLAIM THAT SOMETHING WAS SAVED
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── The defect this file exists for, exactly as it happened ────────────
 * F8 drove the real UI against the real backend. `apply_to_period` was
 * missing from the HTTP DTO, the request failed with 400, and the dialog
 * closed with no message while the database kept the old values. The
 * operator had every reason to believe the edit had landed, and the only
 * way anybody found out was reading Postgres afterwards.
 *
 * The 400 was one bug and is fixed. THIS is the other one, and it is the
 * worse of the two: it is the reason the first stayed invisible.
 *
 * ── Why these tests go through the component ───────────────────────────
 * Because the store was never wrong in isolation — `useMutation` reported
 * the failure correctly the whole time. What was wrong was the seam: a
 * component calling `.mutate()` and closing regardless. A test of the store
 * alone would have passed throughout the defect's entire life, which is
 * precisely why it has to be driven from the form.
 *
 * Every test here therefore renders the real tab, drives the real form, and
 * lets a rejected mutation arrive the way the network delivers one.
 */

const getMonthlyCommitment = vi.fn();
const listRecurringEntries = vi.fn();
const createRecurringEntry = vi.fn();
const updateRecurringEntry = vi.fn();
const deleteRecurringEntry = vi.fn();
const getRecurringSummary = vi.fn();

vi.mock("@/modules/finance/api/monthlyCommitment", async (orig) => ({
  ...(await orig<Record<string, unknown>>()),
  getMonthlyCommitment: (...a: unknown[]) => getMonthlyCommitment(...a),
  markOccurrencePaid: vi.fn().mockResolvedValue(undefined),
  markOccurrencePending: vi.fn().mockResolvedValue(undefined),
  setOccurrenceAmount: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/modules/finance/api/recurringEntries", async (orig) => ({
  ...(await orig<Record<string, unknown>>()),
  listRecurringEntries: (...a: unknown[]) => listRecurringEntries(...a),
  createRecurringEntry: (...a: unknown[]) => createRecurringEntry(...a),
  updateRecurringEntry: (...a: unknown[]) => updateRecurringEntry(...a),
  deleteRecurringEntry: (...a: unknown[]) => deleteRecurringEntry(...a),
  getRecurringSummary: (...a: unknown[]) => getRecurringSummary(...a),
}));

vi.mock("@/lib/api/workspace", () => ({ getApiWorkspaceId: () => "ws-test" }));

/* ── fixtures ────────────────────────────────────────────────────────── */

const ENTRY_ID = "entry-rent";

const apiEntry = {
  id: ENTRY_ID,
  workspace_id: "ws-test",
  description: "Aluguel",
  amount_cents: 250000,
  category_id: "cat-home",
  person_id: null,
  due_day: 5,
  recurrence: "monthly" as const,
  due_month: null,
  amount_varies: false,
  status: "active" as const,
  starts_at: "2025-01-01T12:00:00Z",
  notes: "",
  created_at: "2025-01-01T12:00:00Z",
  updated_at: "2025-01-01T12:00:00Z",
};

function month(): ApiMonthlyCommitment {
  return {
    period: "2026-09", is_projection: false, today: "2026-09-21",
    time_zone: "America/Sao_Paulo",
    committed_cents: 250000, paid_cents: 0, remaining_cents: 250000, estimated_cents: 0,
    occurrence_count: 1, paid_count: 0, pending_count: 1, estimated_count: 0, overdue_count: 0,
    items: [{
      recurring_entry_id: ENTRY_ID, occurrence_id: "occ-1", period: "2026-09",
      description: "Aluguel", category_id: "cat-home", category: "Casa",
      due_on: "2026-09-05", amount_cents: 250000, amount_estimated: false,
      status: "pending", overdue: false,
    }],
  };
}

/**
 * The real store, driven through the real hooks.
 *
 * Not a fake: the seam under test is component → store → mutation, and a
 * stubbed store would replace the very thing that was broken.
 */
function fakeStore(): FinanceStore {
  return {
    state: {
      filters: { month: "2026-09" },
      categories: [{ id: "cat-home", name: "Casa", type: "expense", color: "slate", icon: "home" }],
      people: [],
    },
    categoriesById: new Map([["cat-home", { id: "cat-home", name: "Casa", color: "slate", icon: "home" }]]),
    peopleById: new Map(),
  } as unknown as FinanceStore;
}

/**
 * Wires the real recurring hooks into a store-shaped object, so the form
 * calls the same code path production does.
 */
function RealTab() {
  const base = fakeStore();
  const entries = useRecurringEntries();
  const create = useCreateRecurringEntry();
  const update = useUpdateRecurringEntry();
  const remove = useDeleteRecurringEntry();

  const store = {
    ...base,
    state: { ...base.state, recurringEntries: entries.data ?? [] },
    // Mirrors store.ts exactly. Duplicated rather than imported because
    // `useFinance` pulls in every other Finance query, and this file is
    // about one seam.
    addRecurringEntry: (input: Omit<RecurringEntry, "id">) =>
      create.mutateAsync({
        description: input.description, amount: input.amount, categoryId: input.categoryId,
        personId: input.personId, dueDay: input.dueDay, recurrence: input.recurrence,
        dueMonth: input.dueMonth, amountVaries: input.amountVaries, notes: input.notes,
      }),
    updateRecurringEntry: (
      id: string, patch: Partial<Omit<RecurringEntry, "id">>, applyToPeriod?: string,
    ) =>
      update.mutateAsync({
        id,
        patch: {
          description: patch.description, amount_cents: patch.amount,
          category_id: patch.categoryId, due_day: patch.dueDay,
          recurrence: patch.recurrence,
          due_month: patch.recurrence === "annual" ? patch.dueMonth : undefined,
          clear_due_month: patch.recurrence === "monthly" ? true : undefined,
          amount_varies: patch.amountVaries, status: patch.status, notes: patch.notes,
          apply_to_period: applyToPeriod,
        },
      }),
    endRecurringEntry: (id: string) =>
      update.mutateAsync({ id, patch: { ends_at: new Date().toISOString(), status: "paused" } }),
    removeRecurringEntry: (id: string) => remove.mutateAsync(id),
  } as unknown as FinanceStore;

  return <RecurringEntries store={store} />;
}

function renderTab() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <I18nFixture lang="pt">
      <QueryClientProvider client={qc}><RealTab /></QueryClientProvider>
    </I18nFixture>,
  );
}

const dialog = () => screen.getByRole("dialog");
const badRequest = (msg: string) =>
  new ApiError(400, { error: { code: "invalid", message: msg } }, "bad request");

async function openEdit(user: ReturnType<typeof userEvent.setup>) {
  // The pause control exists only on a DEFINITIONS row, so waiting for it
  // is waiting for the list the form belongs to. The month above renders
  // the same description and resolves first, and clicking that row opens
  // nothing.
  await screen.findByRole("button", { name: "pause" });
  const rows = screen.getAllByText("Aluguel");
  await user.click(rows[rows.length - 1]);
  return screen.findByRole("dialog");
}

beforeEach(() => {
  vi.clearAllMocks();
  getMonthlyCommitment.mockResolvedValue(month());
  listRecurringEntries.mockResolvedValue([apiEntry]);
  getRecurringSummary.mockResolvedValue({
    at: "2026-09-21T12:00:00Z", income_monthly_cents: 0,
    expense_monthly_cents: 250000, net_monthly_cents: -250000, items: [],
  });
  createRecurringEntry.mockResolvedValue(apiEntry);
  updateRecurringEntry.mockResolvedValue(apiEntry);
  deleteRecurringEntry.mockResolvedValue(undefined);
});
afterEach(cleanup);

/* ── 1 · update failure ──────────────────────────────────────────────── */

describe("recurring form · a failed UPDATE never looks like success", () => {
  it("keeps the dialog open, says why, and keeps what was typed", async () => {
    updateRecurringEntry.mockRejectedValue(badRequest("a past month is settled history"));
    const user = userEvent.setup();
    renderTab();
    await openEdit(user);

    const amount = within(dialog()).getByPlaceholderText("500,00");
    await user.clear(amount);
    await user.type(amount, "2600,00");
    await user.click(screen.getByRole("button", { name: "Salvar" }));

    // The dialog is still there. This is the whole defect.
    await screen.findByRole("alert");
    expect(screen.queryByRole("dialog")).not.toBeNull();

    // And it says what the backend said, not a shrug.
    expect(screen.getByRole("alert").textContent).toContain("past month is settled history");

    // The typed value survived: making somebody retype a form is how a
    // retry becomes an abandonment.
    expect((within(dialog()).getByPlaceholderText("500,00") as HTMLInputElement).value).toBe("2600,00");

    // Still usable — a second attempt is one click away.
    expect((screen.getByRole("button", { name: "Salvar" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("falls back to localized copy when the failure carries no message", async () => {
    updateRecurringEntry.mockRejectedValue(new Error("network down"));
    const user = userEvent.setup();
    renderTab();
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Salvar" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Não foi possível salvar");
    expect(screen.queryByRole("dialog")).not.toBeNull();
  });

  it("lets the operator retry after a failure, and closes when it lands", async () => {
    updateRecurringEntry.mockRejectedValueOnce(badRequest("temporário"));
    const user = userEvent.setup();
    renderTab();
    await openEdit(user);

    await user.click(screen.getByRole("button", { name: "Salvar" }));
    await screen.findByRole("alert");

    updateRecurringEntry.mockResolvedValue(apiEntry);
    await user.click(screen.getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});

/* ── 2 · create failure ──────────────────────────────────────────────── */

describe("recurring form · a failed CREATE never looks like success", () => {
  it("keeps the dialog open with the values and a visible error", async () => {
    createRecurringEntry.mockRejectedValue(badRequest("due_month is required on an annual recurring entry"));
    const user = userEvent.setup();
    renderTab();
    await screen.findByText("Falta pagar");
    await user.click(screen.getByRole("button", { name: "Novo recorrente" }));
    await screen.findByRole("dialog");

    const desc = within(dialog()).getAllByRole("textbox")[0];
    await user.type(desc, "BETA teste");
    await user.type(within(dialog()).getByPlaceholderText("500,00"), "1234,56");
    await user.click(screen.getByRole("button", { name: "Criar" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("due_month is required");
    expect(screen.queryByRole("dialog")).not.toBeNull();
    expect((within(dialog()).getByPlaceholderText("500,00") as HTMLInputElement).value).toBe("1234,56");
  });
});

/* ── 3 · apply_to_period refused ─────────────────────────────────────── */

describe("recurring form · a refused apply_to_period is visible", () => {
  it("shows the backend's refusal and keeps the dialog open", async () => {
    // The exact F8 shape: the edit is rejected as a whole, and the month
    // on screen is unchanged. The operator must not be able to miss it.
    updateRecurringEntry.mockRejectedValue(
      badRequest("this month is already marked paid, so editing the recurrence does not change it"));
    const user = userEvent.setup();
    renderTab();
    await openEdit(user);

    await user.click(screen.getByRole("checkbox", { name: /Aplicar também em/ }));
    await user.click(screen.getByRole("button", { name: "Salvar" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("already marked paid");
    expect(screen.queryByRole("dialog")).not.toBeNull();
    // The instruction is still ticked, so retrying does not silently drop it.
    expect((screen.getByRole("checkbox", { name: /Aplicar também em/ }) as HTMLInputElement).checked).toBe(true);
  });
});

/* ── 4 · success ─────────────────────────────────────────────────────── */

describe("recurring form · success closes, and only then", () => {
  it("closes on a successful update and refreshes both readings", async () => {
    const user = userEvent.setup();
    renderTab();
    await openEdit(user);
    const monthReads = getMonthlyCommitment.mock.calls.length;

    await user.click(screen.getByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
    // The month and the definitions both moved: an edit can carry into a
    // month, so a screen showing both must not keep half the old answer.
    await waitFor(() => {
      expect(getMonthlyCommitment.mock.calls.length).toBeGreaterThan(monthReads);
      expect(listRecurringEntries.mock.calls.length).toBeGreaterThan(1);
    });
  });

  it("closes on a successful create and refreshes", async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText("Falta pagar");
    await user.click(screen.getByRole("button", { name: "Novo recorrente" }));
    await screen.findByRole("dialog");

    await user.type(within(dialog()).getAllByRole("textbox")[0], "BETA teste");
    await user.type(within(dialog()).getByPlaceholderText("500,00"), "10,00");
    await user.click(screen.getByRole("button", { name: "Criar" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(createRecurringEntry).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(getMonthlyCommitment.mock.calls.length).toBeGreaterThan(1));
  });

  it("does not submit twice while a save is in flight", async () => {
    let release!: () => void;
    updateRecurringEntry.mockReturnValue(new Promise((r) => { release = () => r(apiEntry); }));
    const user = userEvent.setup();
    renderTab();
    await openEdit(user);

    const save = screen.getByRole("button", { name: "Salvar" });
    await user.click(save);
    await waitFor(() => expect(screen.getByRole("button", { name: "Salvando…" })).toBeTruthy());
    await user.click(screen.getByRole("button", { name: "Salvando…" })).catch(() => {});

    expect(updateRecurringEntry).toHaveBeenCalledTimes(1);
    release();
  });
});

/* ── 5 · the row actions ─────────────────────────────────────────────── */

describe("recurring list · a failed row action never looks like success", () => {
  it("reports a failed pause instead of implying it happened", async () => {
    updateRecurringEntry.mockRejectedValue(badRequest("status inválido"));
    const user = userEvent.setup();
    renderTab();
    await screen.findByText("Falta pagar");

    await user.click(screen.getByRole("button", { name: "pause" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("status inválido");
  });
});
