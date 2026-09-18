/**
 * Backend vocabulary, rendered as copy: the label maps.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE IDENTIFIER NEVER REACHES THE SCREEN. THE LABEL NEVER REACHES THE API
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `kind`, `status`, `sensitivity` and `confidence` arrive as closed
 * vocabulary — they are identifiers the backend enforces, not words. This
 * module is the one place that maps an identifier to a translated label,
 * and the mapping only ever runs outward.
 *
 * ── What is NOT translated, ever ───────────────────────────────────────
 * The operator's own content: a room's name, an artifact's title or body,
 * a memory's content or summary, an item's text. Switching language must
 * not rewrite what somebody wrote, and there is no code path here that
 * could.
 *
 * ── Why the maps are exhaustive records ────────────────────────────────
 * `Record<ArtifactKind, string>` rather than a lookup with a fallback. A
 * fifth kind added to the domain then fails the build here instead of
 * rendering a raw identifier to a person.
 */

import { useT } from "@/lib/i18n";

import type {
  ArtifactKind,
  MemoryConfidence,
  MemoryKind,
  PalaceStatus,
} from "../api/types";

export function useArtifactKindLabel(): (kind: ArtifactKind) => string {
  const t = useT();
  const labels: Record<ArtifactKind, string> = {
    project: t.app.palace.kinds.project,
    list: t.app.palace.kinds.list,
    plan: t.app.palace.kinds.plan,
    note: t.app.palace.kinds.note,
  };
  return (kind) => labels[kind];
}

export function useMemoryKindLabel(): (kind: MemoryKind) => string {
  const t = useT();
  const labels: Record<MemoryKind, string> = {
    fact: t.app.palace.memoryKinds.fact,
    preference: t.app.palace.memoryKinds.preference,
    idea: t.app.palace.memoryKinds.idea,
    decision: t.app.palace.memoryKinds.decision,
    learning: t.app.palace.memoryKinds.learning,
    reflection: t.app.palace.memoryKinds.reflection,
  };
  return (kind) => labels[kind];
}

export function useConfidenceLabel(): (confidence: MemoryConfidence) => string {
  const t = useT();
  const labels: Record<MemoryConfidence, string> = {
    low: t.app.palace.confidence.low,
    medium: t.app.palace.confidence.medium,
    high: t.app.palace.confidence.high,
  };
  return (confidence) => labels[confidence];
}

export function useStatusLabel(): (status: PalaceStatus) => string {
  const t = useT();
  const labels: Record<PalaceStatus, string> = {
    active: t.app.palace.status.active,
    archived: t.app.palace.status.archived,
  };
  return (status) => labels[status];
}
