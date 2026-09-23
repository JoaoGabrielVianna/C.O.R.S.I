import { describe, expect, it } from "vitest";

import { MONTHS_BACK, MONTHS_FORWARD, monthOptions, shiftMonthKey } from "./monthOptions";

/**
 * The Finance month selector.
 *
 * ── The defect under test ──────────────────────────────────────────────
 * The list used to be built from `state.transactions`, so a month with no
 * spending in it did not exist as an option. That is precisely the month a
 * monthly commitment is about: bills fall due whether or not anything was
 * bought, and the historical navigation the Recurring tab is built around
 * was unreachable for exactly those months.
 */
describe("finance · month selector options", () => {
  const today = new Date(Date.UTC(2026, 8, 21)); // 2026-09-21

  it("offers twelve months back, the current one, and three forward", () => {
    const options = monthOptions([], undefined, today);

    expect(options).toHaveLength(MONTHS_BACK + 1 + MONTHS_FORWARD);
    expect(options).toContain("2026-09"); // current
    expect(options).toContain("2025-09"); // twelve back
    expect(options).toContain("2026-12"); // three forward
    // One step beyond each edge is NOT offered.
    expect(options).not.toContain("2025-08");
    expect(options).not.toContain("2027-01");
  });

  it("includes months that have no transaction at all", () => {
    // The whole point: an empty ledger still has twelve reachable months
    // of history, because obligations are not transactions.
    const options = monthOptions([], undefined, today);
    expect(options).toContain("2026-07");
    expect(options).toContain("2026-02");
  });

  it("keeps a transaction month outside the rolling window reachable", () => {
    // The window is a convenience; the ledger is a fact. A purchase from
    // two years ago is real history, and dropping it would make a month
    // the operator can see in the ledger unreachable from the control
    // meant to reach it.
    const old = Date.UTC(2023, 3, 14); // 2023-04
    const options = monthOptions([old], undefined, today);

    expect(options).toContain("2023-04");
    expect(options).toContain("2026-09");
  });

  it("keeps the selected month reachable however it was reached", () => {
    const options = monthOptions([], "2019-11", today);
    expect(options).toContain("2019-11");
  });

  it("sorts newest first, deterministically", () => {
    const options = monthOptions([Date.UTC(2023, 3, 14)], "2019-11", today);
    const sorted = [...options].sort((a, b) => b.localeCompare(a));

    expect(options).toEqual(sorted);
    expect(options[0]).toBe("2026-12");
    expect(options.at(-1)).toBe("2019-11");
    // No duplicates, even when a transaction month is inside the window.
    expect(new Set(options).size).toBe(options.length);
  });

  it("does not duplicate a transaction month that is already in the window", () => {
    const inWindow = Date.UTC(2026, 7, 3); // 2026-08
    const options = monthOptions([inWindow, inWindow], undefined, today);
    expect(options.filter((m) => m === "2026-08")).toHaveLength(1);
  });

  it("crosses the year boundary in both directions", () => {
    expect(shiftMonthKey("2026-01", -1)).toBe("2025-12");
    expect(shiftMonthKey("2026-12", 1)).toBe("2027-01");
    expect(shiftMonthKey("2026-09", -12)).toBe("2025-09");
  });

  it("builds a January window without falling out of the year", () => {
    const january = new Date(Date.UTC(2026, 0, 15));
    const options = monthOptions([], undefined, january);
    expect(options).toContain("2025-01");
    expect(options).toContain("2026-01");
    expect(options).toContain("2026-04");
  });
});
