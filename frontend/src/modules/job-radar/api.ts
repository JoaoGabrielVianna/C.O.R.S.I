/**
 * Job Radar API client.
 *
 * ── Why this file translates timestamps ────────────────────────────────
 * The backend speaks RFC 3339 strings, because that is what a JSON API
 * over Postgres should speak. The Job Radar screens were written against
 * epoch milliseconds and do arithmetic on them (`b.postedAt - a.postedAt`,
 * `daysBetween(stageEnteredAt, now)`). Converting here, at the boundary,
 * keeps both sides idiomatic and means the 2.400 lines of components that
 * already work did not have to be rewritten to change where the data comes
 * from.
 *
 * The domain shapes in `types.ts` are unchanged, and that is deliberate:
 * they were designed for exactly this migration.
 */

import { apiFetch } from "@/lib/api/client";
import type {
  Opportunity,
  OpportunityNotes,
  PipelineStage,
  StageVisit,
} from "@/pages/app/modules/job-radar/types";
import { EMPTY_NOTES } from "@/pages/app/modules/job-radar/types";

/* ── wire shapes ─────────────────────────────────────────────────────── */

interface WireTracking {
  stage: PipelineStage;
  tracked_at: string;
  stage_entered_at: string;
  next_action: string;
}

interface WireOpportunity {
  id: string;
  company_id: string;
  company_name: string;
  role: string;
  salary: string;
  location: string;
  stack: string[] | null;
  description: string;
  source: string;
  source_url?: string;
  match_percent: number | null;
  posted_at: string;
  tracking: WireTracking | null;
  notes: OpportunityNotes;
  created_at: string;
  updated_at: string;
}

interface WireStageEvent {
  id: string;
  from_stage: PipelineStage | null;
  to_stage: PipelineStage;
  occurred_at: string;
}

export interface WireCompany {
  id: string;
  name: string;
  domain?: string;
}

/* ── conversion ──────────────────────────────────────────────────────── */

/**
 * An unparseable or absent timestamp becomes 0 rather than NaN.
 *
 * NaN propagates silently through every comparison — a NaN `postedAt` makes
 * a sort non-deterministic and a NaN day count renders as "NaN days" on the
 * card. Zero is wrong in an obvious, bounded way; NaN is wrong in a way
 * that corrupts whatever touches it.
 */
function toMillis(iso: string | null | undefined): number {
  if (!iso) return 0;
  const ms = Date.parse(iso);
  return Number.isNaN(ms) ? 0 : ms;
}

/**
 * `history` is left empty by the listing on purpose: the server returns a
 * record's transitions only from the by-id read, because a board showing
 * forty cards has no use for four hundred events. The store fills it in
 * when the detail modal opens.
 */
export function toOpportunity(w: WireOpportunity): Opportunity {
  return {
    id: w.id,
    companyId: w.company_id,
    role: w.role,
    salary: w.salary,
    location: w.location,
    stack: w.stack ?? [],
    description: w.description,
    source: w.source,
    sourceUrl: w.source_url || undefined,
    matchPercent: w.match_percent,
    postedAt: toMillis(w.posted_at),
    tracking: w.tracking
      ? {
          stage: w.tracking.stage,
          trackedAt: toMillis(w.tracking.tracked_at),
          stageEnteredAt: toMillis(w.tracking.stage_entered_at),
          history: [],
          nextAction: w.tracking.next_action,
        }
      : null,
    notes: { ...EMPTY_NOTES, ...(w.notes ?? {}) },
    createdAt: toMillis(w.created_at),
    updatedAt: toMillis(w.updated_at),
  };
}

function toHistory(events: WireStageEvent[]): StageVisit[] {
  return events.map((e) => ({ stage: e.to_stage, at: toMillis(e.occurred_at) }));
}

/* ── reads ───────────────────────────────────────────────────────────── */

/**
 * The listing ceiling. It matches the repository's own maximum, so asking
 * for more would silently receive less — a page that believed it had
 * everything while the server had capped it.
 */
export const LIST_LIMIT = 200;

export async function listOpportunities(): Promise<{
  items: Opportunity[];
  total: number;
}> {
  const res = await apiFetch<{ items: WireOpportunity[]; total: number }>(
    `/job-radar/opportunities?limit=${LIST_LIMIT}`,
  );
  return { items: (res.items ?? []).map(toOpportunity), total: res.total ?? 0 };
}

export async function listCompanies(): Promise<WireCompany[]> {
  const res = await apiFetch<{ items: WireCompany[] }>("/job-radar/companies");
  return res.items ?? [];
}

