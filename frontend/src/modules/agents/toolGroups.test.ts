import { describe, expect, it } from "vitest";

import type { ApiTool } from "@/modules/agents/api/tools";
import { authorizedCount, groupTools } from "./toolGroups";

function tool(name: string, title: string, authorized = false): ApiTool {
  return {
    name,
    title,
    description: "d",
    effect: "read",
    internal: false,
    schema: { properties: { x: { type: "string" } } },
    authorized,
  };
}

/**
 * The property this file protects: grouping is DERIVED.
 *
 * Nothing in `toolGroups.ts` may know the name of a provider. The tests
 * below are written with that in mind — the GitHub cases prove the
 * derivation produces the right result for the tools that exist today, and
 * the invented-provider cases prove it did not do so by recognising them.
 */

describe("groupTools", () => {
  const catalogue = [
    tool("github.code.search", "GitHub · Buscar código"),
    tool("github.commit.get", "GitHub · Ler commit"),
    tool("github.commit.list", "GitHub · Commits"),
    tool("github.file.get", "GitHub · Ler arquivo"),
    tool("github.pull_request.get", "GitHub · Ler pull request"),
    tool("github.pull_request.list", "GitHub · Pull requests"),
    tool("github.repository.list", "GitHub · Repositórios"),
  ];

  it("puts one provider's tools under one heading", () => {
    const groups = groupTools(catalogue);
    expect(groups).toHaveLength(1);
    expect(groups[0].key).toBe("github");
    expect(groups[0].label).toBe("GitHub");
    expect(groups[0].tools).toHaveLength(7);
  });

  it("strips the provider from each row so the heading is not repeated", () => {
    const [github] = groupTools(catalogue);
    expect(github.tools.map((t) => t.shortTitle)).toEqual([
      "Buscar código",
      "Ler commit",
      "Commits",
      "Ler arquivo",
      "Ler pull request",
      "Pull requests",
      "Repositórios",
    ]);
  });

  it("keeps the identity untouched while changing only the display", () => {
    // The row still authorizes by canonical name. A grouping that rewrote
    // the identity would toggle the wrong tool.
    const [github] = groupTools(catalogue);
    expect(github.tools[0].name).toBe("github.code.search");
    expect(github.tools[0].title).toBe("GitHub · Buscar código");
  });

  it("groups a provider it has never heard of, from the same two fields", () => {
    // The test that proves this is derivation. If `groupTools` recognised
    // providers, this would fall into an "other" bucket or vanish.
    const groups = groupTools([
      tool("linear.issue.list", "Linear · Issues"),
      tool("linear.issue.get", "Linear · Ler issue"),
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0].key).toBe("linear");
    expect(groups[0].label).toBe("Linear");
    expect(groups[0].tools[0].shortTitle).toBe("Issues");
  });

  it("still groups a tool whose title carries no provider prefix", () => {
    // `system.echo` has the title "Echo". It must not disappear, and it
    // must not be dropped into somebody else's group.
    const groups = groupTools([...catalogue, tool("system.echo", "Echo")]);
    expect(groups.map((g) => g.key)).toEqual(["github", "system"]);
    const system = groups[1];
    expect(system.label).toBe("System");
    expect(system.tools[0].shortTitle).toBe("Echo");
  });

  it("preserves the backend's order, within a group and between groups", () => {
    // Registry order is name order, which is what makes two reads identical.
    // Re-sorting here would be this file inventing a rule the `@` menu would
    // then have to agree with.
    const groups = groupTools([
      tool("system.echo", "Echo"),
      tool("github.commit.list", "GitHub · Commits"),
      tool("github.code.search", "GitHub · Buscar código"),
    ]);
    expect(groups.map((g) => g.key)).toEqual(["system", "github"]);
    expect(groups[1].tools.map((t) => t.name)).toEqual([
      "github.commit.list",
      "github.code.search",
    ]);
  });

  it("returns nothing for an empty catalogue rather than inventing a group", () => {
    expect(groupTools([])).toEqual([]);
  });

  // Job Radar was the first MODULE-backed provider, arriving after this file
  // was written and without it being edited. That is the property under
  // test: a namespace this module has never heard of, with an underscore in
  // it and a two-word label, groups correctly on first sight.
  it("groups a provider it was never told about", () => {
    const groups = groupTools([
      tool("job_radar.opportunity.list", "Job Radar · Listar oportunidades"),
      tool("job_radar.opportunity.get", "Job Radar · Ver oportunidade"),
      tool("job_radar.opportunity.move", "Job Radar · Mover oportunidade"),
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].key).toBe("job_radar");
    // From the backend's own title, not from a lookup table here — which is
    // what an underscore-separated namespace would otherwise render as.
    expect(groups[0].label).toBe("Job Radar");
    expect(groups[0].tools.map((t) => t.shortTitle)).toEqual([
      "Listar oportunidades",
      "Ver oportunidade",
      "Mover oportunidade",
    ]);
  });

  it("survives a title that is only a prefix", () => {
    const [g] = groupTools([tool("weird.thing.do", "Weird · ")]);
    expect(g.tools[0].shortTitle).toBe("Weird · ");
  });
});

describe("authorizedCount", () => {
  it("counts only what is on", () => {
    const [g] = groupTools([
      tool("github.commit.list", "GitHub · Commits", true),
      tool("github.file.get", "GitHub · Ler arquivo", false),
    ]);
    expect(authorizedCount(g)).toBe(1);
  });
});
