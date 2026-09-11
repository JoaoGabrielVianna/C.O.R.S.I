import { describe, expect, it } from "vitest";

import type { ApiContextReport } from "@/modules/agents/api/conversations";
import { blockLabel, blockRows, exclusionRows, reasonLabel } from "@/modules/agents/contextReport";

/**
 * What the Inspector says about a turn that reused earlier evidence.
 *
 * The module decides nothing — every fact comes from the backend's report —
 * so what is testable here is exactly what it is responsible for: labelling,
 * ordering, and not dropping a block it was not taught about.
 */

function report(blocks: ApiContextReport["blocks"]): ApiContextReport {
  const total = blocks.reduce((n, b) => n + b.estimated_tokens, 0);
  return {
    blocks,
    total_characters: blocks.reduce((n, b) => n + b.characters, 0),
    total_estimated_tokens: total,
  };
}

function block(
  kind: ApiContextReport["blocks"][number]["kind"],
  characters: number,
  exclusions?: ApiContextReport["blocks"][number]["exclusions"],
): ApiContextReport["blocks"][number] {
  return {
    kind,
    items: 1,
    characters,
    estimated_tokens: Math.ceil(characters / 4),
    exclusions,
  };
}

describe("replayed tool evidence", () => {
  it("is shown between what the operator curated and the conversation", () => {
    // The position is the claim about its authority: below memory and
    // sources, which the operator decided, and above the conversation,
    // because it is what was observed before anything was said about it.
    const rows = blockRows(
      report([
        block("history", 400),
        block("tool_evidence", 800),
        block("memory", 100),
        block("instructions", 200),
      ]),
    );
    expect(rows.map((r) => r.kind)).toEqual([
      "instructions",
      "memory",
      "tool_evidence",
      "history",
    ]);
  });

  it("is never labelled as this turn's tool results", () => {
    // Two blocks, two lifetimes: one ran now, the other is history. A
    // reader who could not tell them apart could not answer "did this turn
    // actually run anything?".
    expect(blockLabel("tool_evidence")).not.toBe(blockLabel("tool_results"));
    expect(blockLabel("tool_evidence")).toBeTruthy();
  });

  it("names every reason the backend can send for it", () => {
    // A reason with no label renders as its raw code, which is legible but
    // is the client admitting it is behind the backend.
    for (const reason of ["truncated", "failed", "superseded"] as const) {
      expect(reasonLabel(reason)).not.toBe(reason);
    }
  });

  it("reports a truncation as an exclusion of the evidence block", () => {
    const rows = exclusionRows(
      report([
        block("tool_evidence", 5000, [{ reason: "truncated", items: 0, characters: 900 }]),
      ]),
    );
    expect(rows).toHaveLength(1);
    expect(rows[0].kind).toBe("tool_evidence");
    // Items is zero on purpose: nothing was left out, a tail was. The row
    // has to carry the characters, or the Inspector reports a loss of
    // nothing.
    expect(rows[0].items).toBe(0);
    expect(rows[0].characters).toBe(900);
  });

  it("still hides a degradation from the exclusion list", () => {
    // `unavailable` is surfaced as a warning instead, and this holds for the
    // new block the same way it holds for memory.
    const r = report([
      block("tool_evidence", 0, [{ reason: "unavailable", items: 0, characters: 0 }]),
    ]);
    expect(exclusionRows(r)).toHaveLength(0);
  });
});
