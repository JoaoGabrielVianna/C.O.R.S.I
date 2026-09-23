// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n";
import { MonthlyCommitment } from "./MonthlyCommitment";
import type { ApiMonthlyCommitment, ApiOccurrence } from "@/modules/finance/api/monthlyCommitment";
import type { FinanceStore } from "./store";

/**
 * The month, as a screen.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE BACKEND COMPUTES THE MONTH; THIS SCREEN ONLY RENDERS IT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Two claims run through every test here. First that no figure on screen is
 * derived from the rows — an annual premium belongs whole to one month and
 * a twelfth to the recurring summary, and a loop over `items` knows
 * neither. Second that the two writes are MARK PAID and MARK PENDING and
 * never a toggle, because a retried toggle undoes itself silently.
 */

const getMonthlyCommitment = vi.fn();
const markOccurrencePaid = vi.fn();
const markOccurrencePending = vi.fn();
const setOccurrenceAmount = vi.fn();

vi.mock("@/modules/finance/api/monthlyCommitment", async (orig) => ({
  ...(await orig<Record<string, unknown>>()),
  getMonthlyCommitment: (...a: unknown[]) => getMonthlyCommitment(...a),
  markOccurrencePaid: (...a: unknown[]) => markOccurrencePaid(...a),
  markOccurrencePending: (...a: unknown[]) => markOccurrencePending(...a),
  setOccurrenceAmount: (...a: unknown[]) => setOccurrenceAmount(...a),
}));

vi.mock("@/lib/api/workspace", () => ({ getApiWorkspaceId: () => "ws-test" }));

/* ── fixtures ────────────────────────────────────────────────────────── */

function occurrence(over: Partial<ApiOccurrence> = {}): ApiOccurrence {
  return {
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
    ...over,
  };
}

/**
 * Totals that deliberately do NOT equal the sum of the rows.
 *
 * If a test passes while the component adds `items` up, this fixture makes
 * it fail: the committed figure here is not what a loop would produce.
 */
function month(over: Partial<ApiMonthlyCommitment> = {}): ApiMonthlyCommitment {
  return {
    period: "2026-09",
    is_projection: false,
    today: "2026-09-21",
    time_zone: "America/Sao_Paulo",
    committed_cents: 482000,
    paid_cents: 310000,
    remaining_cents: 172000,
    estimated_cents: 42000,
    occurrence_count: 4,
    paid_count: 2,
    pending_count: 2,
    estimated_count: 1,
    overdue_count: 1,
    items: [
      occurrence({ recurring_entry_id: "entry-rent", description: "Aluguel", amount_cents: 250000, status: "paid", paid_on: "2026-09-03" }),
      occurrence({ recurring_entry_id: "entry-net", description: "Internet", amount_cents: 13000, status: "paid", paid_on: "2026-09-15", due_on: "2026-09-15" }),
      occurrence({ recurring_entry_id: "entry-power", description: "Luz", amount_cents: 42000, due_on: "2026-09-20", amount_estimated: true }),
      occurrence({ recurring_entry_id: "entry-gym", description: "Academia", amount_cents: 17000, due_on: "2026-09-10", overdue: true }),
    ],
    ...over,
  };
}

function fakeStore(): FinanceStore {
  return {
    state: {
      filters: { month: "2026-09" },
      recurringEntries: [],
    },
    categoriesById: new Map(),
  } as unknown as FinanceStore;
}

function renderMonth(store: FinanceStore = fakeStore()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nFixture lang="pt">
      <QueryClientProvider client={qc}>
        <MonthlyCommitment store={store} onEditDefinition={() => {}} />
      </QueryClientProvider>
    </I18nFixture>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  getMonthlyCommitment.mockResolvedValue(month());
  markOccurrencePaid.mockResolvedValue(undefined);
  markOccurrencePending.mockResolvedValue(undefined);
  setOccurrenceAmount.mockResolvedValue(undefined);
});
afterEach(cleanup);

/* ── totals ──────────────────────────────────────────────────────────── */

