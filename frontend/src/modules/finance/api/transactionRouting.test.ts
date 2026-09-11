import { describe, expect, it } from "vitest";

import { editWritesToBackend, isBackendTransactionId } from "./transactions";

/**
 * Which writer handles an edit.
 *
 * ── The bug this file exists to keep out ───────────────────────────────
 * `TransactionModal` used to route an edit locally when
 * `!isBackendTransactionId(id) || tx.planId != null`. The second clause was
 * correct while purchase-plan installments were synthesised in the browser,
 * and became wrong the moment plans moved to Postgres — at which point a
 * plan installment was a backend row with a server-issued UUID, sent to
 * `store.updateTransaction`, which walks an array that holds no backend
 * rows. It matched nothing, wrote nothing, and the modal closed reporting
 * success. The user watched an edit save and it never left the browser.
 *
 * Nothing failed, which is why it needs a test rather than a comment: the
 * only visible symptom was a value that did not change.
 */

/** A row as the store shapes it, reduced to what routing looks at. */
const backendId = "9f3a1c2e-4b5d-6e7f-8a9b-0c1d2e3f4a5b";
const legacyId = "tx_ky3l9x_a1b2c";

describe("finance · which writer an edit goes to", () => {
  it("sends a backend row to the backend", () => {
    expect(editWritesToBackend({ id: backendId })).toBe(true);
  });

  it("sends a plan installment to the backend", () => {
    // The regression. A plan installment carries a plan id AND a
    // server-issued uuid; belonging to a plan is not a reason to write it
    // anywhere else. This assertion fails against the old predicate.
    expect(editWritesToBackend({ id: backendId, planId: "plan_1", installmentNumber: 3 } as {
      id: string;
    })).toBe(true);
  });

  it("sends a transfer leg to the backend too", () => {
    // Same reasoning: the row lives in Postgres, so Postgres decides what
    // may be done to it. The tools refuse an amount change on a leg; that
    // refusal belongs to the domain, not to a routing predicate in a modal.
    expect(editWritesToBackend({ id: backendId, transferPairId: "pair_1" } as {
      id: string;
    })).toBe(true);
  });

  it("keeps a browser-only leftover local", () => {
    expect(editWritesToBackend({ id: legacyId })).toBe(false);
  });

  it("decides on the id and nothing else", () => {
    // Every other field is irrelevant, and stating that here is what stops
    // a future condition from being bolted on beside it.
    const withEverything = {
      id: legacyId, planId: "p", installmentNumber: 1, transferPairId: "t",
    } as { id: string };
    expect(editWritesToBackend(withEverything)).toBe(isBackendTransactionId(legacyId));
  });
});