export async function getOpportunity(
  id: string,
): Promise<{ opportunity: Opportunity; history: StageVisit[] }> {
  const res = await apiFetch<{
    opportunity: WireOpportunity;
    history: WireStageEvent[];
  }>(`/job-radar/opportunities/${id}`);
  return {
    opportunity: toOpportunity(res.opportunity),
    history: toHistory(res.history ?? []),
  };
}

/* ── writes ──────────────────────────────────────────────────────────── */

export interface CreateBody {
  company: string;
  role: string;
  salary?: string;
  location?: string;
  stack?: string[];
  description?: string;
  source?: string;
  source_url?: string;
}

export async function createOpportunity(body: CreateBody): Promise<Opportunity> {
  return toOpportunity(
    await apiFetch<WireOpportunity>("/job-radar/opportunities", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

/**
 * The patch surface, expressed in wire names.
 *
 * `stage` is absent, and that is the contract rather than an omission: a
 * stage changes through `moveOpportunity`, which records the transition and
 * resets the stage clock. A PATCH that could also set it would be a second
 * path to the same state with none of the bookkeeping.
 */
export interface UpdateBody {
  company?: string;
  role?: string;
  salary?: string;
  location?: string;
  stack?: string[];
  description?: string;
  source_url?: string;
  next_action?: string;
  notes?: OpportunityNotes;
  /**
   * The record's own clock, in epoch milliseconds.
   *
   * Carried because the records being migrated have been sitting in their
   * stages for weeks. Letting the server stamp `now()` would reset every
   * clock at once, and "12 days in Applied" — the number the board exists
   * to show — would read 0 for the whole pipeline on migration day.
   */
  created_at?: number;
  updated_at?: number;
  /** Where the record has been, oldest first. */
  history?: { stage: string; at: number }[];
}

export async function updateOpportunity(
  id: string,
  body: UpdateBody,
): Promise<Opportunity> {
  return toOpportunity(
    await apiFetch<WireOpportunity>(`/job-radar/opportunities/${id}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  );
}

export async function moveOpportunity(
  id: string,
  stage: PipelineStage,
): Promise<Opportunity> {
  const res = await apiFetch<{ opportunity: WireOpportunity }>(
    `/job-radar/opportunities/${id}/move`,
    { method: "POST", body: JSON.stringify({ stage }) },
  );
  return toOpportunity(res.opportunity);
}

export async function untrackOpportunity(id: string): Promise<Opportunity> {
  return toOpportunity(
    await apiFetch<WireOpportunity>(`/job-radar/opportunities/${id}/untrack`, {
      method: "POST",
    }),
  );
}

export async function deleteOpportunity(id: string): Promise<void> {
  await apiFetch<void>(`/job-radar/opportunities/${id}`, { method: "DELETE" });
}

/* ── import ──────────────────────────────────────────────────────────── */

export interface ImportItem {
  /**
   * The id the record already had in the browser document.
   *
   * This is the import's identity, and the reason a repeated import is a
   * no-op instead of a duplicate. It is deliberately not company+role:
   * two genuinely different postings for the same role at the same
   * employer exist, and any heuristic over what a posting SAYS would
   * collapse them into one.
   */
  legacy_id: string;
  company: string;
  /** The employer's website, when the document knew one. */
  company_domain?: string;
  role: string;
  salary?: string;
  location?: string;
  stack?: string[];
  description?: string;
  source?: string;
  source_url?: string;
  match_percent?: number | null;
  posted_at?: number;
  stage?: PipelineStage | "discover";
  tracked_at?: number;
  stage_entered_at?: number;
  next_action?: string;
  notes?: OpportunityNotes;
  /**
   * The record's own clock, in epoch milliseconds.
   *
   * Carried because the records being migrated have been sitting in their
   * stages for weeks. Letting the server stamp `now()` would reset every
   * clock at once, and "12 days in Applied" — the number the board exists
   * to show — would read 0 for the whole pipeline on migration day.
   */
  created_at?: number;
  updated_at?: number;
  /** Where the record has been, oldest first. */
  history?: { stage: string; at: number }[];
}

export interface ImportResult {
  created_count: number;
  /**
   * Records the server recognised as already migrated.
   *
   * Present so a second run reads as the no-op it is. Without it a retry
   * would report "0 created" and look like a failure.
   */
  already_imported_count: number;
  already_imported: { index: number; legacy_id: string; id: string }[];
  failed_count: number;
  failed: { index: number; company: string; role: string; error: string }[];
}

export async function importOpportunities(
  items: ImportItem[],
): Promise<ImportResult> {
  return apiFetch<ImportResult>("/job-radar/opportunities/import", {
    method: "POST",
    body: JSON.stringify({ items }),
    // The default 15s ceiling is a per-request bound sized for a single
    // read. An import is one request carrying a whole pipeline, so it gets
    // a budget that matches what it actually does.
    timeoutMs: 60_000,
  });
}
