import type { ApiTool } from "@/modules/agents/api/tools";

/**
 * Grouping the tool catalogue by the system that provides it.
 *
 * ── Why this is derivation and not a catalogue ─────────────────────────
 * Every value below is computed from what the backend already sent. There
 * is no list of providers in this file, no `if (name === "github")`, and no
 * table mapping a namespace to a pretty name. A tool from an integration
 * that does not exist yet groups correctly the first time it appears in
 * `GET /chat/agents/{id}/tools`, without this module being edited.
 *
 * That is the same rule the `@` menu follows, and it exists for the same
 * reason: the running backend is the authority on which capabilities exist,
 * and a second list in the browser is a list that will disagree with it
 * after the first deploy.
 *
 * ── The two things it reads, and why those ─────────────────────────────
 *
 *	name    `github.commit.list` → the namespace is the machine-readable
 *	        identity of the provider. Stable, lowercase, and already
 *	        normative: see chat/domain/tool.go, where the namespace is
 *	        defined as the OWNER of the capability.
 *
 *	title   `GitHub · Commits`   → the part before the separator is the
 *	        provider as a person should read it, and the part after is the
 *	        capability. Both are written by the process that implements the
 *	        tool, which is the only thing that knows how to spell them.
 *
 * The title is a display string and the name is an identity, so the KEY
 * comes from the name and only the LABEL comes from the title. A tool whose
 * title carries no separator still groups by namespace — it just falls back
 * to a label derived from that namespace, which is worse-looking and never
 * wrong.
 */

/** The separator the backend uses between provider and capability. */
const TITLE_SEPARATOR = " · ";

export interface GroupedTool extends ApiTool {
  /**
   * The capability on its own, with the provider stripped: "Commits"
   * rather than "GitHub · Commits". The group heading already says which
   * system it is, and repeating it on every row is noise.
   *
   * Falls back to the full title when there is nothing to strip.
   */
  shortTitle: string;
}

export interface ToolGroup {
  /** The namespace. Machine-readable, stable, and what callers match on. */
  key: string;
  /** How the provider reads. Comes from the backend's own title. */
  label: string;
  tools: GroupedTool[];
}

/**
 * Groups the catalogue, preserving the backend's order.
 *
 * ── Why the order is not sorted here ───────────────────────────────────
 * The registry returns tools in name order, which puts every tool of a
 * provider together and makes the sequence identical between two reads.
 * Re-sorting would be this file inventing an ordering rule that the
 * settings page and the `@` menu would then have to agree with — and one of
 * them would eventually not.
 */
export function groupTools(items: readonly ApiTool[]): ToolGroup[] {
  const groups: ToolGroup[] = [];
  const byKey = new Map<string, ToolGroup>();

  for (const tool of items) {
    const key = namespaceOf(tool.name);
    let group = byKey.get(key);
    if (!group) {
      group = { key, label: providerLabel(tool, key), tools: [] };
      byKey.set(key, group);
      groups.push(group);
    }
    group.tools.push({ ...tool, shortTitle: capabilityLabel(tool) });
  }
  return groups;
}

/**
 * The owner of the capability: everything before the first dot.
 *
 * Exported because the transcript groups STORED references the same way,
 * and two copies of this rule would be two answers to "which provider is
 * this?" the first time somebody adjusts one. It mirrors
 * chat/domain.ToolName.Namespace, which is the normative definition.
 */
export function namespaceOf(name: string): string {
  const dot = name.indexOf(".");
  return dot === -1 ? name : name.slice(0, dot);
}

/**
 * How the provider is spelled, taken from the backend's title when it says
 * so and derived from the namespace when it does not.
 *
 * The fallback capitalises and nothing else. Trying to be cleverer — a
 * lookup that turns `github` into `GitHub` — is exactly the hardcoded
 * catalogue this file exists to avoid, and it would be wrong for the first
 * provider whose name is not a single word.
 */
function providerLabel(tool: ApiTool, key: string): string {
  return providerLabelFrom(tool.title, key);
}

/**
 * How the provider is spelled, from any title that follows the convention.
 *
 * Exported for the transcript, which has a stored title and a namespace and
 * needs the same answer the menu gave when the turn happened.
 */
export function providerLabelFrom(title: string, namespace: string): string {
  const cut = title.indexOf(TITLE_SEPARATOR);
  if (cut > 0) return title.slice(0, cut).trim();
  return namespace.charAt(0).toUpperCase() + namespace.slice(1);
}

/** The capability on its own: everything after the separator. */
function capabilityLabel(tool: ApiTool): string {
  const cut = tool.title.indexOf(TITLE_SEPARATOR);
  if (cut === -1) return tool.title;
  const rest = tool.title.slice(cut + TITLE_SEPARATOR.length).trim();
  // A title that is ONLY a prefix ("GitHub · ") would leave an empty row
  // label. Showing the whole title is uglier and readable.
  return rest === "" ? tool.title : rest;
}

/** How many tools of a group are authorized. */
export function authorizedCount(group: ToolGroup): number {
  return group.tools.filter((t) => t.authorized).length;
}
