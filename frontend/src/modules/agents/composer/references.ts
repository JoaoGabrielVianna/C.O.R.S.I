import { Wrench } from "lucide-react";
import type { ComponentType, SVGProps } from "react";

import { GithubIcon } from "@/components/ui/BrandIcons";

import { matchesQuery } from "@/lib/search";
import type { ApiTool, ToolsReport } from "@/modules/agents/api/tools";
import { groupTools } from "@/modules/agents/toolGroups";
import { activeFormat } from "@/lib/i18n";

/**
 * The capabilities a conversation's composer can attach to the next turn.
 *
 * ── Why this is not the command registry ───────────────────────────────
 * Because `/` and `@` mean different things, and the difference is the
 * whole product idea:
 *
 *	/  an action the composer carries out NOW. Choosing `/lembrar` opens
 *	   the consolidation flow before you have finished the sentence.
 *	@  a reference attached to the NEXT turn. Choosing one runs nothing,
 *	   calls nothing and sends nothing — it adds a chip.
 *
 * ── Why a row is an INTEGRATION and not a capability ───────────────────
 * It was one capability per row, and seven GitHub rows is not a menu
 * anybody wants to read: `@GitHub · Repositórios`, `@GitHub · Commits`,
 * `@GitHub · Ler commit`… The unit a person thinks in is the system, not
 * its verbs. Nobody attaches "read a file"; they attach GitHub and expect
 * it to do what GitHub does.
 *
 * The capabilities did not go anywhere. They are still what is authorized,
 * declared, executed, audited and priced — a group is a way of NAMING a set
 * of them in a menu, and the backend expands it back into that set against
 * the grants before anything is exposed. See chat/domain/reference.go.
 *
 * ── Why the list is derived and never authored here ────────────────────
 * The backend is the catalogue. This module transforms
 * `GET /chat/agents/{id}/tools` into rows; it never invents a name, a
 * description or an entry. There is no list of providers in this file, no
 * `if (namespace === "github")` deciding what exists, and an integration
 * that ships next year appears the first time its tools do — which is the
 * test `groupTools` was already written to pass, and why this reuses it
 * rather than grouping again.
 */

/** The families `@` can offer. One today, because one exists. */
export type ComposerReferenceKind = "integration";

export interface ComposerReference {
  kind: ComposerReferenceKind;
  /**
   * The canonical, machine-readable identity — for an integration, the
   * namespace its capabilities are named under (`github`). This is what
   * travels to the backend. Never a display string: the label is written
   * by whoever implements the tools and may be rewritten by a deploy, and
   * a turn's scope must not depend on how something reads today.
   */
  id: string;
  /** How it reads in the menu and on the chip. */
  label: string;
  /** One line, describing what the integration can reach. */
  description: string;
  /**
   * The glyph.
   *
   * A component rather than a name, so a future integration can supply its
   * own mark without this type learning what an integration is. Never a
   * remote asset: a menu that fetches a logo is a menu that flashes.
   */
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  /** Extra words that should find it. Never shown. */
  keywords: readonly string[];
  /**
   * How many capabilities this row stands for, on this agent, right now.
   *
   * Shown so the row is honest about being a group, and never used as
   * identity or as a permission: the backend expands the group against its
   * own grants and this number has no vote in it.
   */
  capabilityCount: number;
}

/** A stable key for a reference, used for dedupe and for React lists. */
export function referenceKey(r: Pick<ComposerReference, "kind" | "id">): string {
  return `${r.kind}:${r.id}`;
}

/**
 * The integrations of one agent, as menu rows.
 *
 * ── Why only the authorized capabilities count ─────────────────────────
 * Because the menu offers what can be attached, and attaching something
 * unauthorized is refused by the backend — correctly, since a selection may
 * never grant. A provider whose capabilities are all unauthorized is
 * therefore not a row: it would be a row that cannot be chosen, in a menu
 * whose entire purpose is choosing. Authorization stays in Settings, which
 * is where the same grouping already renders every provider including the
 * ones with nothing granted — so the place that shows "GitHub, 0 of 7"
 * exists, and it is not this one.
 */
export function referencesFromTools(report: ToolsReport | undefined): ComposerReference[] {
  if (!report) return [];
  return groupTools(report.items.filter((t) => t.authorized)).map((group) => ({
    kind: "integration" as const,
    id: group.key,
    label: group.label,
    description: describe(group.tools),
    icon: glyphFor(group.key),
    // The namespace is searchable because it is what somebody who knows the
    // system types, and every capability title comes along so that `@commit`
    // still finds GitHub — which is how a person looks for a verb when they
    // have forgotten which system provides it.
    keywords: [group.key, ...group.tools.map((t) => t.name), ...group.tools.map((t) => t.title)],
    capabilityCount: group.tools.length,
  }));
}

/**
 * One line describing what a group can reach, built from the capabilities
 * themselves.
 *
 * ── Why not a sentence per provider ────────────────────────────────────
 * Because that sentence would live here, which would make this file a
 * catalogue of integrations — the one thing it must never become. Naming
 * the capabilities is the only description that is true by construction,
 * stays true when a provider gains or loses one, and is right the first
 * time an unknown integration appears.
 */
function describe(tools: readonly { shortTitle: string }[]): string {
  const fmt = activeFormat();
  // The capability titles come from the backend and are NOT translated —
  // they are the names of real capabilities. Only the joining is language
  // work, and `Intl.ListFormat` does it correctly for both: "a, b e c"
  // against "a, b and c". Lowercasing follows the reader's locale for the
  // same reason it should never have been pinned to pt-BR.
  const names = tools.map((t) => t.shortTitle.toLocaleLowerCase(fmt.tag));
  if (names.length === 0) return "";
  if (names.length === 1) return names[0];
  return new Intl.ListFormat(fmt.tag, { style: "long", type: "conjunction" }).format(names);
}

/**
 * The mark a row is drawn with, chosen from the namespace.
 *
 * ── Why this is not a catalogue ────────────────────────────────────────
 * Because it decides nothing about what exists. It maps a namespace the
 * BACKEND already sent to a picture, and an unknown namespace falls back to
 * the generic wrench rather than being hidden or refused. Deleting this
 * function would change how the menu looks and nothing about what it
 * offers, which is the test for whether a frontend list has become a
 * second source of truth.
 */
function glyphFor(namespace: string): ComponentType<SVGProps<SVGSVGElement>> {
  switch (namespace) {
    case "github":
      return GithubIcon;
    default:
      return Wrench;
  }
}

/**
 * The references a query selects, in the order they were given.
 *
 * Identity, label, description and keywords are all searched, which is what
 * makes both `@github` and `@commits` find GitHub without the menu growing
 * a facet UI nobody asked for.
 */
export function filterReferences(
  query: string,
  references: readonly ComposerReference[],
): ComposerReference[] {
  return references.filter((r) =>
    matchesQuery([r.id, r.label, r.description, ...r.keywords], query),
  );
}

/** Kept for the transcript, which renders what a past turn actually froze. */
export type { ApiTool };
