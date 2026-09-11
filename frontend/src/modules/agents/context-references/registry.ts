import { Target } from "lucide-react";
import type { ComponentType, SVGProps } from "react";

import { providerOf, type ContextReferenceType } from "./contract";

/**
 * How a reference is DRAWN — and the boundary that keeps that decision in
 * one place.
 *
 * ── The coupling this exists to prevent ────────────────────────────────
 * Without it, every surface that shows a reference grows the same branch:
 *
 *   if (ref.type === "job_radar.opportunity") return <JobRadarCard/>
 *
 * in the composer, in the transcript, in the conversation header, in the
 * sidebar. Four copies, and the day `github.repository` arrives, three of
 * them are missed. Here a provider registers once and every surface that
 * renders a reference gets it.
 *
 * ── Why it is a lookup table and not a plugin framework ────────────────
 * Because a plugin framework would be a larger thing than the problem. This
 * is a `Record<string, Presentation>` with a fallback, it is ~40 lines, and
 * the fallback is what makes it safe: a type nobody registered still
 * renders — as its label, with a generic glyph — rather than throwing or
 * disappearing. A reference the frontend has never heard of is a normal
 * state during a deploy, not an error.
 *
 * ── What a presentation may NOT decide ─────────────────────────────────
 * Anything about meaning. It supplies a glyph and a provider name; it does
 * not decide whether the reference is valid, what it points at, or whether
 * the agent may read it. Deleting this file would change how references
 * look and nothing about what they are — which is the test for whether a
 * presentation layer has quietly become a source of truth.
 */

export interface ReferencePresentation {
  /**
   * The provider as a person reads it: "Job Radar".
   *
   * Presentation only. The backend's label already carries the entity's own
   * words; this is the eyebrow above them.
   */
  providerLabel: string;
  /**
   * The glyph. A component rather than a name so a provider can supply its
   * own mark without this module learning what a provider is, and never a
   * remote asset — a card that fetches a logo is a card that flashes.
   */
  icon: ComponentType<SVGProps<SVGSVGElement>>;
}

/**
 * Registered presentations, keyed by PROVIDER rather than by full type.
 *
 * By provider because that is the level a glyph and a name belong to:
 * `job_radar.opportunity` and a future `job_radar.company` are the same
 * product with the same mark, and registering each type separately would
 * mean repeating both. A type that needs to differ can be added as a full
 * key — `lookup` checks the exact type first.
 */
const byType: Record<string, ReferencePresentation> = {};

const byProvider: Record<string, ReferencePresentation> = {
  job_radar: { providerLabel: "Job Radar", icon: Target },
};

/**
 * The fallback, used for any provider that has not registered one.
 *
 * It renders the provider's own namespace, tidied. Not a guess at a pretty
 * name: turning `job_radar` into "Job Radar" by rule would be wrong for the
 * first provider whose name is not two words, which is exactly the trap
 * `toolGroups.ts` documents for tool namespaces.
 */
function fallback(type: ContextReferenceType): ReferencePresentation {
  const provider = providerOf(type);
  return {
    providerLabel: provider.replace(/_/g, " "),
    icon: Target,
  };
}

/** The presentation for one reference type. Never throws, never returns null. */
export function presentationFor(type: ContextReferenceType): ReferencePresentation {
  return byType[type] ?? byProvider[providerOf(type)] ?? fallback(type);
}
