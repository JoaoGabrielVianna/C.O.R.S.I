import { describe, expect, it } from "vitest";

import type { ApiTool, ToolsReport } from "@/modules/agents/api/tools";
import { filterReferences, referenceKey, referencesFromTools } from "./references";

/**
 * The `@` menu's contract with the backend.
 *
 * ── The property under test, stated once ───────────────────────────────
 * The menu DERIVES. It never authors a name, a label or a description, it
 * never adds a row the backend did not send, and it holds no list of
 * providers. That is what makes "GitHub appears in @" a consequence of the
 * backend registering GitHub tools rather than of this module knowing what
 * GitHub is — and it is why an integration that ships next year needs no
 * change here.
 *
 * ── What a row IS ──────────────────────────────────────────────────────
 * One INTEGRATION, not one capability. The capabilities are still what is
 * authorized, declared, executed and audited; the row is a way of naming a
 * set of them, and the backend expands it back against the grants.
 */

function tool(over: Partial<ApiTool>): ApiTool {
  return {
    name: "system.echo",
    title: "Echo",
    description: "Returns the text it was given.",
    effect: "read",
    internal: false,
    schema: { properties: { text: { type: "string" } }, required: ["text"] },
    authorized: true,
    ...over,
  };
}

function report(items: ApiTool[]): ToolsReport {
  return { items, authorized_count: items.filter((t) => t.authorized).length };
}

const github = [
  tool({ name: "github.repository.list", title: "GitHub · Repositórios" }),
  tool({ name: "github.commit.list", title: "GitHub · Commits" }),
  tool({ name: "github.commit.get", title: "GitHub · Ler commit" }),
  tool({ name: "github.file.get", title: "GitHub · Ler arquivo" }),
  tool({ name: "github.code.search", title: "GitHub · Buscar código" }),
  tool({ name: "github.pull_request.list", title: "GitHub · Pull requests" }),
  tool({ name: "github.pull_request.get", title: "GitHub · Ler pull request" }),
];

const jobRadar = [
  tool({ name: "job_radar.opportunity.list", title: "Job Radar · Listar oportunidades" }),
  tool({ name: "job_radar.opportunity.get", title: "Job Radar · Ver oportunidade" }),
  tool({
    name: "job_radar.opportunity.move",
    title: "Job Radar · Mover oportunidade",
    effect: "write",
  }),
];

