// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import { I18nProvider } from "@/lib/i18n";
import type { WriteReceipt } from "@/modules/agents/api/stream";

import { WriteReceiptStrip } from "./WriteReceiptStrip";

/**
 * The incident these guard: a live agent answered "8 transações
 * importadas" in a turn that called nothing, against an empty ledger. The
 * words were the only thing on screen.
 */

afterEach(cleanup);

function show(receipt?: WriteReceipt) {
  return render(
    <I18nProvider>
      <WriteReceiptStrip receipt={receipt} />
    </I18nProvider>,
  );
}

const executed: WriteReceipt = {
  message_id: "m1",
  executed: 1,
  failed: 0,
  refused: 0,
  writes: [
    {
      tool_call_id: "t1",
      capability: "finance.import.commit",
      status: "EXECUTED",
      occurred_at: "2026-08-28T12:00:00Z",
      duration_ms: 12,
    },
  ],
};

describe("WriteReceiptStrip", () => {
  it("confirms a change only when one actually executed", () => {
    show(executed);
    expect(screen.getByText(/1 alteração confirmada|1 change confirmed/)).toBeTruthy();
  });

  it("shows no confirmation for a turn that executed nothing", () => {
    // This is the incident, rendered. The model's sentence is elsewhere on
    // screen; nothing here corroborates it.
    show({ message_id: "m1", executed: 0, failed: 0, refused: 0, writes: [] });
    expect(screen.queryByText(/confirmada|confirmed/)).toBeNull();
  });

  it("never confirms a failed write", () => {
    show({
      message_id: "m1",
      executed: 0,
      failed: 1,
      refused: 0,
      writes: [
        {
          tool_call_id: "t1",
          capability: "finance.import.commit",
          status: "FAILED",
          occurred_at: "2026-08-28T12:00:00Z",
          duration_ms: 4,
          error_code: "tool_execution_failed",
        },
      ],
    });
    expect(screen.queryByText(/confirmada|confirmed/)).toBeNull();
    expect(screen.getByText(/falhou|failed/)).toBeTruthy();
    expect(screen.getByText(/nada foi alterado|nothing was changed/)).toBeTruthy();
  });

  it("never confirms a refused write", () => {
    show({
      message_id: "m1",
      executed: 0,
      failed: 0,
      refused: 1,
      writes: [
        {
          tool_call_id: "t1",
          capability: "finance.import.commit",
          status: "NOT_EXECUTED",
          occurred_at: "2026-08-28T12:00:00Z",
          duration_ms: 0,
          error_code: "tool_not_authorized",
        },
      ],
    });
    expect(screen.queryByText(/confirmada|confirmed/)).toBeNull();
    expect(screen.getByText(/recusada|refused/)).toBeTruthy();
  });

  it("renders nothing when the receipt is missing, rather than assuming success", () => {
    const { container } = show(undefined);
    expect(container.innerHTML).toBe("");
  });

  it("reads only the receipt: the message text cannot produce a confirmation", () => {
    // No content prop exists on this component at all, which is the point.
    // The assertion is structural: a turn whose receipt says nothing ran
    // shows nothing, no matter what was said beside it.
    show({ message_id: "m1", executed: 0, failed: 0, refused: 0, writes: [] });
    expect(screen.queryByText(/8 transações importadas/)).toBeNull();
    expect(screen.queryByText(/confirmada|confirmed/)).toBeNull();
  });
});
