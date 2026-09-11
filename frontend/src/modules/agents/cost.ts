/**
 * Reading a usage report's cost honestly.
 *
 * The backend stopped conflating "free" with "not known": a turn
 * whose rate card could not be read carries no cost at all and is counted
 * in `unpriced_messages`, and `priced` is false exactly when that count is
 * above zero. This is the one place that turns those two fields into the
 * three states the UI actually needs to render differently.
 *
 * Plain module rather than a component file so both the rendering helpers
 * and the pages can share it.
 */

import type { UsageReport } from "@/modules/agents/api/usage";

export type CostState =
  /** Every turn in the report carries a cost. The figure is complete. */
  | "known"
  /** Some turns do. The figure is a floor, not a total. */
  | "partial"
  /** None do. There is no figure — and that is not the same as zero. */
  | "unknown";

export function costStateOf(report: UsageReport | undefined): CostState {
  if (!report) return "unknown";
  if (report.priced) return "known";
  return report.unpriced_messages < report.messages ? "partial" : "unknown";
}
