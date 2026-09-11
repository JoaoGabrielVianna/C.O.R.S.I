/**
 * Typed REST client for /releases.
 *
 * ── What this reads is a snapshot, not a description of the code ───────
 * Every field below was written down when a release was recorded and is
 * returned verbatim afterwards. Nothing here is computed from the running
 * build, and nothing in this module may start doing so: the moment the
 * page derives "capabilities" from what the app currently has, the history
 * stops being a history and becomes a lagging view of the present.
 *
 * ── There is no create call ────────────────────────────────────────────
 * Recording a release is not a UI action in this version. The only
 * mutation exposed is publish, which turns a recorded draft into a
 * declared release — and it is the one irreversible action in the module,
 * so it is a POST on an explicit sub-resource rather than a status field
 * someone can flip by accident.
 */

import { apiFetch } from "@/lib/api/client";

/** A module's lifecycle. Not the status of its newest release. */
export type ModuleStatus = "active" | "partial" | "frozen";

/** draft = recorded, never current. published = declared and frozen. */
export type ReleaseStatus = "draft" | "published";

/**
 * How much a release asks to be trusted — a separate question from whether
 * it has been declared. A release candidate can be published, and a stable
 * version can sit unpublished; one field for both would have to lie.
 */
export type Stability = "stable" | "beta" | "rc";

export interface ApiCapability {
  name: string;
  /** What the capability covers, and where its claim stops. */
  note?: string;
}

/** A measured fact. `value` is a string because "8/8" is as valid as "123". */
export interface ApiMetric {
  label: string;
  value: string;
}

export interface ApiNote {
  text: string;
  /** Whatever makes the line verifiable: an ADR, a test name, a file. */
  ref?: string;
}

export interface ApiDocRef {
  label: string;
  path: string;
}

export interface ApiRelease {
  id: string;
  module_key: string;
  version: string;
  status: ReleaseStatus;
  stability: Stability;
  /** Null while the release is a draft. Never invented. */
  released_at: string | null;
  summary: string;
  capabilities: ApiCapability[];
  evidence: ApiMetric[];
  limitations: ApiNote[];
  decisions: ApiNote[];
  technical_notes: ApiNote[];
  doc_refs: ApiDocRef[];
  created_at: string;
  published_at: string | null;
}

export interface ApiModuleCard {
  key: string;
  name: string;
  description: string;
  status: ModuleStatus;
  /**
   * The highest *published* version, or null. A module with only drafts
   * has no current release, and the card says so rather than showing the
   * draft as though it shipped.
   */
  current_release: ApiRelease | null;
  /** Published releases only. */
  release_count: number;
  last_released_at: string | null;
}

export interface ApiModuleDetail extends ApiModuleCard {
  /**
   * The full timeline, newest version first, drafts included and labelled.
   * Ordering is by version, not by date.
   */
  releases: ApiRelease[];
}

export async function listModules(signal?: AbortSignal): Promise<ApiModuleCard[]> {
  const res = await apiFetch<{ items: ApiModuleCard[] }>("/releases/modules", { signal });
  return res.items;
}

export function getModule(key: string, signal?: AbortSignal): Promise<ApiModuleDetail> {
  return apiFetch<ApiModuleDetail>(`/releases/modules/${encodeURIComponent(key)}`, { signal });
}

export function getRelease(
  key: string,
  version: string,
  signal?: AbortSignal,
): Promise<ApiRelease> {
  return apiFetch<ApiRelease>(
    `/releases/modules/${encodeURIComponent(key)}/releases/${encodeURIComponent(version)}`,
    { signal },
  );
}

export function publishRelease(key: string, version: string): Promise<ApiRelease> {
  return apiFetch<ApiRelease>(
    `/releases/modules/${encodeURIComponent(key)}/releases/${encodeURIComponent(version)}/publish`,
    { method: "POST" },
  );
}
