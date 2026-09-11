import type { ApiError } from "@/lib/api/client";

/**
 * Telling a budget refusal apart from a failure.
 *
 * The backend is the authority: it answers 429 with one of these codes, and
 * the client branches on the code rather than on the sentence. Its own
 * module so the component file stays exportable as components only.
 */
const BUDGET_REFUSAL_CODES = new Set<string>([
  "token_limit_reached",
  "cost_limit_reached",
  "pricing_unavailable",
]);

export function isBudgetRefusal(error: ApiError): boolean {
  return error.status === 429 && BUDGET_REFUSAL_CODES.has(error.code);
}
