// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nFixture } from "@/lib/i18n";

import type { ApiAgent, ApiMemoryPolicy, UpdateAgentRequest } from "@/modules/agents/api/agents";
import { MemoryPolicyCard } from "./MemoryPolicyCard";

/**
 * The Memory Policy card, at the level where its bug would live.
 *
 * ── Why this file needs a DOM and the other test file does not ─────────
 * `memoryCapture.test.ts` tests a decision expressed as a pure function, so
 * it needs nothing. What can go wrong here is a click reaching the wrong
 * state and a save sending the wrong body — neither of which is a function
 * call anybody could test in isolation. This is the smallest harness that
 * can observe it: jsdom, testing-library, one mocked API module.
 *
 * It is deliberately not the beginning of a component-testing programme.
 * The frontend still has no broad component-testing programme; this
 * covers one card, because one card was added.
 *
 * The single assertion that matters most is the last one: a save sends the
 * policy and NOTHING else. The settings form above this card saves through
 * the same route, and a body that carried extra fields would let one card
 * overwrite the other's work.
 */

const updateAgent = vi.fn<(id: string, body: UpdateAgentRequest) => Promise<ApiAgent>>();

vi.mock("@/modules/agents/api/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/agents/api/agents")>()),
  updateAgent: (id: string, body: UpdateAgentRequest) => updateAgent(id, body),
}));

function agentWith(policy: ApiMemoryPolicy): ApiAgent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    provider_id: "provider-1",
    name: "Content Agent",
    description: "",
    system_prompt: "",
    model: "test-model",
    temperature: 0.7,
    max_tokens: 4096,
    history_limit: 40,
    accent: "cyan",
    budget: { daily_token_limit: null, daily_cost_limit_usd: null },
    memory_policy: policy,
    created_at: "2026-08-13T00:00:00Z",
    updated_at: "2026-08-13T00:00:00Z",
  };
}

function renderCard(policy: ApiMemoryPolicy) {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nFixture lang="pt">
        <MemoryPolicyCard agent={agentWith(policy)} />
      </I18nFixture>
    </QueryClientProvider>,
  );
}

const onRequest = () => screen.getByRole("radio", { name: /Quando eu pedir/ });
const off = () => screen.getByRole("radio", { name: /Desligada/ });
const notes = () => screen.getByLabelText(/Orientação adicional/);
const saveButton = () => screen.getByRole("button", { name: /Salvar política/ });

afterEach(() => {
  cleanup();
  updateAgent.mockReset();
});

describe("MemoryPolicyCard", () => {
  it("shows the stored mode, on_request", () => {
    renderCard({ mode: "on_request", notes: "" });
    expect((onRequest() as HTMLInputElement).checked).toBe(true);
    expect((off() as HTMLInputElement).checked).toBe(false);
  });

  it("shows the stored mode, off, and disables the guidance with it", () => {
    renderCard({ mode: "off", notes: "guarde decisões" });
    expect((off() as HTMLInputElement).checked).toBe(true);
    // A policy that is off has nothing to guide. Leaving the box editable
    // would invite someone to write instructions nothing will ever read.
    expect((notes() as HTMLTextAreaElement).disabled).toBe(true);
    expect((notes() as HTMLTextAreaElement).value).toBe("guarde decisões");
  });

  it("says out loud that nothing is saved without confirmation", () => {
    renderCard({ mode: "on_request", notes: "" });
    // The card is where a user decides how much of their conversation may
    // become durable. The promise that a proposal is only a proposal has to
    // be on this screen, not only in a document.
    expect(screen.getByText(/depende de você confirmar/)).toBeTruthy();
    expect(screen.getByText(/sobrevive ao fim de uma conversa/)).toBeTruthy();
  });

  it("sends the mode the user picked", async () => {
    updateAgent.mockResolvedValue(agentWith({ mode: "off", notes: "" }));
    renderCard({ mode: "on_request", notes: "" });

    await userEvent.click(off());
    await userEvent.click(saveButton());

    expect(updateAgent).toHaveBeenCalledWith("agent-1", {
      memory_policy: { mode: "off", notes: "" },
    });
    expect(await screen.findByText("Salvo.")).toBeTruthy();
  });

  it("sends the guidance the user typed", async () => {
    updateAgent.mockResolvedValue(agentWith({ mode: "on_request", notes: "x" }));
    renderCard({ mode: "on_request", notes: "" });

    await userEvent.type(notes(), "guarde restrições de stack");
    await userEvent.click(saveButton());

    expect(updateAgent).toHaveBeenCalledWith("agent-1", {
      memory_policy: { mode: "on_request", notes: "guarde restrições de stack" },
    });
  });

  it("sends the policy and nothing else", async () => {
    updateAgent.mockResolvedValue(agentWith({ mode: "on_request", notes: "" }));
    renderCard({ mode: "on_request", notes: "" });

    await userEvent.click(saveButton());

    // Omission is what protects the other card: a body carrying `name` or
    // `system_prompt` here would write this card's stale copy of the form
    // over whatever was saved above it.
    const [, body] = updateAgent.mock.calls[0];
    expect(Object.keys(body)).toEqual(["memory_policy"]);
  });

  it("refuses to save guidance past the ceiling, before asking the server", async () => {
    renderCard({ mode: "on_request", notes: "a".repeat(1000) });

    await userEvent.type(notes(), "b");
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("1001/1000")).toBeTruthy();
    expect(updateAgent).not.toHaveBeenCalled();
  });

  it("keeps the draft and shows why when the save fails", async () => {
    updateAgent.mockRejectedValue(new Error("banco fora do ar"));
    renderCard({ mode: "on_request", notes: "" });

    await userEvent.type(notes(), "não perder isto");
    await userEvent.click(saveButton());

    expect(await screen.findByText(/banco fora do ar/)).toBeTruthy();
    // The words the user wrote survive the failure, as everywhere else in
    // this module: never lose what the user typed.
    expect((notes() as HTMLTextAreaElement).value).toBe("não perder isto");
    expect(screen.queryByText("Salvo.")).toBeNull();
  });
});
