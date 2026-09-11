// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { I18nFixture } from "@/lib/i18n";
import type { ReadReceipt } from "@/modules/agents/api/stream";
import { ExternalReadStrip } from "./ExternalReadStrip";

/**
 * The strip that would have contradicted an invented follower count.
 *
 * A live content agent answered "1.535 seguidores, 40.055 views" in a turn
 * that called nothing; the real figures were 163 and 224. What is asserted
 * here is that the component renders the RECEIPT and never the message —
 * and that, unlike the write strip, it makes the ABSENCE of evidence
 * visible, because absence is the state that failure happened in.
 */

function wrapper() {
  return ({ children }: { children: ReactNode }) => (
    <I18nFixture lang="pt">{children}</I18nFixture>
  );
}

function receipt(over: Partial<ReadReceipt> = {}): ReadReceipt {
  return {
    message_id: "m1",
    reads: [],
    status: "NO_EXTERNAL_READ",
    verified: 0,
    failed: 0,
    available: true,
    ...over,
  };
}

const aRead = (over = {}) => ({
  tool_call_id: "tc1",
  capability: "meta_threads.profile.insights",
  source: "meta_threads",
  status: "VERIFIED_EXTERNAL_READ" as const,
  occurred_at: "2026-09-07T12:00:00Z",
  duration_ms: 120,
  ...over,
});

afterEach(cleanup);

describe("ExternalReadStrip", () => {
  it("says so when a turn read nothing external and could have", () => {
    // THE case: the state the invented numbers were produced in.
    render(<ExternalReadStrip receipt={receipt()} />, { wrapper: wrapper() });

    expect(screen.getByText(/sem leitura externa/i)).toBeTruthy();
    expect(screen.getByTestId("external-read-badge").getAttribute("data-tone")).toBe("warning");
  });

  it("stays quiet for an agent that cannot read externally at all", () => {
    // Otherwise the badge appears on every turn of every agent and people
    // learn to ignore it exactly where it matters.
    render(<ExternalReadStrip receipt={receipt({ available: false })} />, { wrapper: wrapper() });
    expect(screen.queryByTestId("external-read-badge")).toBeNull();
  });

  it("reports a real read, with its source", () => {
    render(
      <ExternalReadStrip
        receipt={receipt({ status: "VERIFIED_EXTERNAL_READ", verified: 1, reads: [aRead()] })}
      />,
      { wrapper: wrapper() },
    );
    expect(screen.getByText(/leitura externa/i)).toBeTruthy();
    expect(screen.getByText(/meta_threads/)).toBeTruthy();
    expect(screen.getByTestId("external-read-badge").getAttribute("data-tone")).toBe("verified");
  });

  it("distinguishes a failed read from having read nothing", () => {
    render(
      <ExternalReadStrip
        receipt={receipt({
          status: "FAILED_EXTERNAL_READ",
          failed: 1,
          reads: [aRead({ status: "FAILED_EXTERNAL_READ", error_code: "tool_execution_failed" })],
        })}
      />,
      { wrapper: wrapper() },
    );
    // "we tried and it did not answer" is a different sentence from "we
    // did not try", and a different next action for the reader.
    expect(screen.getByText(/falhou/i)).toBeTruthy();
    expect(screen.getByText(/nada verificado/i)).toBeTruthy();
    expect(screen.queryByText(/sem leitura externa/i)).toBeNull();
  });

  it("reports a partial turn as verified while naming the failures", () => {
    render(
      <ExternalReadStrip
        receipt={receipt({
          status: "VERIFIED_EXTERNAL_READ",
          verified: 1,
          failed: 2,
          reads: [aRead(), aRead({ status: "FAILED_EXTERNAL_READ" })],
        })}
      />,
      { wrapper: wrapper() },
    );
    const badge = screen.getByTestId("external-read-badge");
    expect(badge.getAttribute("data-tone")).toBe("verified");
    expect(badge.textContent).toMatch(/2 falharam/);
  });

  /* ── the property that outranks the states ─────────────────────────── */

  it("never renders anything derived from the message text", () => {
    // The component takes no message and has no access to one. This is the
    // assertion that would fail if somebody ever passed it prose to
    // "improve" the wording.
    const props = ExternalReadStrip.length;
    expect(props).toBe(1);

    render(
      <ExternalReadStrip
        receipt={receipt({ status: "VERIFIED_EXTERNAL_READ", verified: 1, reads: [aRead()] })}
      />,
      { wrapper: wrapper() },
    );
    // No number from any payload can appear, because none was given.
    expect(document.body.textContent).not.toMatch(/163|224|1\.535|40\.055/);
  });

  it("renders nothing without a receipt, rather than guessing", () => {
    render(<ExternalReadStrip receipt={undefined} />, { wrapper: wrapper() });
    expect(screen.queryByTestId("external-read-badge")).toBeNull();
  });
});
