import { describe, expect, it } from "vitest";

import { scanAll } from "../../../scripts/i18n-scan.mjs";
import { BASELINE_TOTAL, HARDCODED_COPY_BASELINE } from "./hardcodedCopyBaseline";

/**
 * The regression gate for untranslated copy.
 *
 * ── Why a ratchet and not a clean-tree assertion ───────────────────────
 * Asserting zero would mean either migrating every remaining surface in one
 * change, or disabling the test — and a disabled test is how a codebase
 * ends up with a flagship module written entirely in one language while the
 * shell speaks another, which is precisely the state this sprint found.
 *
 * ── Why a heuristic scanner is acceptable here ─────────────────────────
 * Because it is not asked to be right about any single string. It is asked
 * to notice that a file's count moved, and a false positive that is stable
 * across runs simply sits in the baseline doing no harm. What it cannot do
 * is silently miss new copy in a file it already tracks — the per-file
 * numbers are what make that visible.
 *
 * The one thing this cannot catch is copy added to a file the scanner does
 * not consider user-facing at all — a string thrown from a hook, say. That
 * limitation is accepted rather than papered over with a noisier gate:
 * a gate that shouts about `className` is a gate someone turns off.
 */
describe("i18n · hardcoded copy ratchet", () => {
  const report = scanAll();
  const counts: Record<string, number> = Object.fromEntries(
    Object.entries(report).map(([file, hits]) => [file, (hits as string[]).length]),
  );

  it("adds no untranslated copy to a file that had none", () => {
    const newlyOffending = Object.keys(counts)
      .filter((file) => !(file in HARDCODED_COPY_BASELINE))
      .sort();

    expect(
      newlyOffending,
      "These files gained hardcoded user-facing copy. Internationalization is " +
        "part of the Definition of Done: move the strings into `lib/i18n/pt.ts` " +
        "and `en.ts` rather than adding them to the baseline.",
    ).toEqual([]);
  });

  it("does not grow the count in a file that still has some", () => {
    const grown = Object.entries(counts)
      .filter(([file, n]) => file in HARDCODED_COPY_BASELINE && n > HARDCODED_COPY_BASELINE[file])
      .map(([file, n]) => `${file}: ${HARDCODED_COPY_BASELINE[file]} → ${n}`)
      .sort();

    expect(grown, "Hardcoded copy grew in these files.").toEqual([]);
  });

  it("keeps the baseline honest when a file is migrated", () => {
    // A file listed in the baseline that the scanner no longer reports, or
    // reports fewer strings for, is progress — and the baseline should be
    // lowered to lock it in. Failing here is a chore, not a bug, and it is
    // the mechanism that stops the ratchet from rusting open.
    const stale = Object.entries(HARDCODED_COPY_BASELINE)
      .filter(([file, n]) => (counts[file] ?? 0) < n)
      .map(([file, n]) => `${file}: baseline ${n}, actual ${counts[file] ?? 0} — lower it`)
      .sort();

    expect(stale).toEqual([]);
  });

  it("tracks the total, so a broad regression is one number", () => {
    const total = Object.values(counts).reduce((a, b) => a + b, 0);
    expect(total).toBeLessThanOrEqual(BASELINE_TOTAL);
  });
});