describe("monthly commitment · totals", () => {
  it("renders the figures the backend sent", async () => {
    renderMonth();
    // R$ 1.720,00 is `remaining_cents`, the headline.
    expect(await screen.findByText(/1\.720,00/)).toBeTruthy();
    expect(screen.getByText(/4\.820,00/)).toBeTruthy(); // committed
    expect(screen.getByText(/3\.100,00/)).toBeTruthy(); // paid
  });

  it("does not recompute the totals from the rows", async () => {
    // The fixture's rows add to 322000 cents, and the payload says 482000.
    // A component that summed `items` would show R$ 3.220,00 here.
    renderMonth();
    await screen.findByText(/4\.820,00/);
    expect(screen.queryByText(/3\.220,00/)).toBeNull();
  });

  it("reports progress and overdue from the payload counts", async () => {
    renderMonth();
    expect(await screen.findByText("2 de 4 pagas")).toBeTruthy();
    expect(screen.getByText("1 conta vencida")).toBeTruthy();
  });

  it("shows how much of the total is still an estimate", async () => {
    renderMonth();
    expect(await screen.findByText(/420,00 ainda é estimativa em 1 conta/)).toBeTruthy();
  });

  it("survives a month with no obligations without dividing by zero", async () => {
    getMonthlyCommitment.mockResolvedValue(month({
      items: [], occurrence_count: 0, paid_count: 0, pending_count: 0,
      estimated_count: 0, overdue_count: 0,
      committed_cents: 0, paid_cents: 0, remaining_cents: 0, estimated_cents: 0,
    }));
    renderMonth();
    expect(await screen.findByText("Nenhuma conta recorrente vence neste mês.")).toBeTruthy();
    expect(screen.getByText("0 de 0 pagas")).toBeTruthy();
  });
});

/* ── row state ───────────────────────────────────────────────────────── */

describe("monthly commitment · occurrence state", () => {
  it("renders paid and pending distinctly, in text and not only in colour", async () => {
    renderMonth();
    const rent = (await screen.findByRole("button", { name: "Desmarcar Aluguel como paga" }));
    expect(rent.getAttribute("aria-pressed")).toBe("true");

    const gym = screen.getByRole("button", { name: "Marcar Academia como paga" });
    expect(gym.getAttribute("aria-pressed")).toBe("false");

    // Both paid rows state WHEN, as words rather than as a tint.
    expect(screen.getAllByText(/pago em/)).toHaveLength(2);
  });

  it("renders overdue from the backend flag, in words", async () => {
    renderMonth();
    expect(await screen.findByText("vencida")).toBeTruthy();
  });

  it("does not call a bill due today overdue", async () => {
    // `today` is the 21st and the bill is due the 21st; the backend says
    // overdue=false. A screen deriving lateness from Date.now() could
    // easily disagree, which is the whole reason it does not.
    getMonthlyCommitment.mockResolvedValue(month({
      overdue_count: 0,
      items: [occurrence({
        recurring_entry_id: "entry-today", description: "Água",
        due_on: "2026-09-21", overdue: false,
      })],
    }));
    renderMonth();
    await screen.findByText("Água");
    expect(screen.queryByText("vencida")).toBeNull();
    expect(screen.getByText(/vence/)).toBeTruthy();
  });

  it("marks an estimated amount as such", async () => {
    renderMonth();
    await screen.findByText("Luz");
    expect(screen.getAllByText("estimado").length).toBeGreaterThan(0);
  });
});

/* ── the two writes ──────────────────────────────────────────────────── */

