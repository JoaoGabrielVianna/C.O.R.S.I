/**
 * Job Radar — domain types (v0.0.0).
 *
 * Designed to map cleanly onto the future backend:
 *
 *   collector → normalizer → postgres → API → frontend
 *
 * Every type below is plain JSON-serializable. When the collector ships, the
 * frontend swaps `localStorage` for an API client and consumes the same shapes.
 *
 * Lifecycle is binary, not staged:
 *   · `tracking === null`  → lives in Discover (raw or manual entry)
 *   · `tracking !== null`  → lives in Pipeline at one of six stages
 *
 * There is no "saved" flag separate from the pipeline — saving a discover
 * card moves it to Pipeline at stage `saved`. Untracking returns it to
 * Discover. One source of truth.
 */

/* ── Pipeline ────────────────────────────────────────────────────────── */
export type PipelineStage =
  | "saved"
  | "applied"
  | "interview"
  | "technical"
  | "offer"
  | "rejected";

export const PIPELINE_STAGE_ORDER: readonly PipelineStage[] = [
  "saved",
  "applied",
  "interview",
  "technical",
  "offer",
  "rejected",
] as const;

export type StageVisit = {
  stage: PipelineStage;
  at: number;
};

export type OpportunityTracking = {
  stage: PipelineStage;
  /** First moment this opportunity entered Pipeline (any stage). */
  trackedAt: number;
  /** Moment the current stage was entered. */
  stageEnteredAt: number;
  history: StageVisit[];
  nextAction: string;
};

/* ── Filters ─────────────────────────────────────────────────────────── */
export type WorkMode = "any" | "remote" | "hybrid" | "onsite";

export type Filters = {
  query: string;
  workMode: WorkMode;
  country: string;
  stack: string[];
  salaryMin: string;
  salaryMax: string;
};

export type SavedFilter = {
  id: string;
  name: string;
  filters: Filters;
  createdAt: number;
};

/* ── Company / Opportunity ──────────────────────────────────────────── */
export type Company = {
  id: string;
  name: string;
  domain?: string;
};

export type OpportunityNotes = {
  general: string;
  interview: string;
  technical: string;
  personal: string;
};

export type Opportunity = {
  id: string;
  companyId: string;

  role: string;
  salary: string;
  location: string;
  stack: string[];
  description: string;

  /**
   * Where this record originated.
   * Today: `"manual"` for user-entered.
   * Future collectors: `"linkedin"`, `"lever"`, `"greenhouse"`, `"ashby"`, …
   * Single source-of-truth label that the normalizer sets.
   */
  source: string;

  /** Optional link to the original posting (collector or user-provided). */
  sourceUrl?: string;

  /**
   * Optional 0–100 match score. Today: only set when a real collector or
   * normalizer computes it. We don't fabricate a value in the frontend.
   */
  matchPercent?: number | null;

  /** When the posting was first surfaced (collector timestamp, or createdAt). */
  postedAt: number;

  tracking: OpportunityTracking | null;
  notes: OpportunityNotes;

  createdAt: number;
  updatedAt: number;
};

export type JobRadarTab = "discover" | "pipeline";

export type JobRadarState = {
  companies: Company[];
  opportunities: Opportunity[];
  filters: Filters;
  savedFilters: SavedFilter[];
  /** Opportunity currently open in the centered detail modal. */
  modalId: string | null;
};

export const EMPTY_FILTERS: Filters = {
  query: "",
  workMode: "any",
  country: "",
  stack: [],
  salaryMin: "",
  salaryMax: "",
};

export const EMPTY_NOTES: OpportunityNotes = {
  general: "",
  interview: "",
  technical: "",
  personal: "",
};