describe("grouping the menu by integration", () => {
  /* A */
  it("offers one row for the seven GitHub capabilities", () => {
    const rows = referencesFromTools(report(github));

    expect(rows).toHaveLength(1);
    expect(rows[0].id).toBe("github");
    expect(rows[0].label).toBe("GitHub");
    expect(rows[0].capabilityCount).toBe(7);
  });

  /* B */
  it("offers one row for Job Radar", () => {
    const rows = referencesFromTools(report(jobRadar));

    expect(rows).toHaveLength(1);
    expect(rows[0].id).toBe("job_radar");
    expect(rows[0].label).toBe("Job Radar");
  });

  /* C */
  it("offers exactly two rows for two integrations", () => {
    const rows = referencesFromTools(report([...github, ...jobRadar]));

    expect(rows.map((r) => r.id)).toEqual(["github", "job_radar"]);
  });

  /* D — the rule that keeps this file from becoming a catalogue */
  it("groups an integration nobody has heard of, with no change here", () => {
    const rows = referencesFromTools(
      report([
        tool({ name: "linear.issue.list", title: "Linear · Issues" }),
        tool({ name: "linear.issue.get", title: "Linear · Ver issue" }),
      ]),
    );

    expect(rows).toHaveLength(1);
    expect(rows[0].id).toBe("linear");
    expect(rows[0].label).toBe("Linear");
    expect(rows[0].capabilityCount).toBe(2);
  });

  /* K */
  it("gives the same identity whatever order the tools arrive in", () => {
    const forwards = referencesFromTools(report(github));
    const backwards = referencesFromTools(report([...github].reverse()));

    expect(backwards[0].id).toBe(forwards[0].id);
    expect(backwards[0].capabilityCount).toBe(forwards[0].capabilityCount);
    expect(referenceKey(backwards[0])).toBe(referenceKey(forwards[0]));
  });

  /* L — identity is the namespace, never the label */
  it("keys on the namespace and not on how the provider reads", () => {
    const rows = referencesFromTools(
      report([
        tool({ name: "github.repository.list", title: "GitHub · Repositórios" }),
        // Same provider, differently spelled title. Still one row, still
        // keyed on `github`: a display string may be rewritten by a deploy
        // and a turn's scope must not move when it is.
        tool({ name: "github.commit.list", title: "Github Enterprise · Commits" }),
      ]),
    );

    expect(rows).toHaveLength(1);
    expect(rows[0].id).toBe("github");
    expect(referenceKey(rows[0])).toBe("integration:github");
  });

  /* F — the menu never offers what the agent may not use */
  it("counts only the authorized capabilities of a provider", () => {
    const rows = referencesFromTools(
      report([
        tool({ name: "github.repository.list", title: "GitHub · Repositórios" }),
        tool({ name: "github.commit.list", title: "GitHub · Commits" }),
        tool({ name: "github.file.get", title: "GitHub · Ler arquivo", authorized: false }),
      ]),
    );

    expect(rows[0].capabilityCount).toBe(2);
    expect(rows[0].keywords).not.toContain("github.file.get");
  });

  it("omits a provider whose capabilities are all unauthorized", () => {
    // A row that cannot be chosen, in a menu whose only purpose is
    // choosing. Authorization is a different screen, and it already shows
    // every provider including the empty ones.
    const rows = referencesFromTools(
      report([
        tool({ name: "github.repository.list", title: "GitHub · Repositórios", authorized: false }),
        tool({ name: "job_radar.opportunity.list", title: "Job Radar · Listar oportunidades" }),
      ]),
    );

    expect(rows.map((r) => r.id)).toEqual(["job_radar"]);
  });

  it("describes a group from its own capabilities and not from a sentence written here", () => {
    const rows = referencesFromTools(
      report([
        tool({ name: "linear.issue.list", title: "Linear · Issues" }),
        tool({ name: "linear.project.list", title: "Linear · Projetos" }),
      ]),
    );

    // The description names what is really there. It is right the first
    // time an unknown integration appears, and it stays right when one
    // gains or loses a capability.
    expect(rows[0].description).toContain("issues");
    expect(rows[0].description).toContain("projetos");
  });

  it("returns nothing at all when there is no report yet", () => {
    expect(referencesFromTools(undefined)).toEqual([]);
    expect(referencesFromTools(report([]))).toEqual([]);
  });
});

describe("finding an integration", () => {
  const rows = referencesFromTools(report([...github, ...jobRadar]));

  it("finds it by how it reads", () => {
    expect(filterReferences("github", rows).map((r) => r.id)).toEqual(["github"]);
    expect(filterReferences("radar", rows).map((r) => r.id)).toEqual(["job_radar"]);
  });

  it("finds it by a capability, for somebody who forgot which system it is", () => {
    expect(filterReferences("commit", rows).map((r) => r.id)).toEqual(["github"]);
    expect(filterReferences("oportunidade", rows).map((r) => r.id)).toEqual(["job_radar"]);
  });

  it("finds it by the canonical name a person who knows the system types", () => {
    expect(filterReferences("github.file.get", rows).map((r) => r.id)).toEqual(["github"]);
  });

  it("matches nothing when nothing matches", () => {
    expect(filterReferences("zzzz", rows)).toEqual([]);
  });

  it("returns everything for an empty query", () => {
    expect(filterReferences("", rows)).toHaveLength(2);
  });
});