describe("monthly commitment · pay and unpay", () => {
  it("settles with POST and never with a toggle", async () => {
    const user = userEvent.setup();
    renderMonth();
    await user.click(await screen.findByRole("button", { name: "Marcar Academia como paga" }));

    await waitFor(() => expect(markOccurrencePaid).toHaveBeenCalledTimes(1));
    expect(markOccurrencePaid).toHaveBeenCalledWith("entry-gym", "2026-09");
    expect(markOccurrencePending).not.toHaveBeenCalled();
  });

  it("unsettles with DELETE", async () => {
    const user = userEvent.setup();
    renderMonth();
    await user.click(await screen.findByRole("button", { name: "Desmarcar Aluguel como paga" }));

    await waitFor(() => expect(markOccurrencePending).toHaveBeenCalledTimes(1));
    expect(markOccurrencePending).toHaveBeenCalledWith("entry-rent", "2026-09");
    expect(markOccurrencePaid).not.toHaveBeenCalled();
  });

  it("does not spam a second request while the first is in flight", async () => {
    let release!: () => void;
    markOccurrencePaid.mockReturnValue(new Promise<void>((r) => { release = () => r(); }));

    const user = userEvent.setup();
    renderMonth();
    const gym = await screen.findByRole("button", { name: "Marcar Academia como paga" });

    await user.click(gym);
    await waitFor(() => expect(gym.hasAttribute("disabled")).toBe(true));
    await user.click(gym).catch(() => {});

    expect(markOccurrencePaid).toHaveBeenCalledTimes(1);
    release();
  });

  it("leaves the rendered state alone when a write fails", async () => {
    // Nothing is optimistic here, deliberately: an optimistic tick would
    // have to predict four server-computed totals and five counts from the
    // row it is changing, which is recomputing financial truth in the
    // client. So a failure needs no rollback — the row never moved — and
    // the failure is reported instead.
    markOccurrencePaid.mockRejectedValue(new Error("boom"));

    const user = userEvent.setup();
    renderMonth();
    await user.click(await screen.findByRole("button", { name: "Marcar Academia como paga" }));

    expect(await screen.findByText("Não foi possível salvar. Nada foi alterado.")).toBeTruthy();
    // Still pending, and still offering to be settled.
    expect(screen.getByRole("button", { name: "Marcar Academia como paga" })).toBeTruthy();
  });

  it("refetches the month after a successful write", async () => {
    const user = userEvent.setup();
    renderMonth();
    await screen.findByText("Aluguel");
    const readsBefore = getMonthlyCommitment.mock.calls.length;

    await user.click(screen.getByRole("button", { name: "Marcar Academia como paga" }));
    await waitFor(() =>
      expect(getMonthlyCommitment.mock.calls.length).toBeGreaterThan(readsBefore));
  });
});

/* ── variable amounts ────────────────────────────────────────────────── */

describe("monthly commitment · variable amounts", () => {
  it("PATCHes the month's amount and refetches", async () => {
    const user = userEvent.setup();
    renderMonth();

    await user.click(await screen.findByRole("button", { name: "Valor de Luz neste mês" }));
    const field = screen.getByRole("textbox", { name: "Valor de Luz neste mês" });
    await user.clear(field);
    await user.type(field, "437,20");
    await user.click(screen.getByRole("button", { name: "Salvar valor" }));

    await waitFor(() => expect(setOccurrenceAmount).toHaveBeenCalledTimes(1));
    expect(setOccurrenceAmount).toHaveBeenCalledWith("entry-power", "2026-09", 43720);
    // It does not also settle the bill: two facts, two requests.
    expect(markOccurrencePaid).not.toHaveBeenCalled();
  });

  it("cancels without sending anything", async () => {
    const user = userEvent.setup();
    renderMonth();

    await user.click(await screen.findByRole("button", { name: "Valor de Luz neste mês" }));
    await user.click(screen.getByRole("button", { name: "Cancelar" }));

    expect(setOccurrenceAmount).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Valor de Luz neste mês" })).toBeTruthy();
  });

  it("does not offer to edit the amount of a bill already paid", async () => {
    // The backend refuses it on purpose — unpay, change, pay again — so
    // offering the control would be inviting a 409.
    renderMonth();
    await screen.findByText("Aluguel");

    expect(screen.queryByRole("button", { name: "Valor de Aluguel neste mês" })).toBeNull();
    expect(screen.getByText(/desmarque, ajuste e marque de novo/)).toBeTruthy();
  });
});

/* ── projection and history ──────────────────────────────────────────── */

describe("monthly commitment · projection and history", () => {
  const projection = () => month({
    period: "2026-10",
    is_projection: true,
    paid_count: 0,
    items: [occurrence({
      recurring_entry_id: "entry-rent", period: "2026-10", description: "Aluguel",
      occurrence_id: undefined, due_on: "2026-10-05", amount_estimated: true,
    })],
  });

  it("labels a future month as a projection and says what that means", async () => {
    getMonthlyCommitment.mockResolvedValue(projection());
    renderMonth();

    expect(await screen.findByText("projeção")).toBeTruthy();
    expect(screen.getByText(/Este mês ainda não começou/)).toBeTruthy();
  });

  it("disables every write on a projected month", async () => {
    getMonthlyCommitment.mockResolvedValue(projection());
    const user = userEvent.setup();
    renderMonth();

    const control = await screen.findByRole("button", { name: "Marcar Aluguel como paga" });
    expect(control.hasAttribute("disabled")).toBe(true);
    await user.click(control).catch(() => {});
    expect(markOccurrencePaid).not.toHaveBeenCalled();

    // And the amount cannot be edited either.
    expect(screen.queryByRole("button", { name: "Valor de Aluguel neste mês" })).toBeNull();
  });

  it("says a reconstructed past month is not a record of the time", async () => {
    getMonthlyCommitment.mockResolvedValue(month({
      period: "2026-07",
      today: "2026-09-21",
      estimated_count: 1,
      items: [occurrence({
        recurring_entry_id: "entry-rent", period: "2026-07",
        due_on: "2026-07-05", amount_estimated: true,
      })],
    }));
    renderMonth();
    expect(await screen.findByText(/reconstruídos a partir das recorrências de hoje/)).toBeTruthy();
  });
});

