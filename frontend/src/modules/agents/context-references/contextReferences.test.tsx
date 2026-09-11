// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import { ContextReferenceCard, ContextReferenceList } from "@/modules/agents/components/ContextReferenceCard";
import { contextReferenceKey, providerOf, toInput } from "./contract";
import { presentationFor } from "./registry";
import { resolveAgent } from "@/modules/job-radar/talkToAgent";
import type { ApiAgent } from "@/modules/agents/api/agents";
import { I18nFixture } from "@/lib/i18n";

afterEach(cleanup);

/* ── the contract ────────────────────────────────────────────────────── */

describe("the cross-channel contract", () => {
  it("derives the provider from the type", () => {
    expect(providerOf("job_radar.opportunity")).toBe("job_radar");
    expect(providerOf("github.repository")).toBe("github");
    // A malformed type is not a crash; it is its own provider.
    expect(providerOf("nonsense")).toBe("nonsense");
  });

  it("keys on identity and never on the label", () => {
    const a = { type: "job_radar.opportunity", id: "1", label: "Acme · Backend" };
    const b = { type: "job_radar.opportunity", id: "1", label: "renamed since" };
    expect(contextReferenceKey(a)).toBe(contextReferenceKey(b));
  });

  // What travels to the backend is identity ONLY. If this ever started
  // carrying the label, a client could write into the model's context.
  it("sends identity only", () => {
    const sent = toInput({
      type: "job_radar.opportunity",
      id: "3e83eeca",
      label: "Acme · Backend Engineer",
      subtitle: "Remote (BR)",
    } as never);
    expect(sent).toEqual({ type: "job_radar.opportunity", id: "3e83eeca" });
    expect(Object.keys(sent)).toHaveLength(2);
  });
});

/* ── the renderer registry ───────────────────────────────────────────── */

describe("the renderer registry", () => {
  it("knows the providers that registered", () => {
    expect(presentationFor("job_radar.opportunity").providerLabel).toBe("Job Radar");
  });

  // A type nobody registered is a normal state during a deploy, not an
  // error. It must still render.
  it("falls back for a provider it has never heard of", () => {
    const p = presentationFor("calendar.event");
    expect(p.providerLabel).toBe("calendar");
    expect(p.icon).toBeTruthy();
  });

  it("does not throw on a malformed type", () => {
    expect(() => presentationFor("")).not.toThrow();
  });
});

/* ── the card ────────────────────────────────────────────────────────── */

describe("the reference card", () => {
  const reference = {
    type: "job_radar.opportunity",
    id: "3e83eeca-55d4-4355-82fb-7f803f10da45",
    label: "Acme · Backend Engineer",
    subtitle: "Remote (BR)",
  };

  it("shows the entity and its provider", () => {
    render(<I18nFixture lang="pt"><ContextReferenceCard reference={reference} /></I18nFixture>);
    expect(screen.getByText("Acme · Backend Engineer")).toBeTruthy();
    expect(screen.getByText("Job Radar")).toBeTruthy();
    expect(screen.getByText("Remote (BR)")).toBeTruthy();
  });

  // The semantics are the type and the id, not the words. A test that
  // asserted only on the label would pass for a card showing the right
  // text about the wrong row.
  it("carries the identity in the DOM, not just the label", () => {
    const { container } = render(<I18nFixture lang="pt"><ContextReferenceCard reference={reference} /></I18nFixture>);
    const el = container.querySelector("[data-reference-type]");
    expect(el?.getAttribute("data-reference-type")).toBe("job_radar.opportunity");
    expect(el?.getAttribute("data-reference-id")).toBe(reference.id);
  });

  // A conversation outlives its subject. The card says so rather than
  // vanishing, because removing it would rewrite what was discussed.
  it("says so when the entity is gone, keeping the historical label", () => {
    render(<I18nFixture lang="pt"><ContextReferenceCard reference={{ ...reference, unavailable: true }} /></I18nFixture>);
    expect(screen.getByText("Acme · Backend Engineer")).toBeTruthy();
    expect(screen.getByText(/não está mais disponível/i)).toBeTruthy();
    // The subtitle is not shown beside "unavailable": even a stable fact
    // reads as a current one about something that no longer exists.
    expect(screen.queryByText("Remote (BR)")).toBeNull();
  });

  it("renders an unknown provider rather than hiding it", () => {
    render(
      <I18nFixture lang="pt">
        <ContextReferenceCard
          reference={{ type: "calendar.event", id: "e1", label: "Entrevista Acme" }}
        />
      </I18nFixture>,
    );
    expect(screen.getByText("Entrevista Acme")).toBeTruthy();
    expect(screen.getByText("calendar")).toBeTruthy();
  });
});

describe("the reference list", () => {
  // A thread started from the composer must look exactly as it did before
  // this feature existed.
  it("renders nothing when there is nothing attached", () => {
    const { container } = render(<ContextReferenceList references={undefined} />);
    expect(container.innerHTML).toBe("");
    const empty = render(<ContextReferenceList references={[]} />);
    expect(empty.container.innerHTML).toBe("");
  });

  it("renders one card per subject", () => {
    render(
      <I18nFixture lang="pt">
        <ContextReferenceList
          references={[
            { type: "job_radar.opportunity", id: "1", label: "Acme · Backend" },
            { type: "job_radar.opportunity", id: "2", label: "Stripe · Infra" },
          ]}
        />
      </I18nFixture>,
    );
    expect(screen.getByText("Acme · Backend")).toBeTruthy();
    expect(screen.getByText("Stripe · Infra")).toBeTruthy();
  });
});

/* ── choosing the agent ──────────────────────────────────────────────── */

const agent = (id: string, name: string) => ({ id, name }) as ApiAgent;

describe("resolving which agent to open", () => {
  // The whole point of the picker: nothing is chosen by name, ever.
  it("uses the only agent when there is one", () => {
    const r = resolveAgent([agent("a", "Scout")], null);
    expect(r).toEqual({ agent: agent("a", "Scout") });
  });

  it("asks when there is more than one and nothing is remembered", () => {
    const r = resolveAgent([agent("a", "Scout"), agent("b", "Outro")], null);
    expect(r).toEqual({ mustChoose: true });
  });

  it("uses the remembered choice", () => {
    const r = resolveAgent([agent("a", "Scout"), agent("b", "Outro")], "b");
    expect(r).toEqual({ agent: agent("b", "Outro") });
  });

  // A remembered id that no longer exists — deleted agent, different
  // workspace — must fall back to asking rather than to a failed request.
  it("ignores a remembered agent that is gone", () => {
    const r = resolveAgent([agent("a", "Scout"), agent("b", "Outro")], "deleted");
    expect(r).toEqual({ mustChoose: true });
  });

  it("asks when there are no agents at all", () => {
    expect(resolveAgent([], null)).toEqual({ mustChoose: true });
  });

  // The guard against the heuristic this design exists to avoid: an agent
  // literally named "Scout" gets no special treatment.
  it("gives no privilege to an agent called Scout", () => {
    const r = resolveAgent([agent("a", "Outro"), agent("b", "Scout")], null);
    expect(r).toEqual({ mustChoose: true });
  });
});
