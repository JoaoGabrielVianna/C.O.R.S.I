/**
 * Typed REST client for /finance/recurring-entries.
 *
 * A recurring entry is money that REPEATS, in either direction — a rent, a
 * subscription, and equally a salary. It is not a payment: creating one
 * writes nothing to /finance/transactions, by design. See
 * backend/internal/finance/domain/recurringentry.go.
 *
 * The direction is not a field here: it comes from the category, exactly as
 * a transaction's type does.
 *
 * Amounts are integer cents on the wire, like every other amount in this
 * module.
 */

import { apiFetch, type PageEnvelope } from "@/lib/api/client";

export type RecurringFrequencyWire = "monthly" | "annual";
export type RecurringStatusWire = "active" | "paused";

export interface ApiRecurringEntry {
  id: string;
  workspace_id: string;
  description: string;
  amount_cents: number;
  category_id: string;
  person_id: string | null;
  due_day: number;
  recurrence: RecurringFrequencyWire;
  status: RecurringStatusWire;
  starts_at: string;
  ends_at?: string | null;
  notes: string;
  created_at: string;
  updated_at: string;
}

export interface CreateRecurringEntryRequest {
  description: string;
  amount_cents: number;
  category_id: string;
  person_id?: string | null;
  due_day: number;
  recurrence?: RecurringFrequencyWire;
  starts_at?: string;
  notes?: string;
}

export interface UpdateRecurringEntryRequest {
  description?: string;
  amount_cents?: number;
  category_id?: string;
  person_id?: string | null;
  clear_person_id?: boolean;
  due_day?: number;
  recurrence?: RecurringFrequencyWire;
  status?: RecurringStatusWire;
  ends_at?: string | null;
  clear_ends_at?: boolean;
  notes?: string;
}

export async function listRecurringEntries(): Promise<ApiRecurringEntry[]> {
  const env = await apiFetch<PageEnvelope<ApiRecurringEntry>>(
    `/finance/recurring-entries?limit=100`,
  );
  return env.items;
}

export function createRecurringEntry(body: CreateRecurringEntryRequest): Promise<ApiRecurringEntry> {
  return apiFetch<ApiRecurringEntry>(`/finance/recurring-entries`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateRecurringEntry(
  id: string,
  body: UpdateRecurringEntryRequest,
): Promise<ApiRecurringEntry> {
  return apiFetch<ApiRecurringEntry>(`/finance/recurring-entries/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteRecurringEntry(id: string): Promise<void> {
  return apiFetch<void>(`/finance/recurring-entries/${id}`, { method: "DELETE" });
}

/**
 * The monthly recurring reading.
 *
 * Its own endpoint, not a field on /transactions/totals: that contract
 * classifies rows in `finance.transactions`, and a recurring entry is not
 * one. The two are presented side by side and never summed — an entry
 * already paid this month is also a transaction.
 *
 * Income and expense come back APART, because "how much comes in" and "how
 * much goes out" are two facts a single net figure would hide.
 */
export interface ApiRecurringLine {
  id: string;
  description: string;
  category_id: string;
  direction: "income" | "expense";
  amount_cents: number;
  monthly_cents: number;
  recurrence: RecurringFrequencyWire;
  due_day: number;
}

export interface ApiRecurringSummary {
  at: string;
  income_monthly_cents: number;
  expense_monthly_cents: number;
  net_monthly_cents: number;
  items: ApiRecurringLine[];
}

export function getRecurringSummary(): Promise<ApiRecurringSummary> {
  return apiFetch<ApiRecurringSummary>(`/finance/recurring-entries/summary`);
}