/* ── unplaceable ─────────────────────────────────────────────────────── */

describe("monthly commitment · unplaceable annual entries", () => {
  const withUnplaceable = () => month({
    unplaceable: [{
      recurring_entry_id: "entry-ipva",
      description: "IPVA",
      reason: "missing_due_month",
    }],
  });

  it("states that the totals may be incomplete rather than hiding it", async () => {
    getMonthlyCommitment.mockResolvedValue(withUnplaceable());
    renderMonth();

    expect(await screen.findByText("Os totais deste mês podem estar incompletos.")).toBeTruthy();
    expect(screen.getByText(/não dizem em qual mês vencem/)).toBeTruthy();
  });

  it("offers a way to repair the definition, and invents no month", async () => {
    getMonthlyCommitment.mockResolvedValue(withUnplaceable());
    const onEdit = vi.fn();
    const store = fakeStore();
    (store.state.recurringEntries as unknown[]).push({ id: "entry-ipva", description: "IPVA" });

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <I18nFixture lang="pt">
        <QueryClientProvider client={qc}>
          <MonthlyCommitment store={store} onEditDefinition={onEdit} />
        </QueryClientProvider>
      </I18nFixture>,
    );

    const user = userEvent.setup();
    const repair = await screen.findByRole("button", { name: /IPVA/ });
    await user.click(repair);

    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit.mock.calls[0][0]).toMatchObject({ id: "entry-ipva" });
  });
});

/* ── accessibility and layout ────────────────────────────────────────── */

describe("monthly commitment · accessibility", () => {
  it("names the obligation in every payment control", async () => {
    renderMonth();
    await screen.findByText("Aluguel");

    for (const name of ["Aluguel", "Internet", "Luz", "Academia"]) {
      const control =
        screen.queryByRole("button", { name: `Marcar ${name} como paga` }) ??
        screen.queryByRole("button", { name: `Desmarcar ${name} como paga` });
      expect(control, `no accessible payment control for ${name}`).toBeTruthy();
    }
  });

  it("exposes the paid state to assistive technology, not only as colour", async () => {
    renderMonth();
    const paid = await screen.findByRole("button", { name: "Desmarcar Aluguel como paga" });
    const pending = screen.getByRole("button", { name: "Marcar Academia como paga" });

    expect(paid.getAttribute("aria-pressed")).toBe("true");
    expect(pending.getAttribute("aria-pressed")).toBe("false");
  });

  it("commits an inline amount edit from the keyboard", async () => {
    const user = userEvent.setup();
    renderMonth();

    await user.click(await screen.findByRole("button", { name: "Valor de Luz neste mês" }));
    const field = screen.getByRole("textbox", { name: "Valor de Luz neste mês" });
    await user.clear(field);
    await user.type(field, "98,40{Enter}");

    await waitFor(() => expect(setOccurrenceAmount).toHaveBeenCalledWith("entry-power", "2026-09", 9840));
  });

  it("cancels an inline amount edit with Escape", async () => {
    const user = userEvent.setup();
    renderMonth();

    await user.click(await screen.findByRole("button", { name: "Valor de Luz neste mês" }));
    await user.keyboard("{Escape}");

    expect(setOccurrenceAmount).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Valor de Luz neste mês" })).toBeTruthy();
  });

  it("renders the core surface as a list rather than a wide table", async () => {
    // A table would need horizontal scrolling on a phone, which is where
    // "what have I not paid" is actually asked. The list keeps the status
    // control, the description, the due state and the amount in one row at
    // any width.
    const { container } = renderMonth();
    await screen.findByText("Aluguel");

    expect(container.querySelector("table")).toBeNull();
    const list = screen.getByRole("list");
    expect(within(list).getAllByRole("listitem")).toHaveLength(4);
  });
});
