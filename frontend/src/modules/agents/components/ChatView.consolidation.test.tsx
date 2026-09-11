// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nFixture } from "@/lib/i18n";
import { MemoryRouter } from "react-router-dom";

import type { ApiAgent } from "@/modules/agents/api/agents";
import type { ApiConversation, ApiMessage } from "@/modules/agents/api/conversations";
import { ChatView } from "./ChatView";

/**
 * What the composer does with `/lembrar`, in the real component.
 *
 * ── Why this test renders ChatView instead of calling a function ───────
 * Because the bug it guards against is a wiring bug. The parser's answer is
 * already covered by a pure test; what cannot be covered that way is
 * whether the answer is acted on — and the failure this batch inherits its
 * caution from was exactly that: a correct classification that still fell
 * through to `send`.
 *
 * So the API layer is mocked and everything above it is real: the composer,
 * the switch, the hook, the dialog. The single most important assertion in
 * this file is the negative one — `streamMessage` is never called.
 */

const streamMessage = vi.fn();
const listMessages = vi.fn();
const consolidateMemory = vi.fn();
const confirmMemoryCandidates = vi.fn();
const createMemory = vi.fn();

vi.mock("@/modules/agents/api/stream", () => ({
  streamMessage: (...args: unknown[]) => streamMessage(...args),
}));

vi.mock("@/modules/agents/api/conversations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/agents/api/conversations")>()),
  listMessages: (...args: unknown[]) => listMessages(...args),
}));

vi.mock("@/modules/agents/api/memories", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/agents/api/memories")>()),
  consolidateMemory: (...args: unknown[]) => consolidateMemory(...args),
  confirmMemoryCandidates: (...args: unknown[]) => confirmMemoryCandidates(...args),
  createMemory: (...args: unknown[]) => createMemory(...args),
}));

const agent = {
  id: "agent-1",
  name: "Content Agent",
  model: "test-model",
} as ApiAgent;

const conversation = { id: "conv-1", agent_id: "agent-1", title: "Vagas" } as ApiConversation;

const transcript: ApiMessage[] = [
  { id: "m1", role: "user", content: "quero vagas backend", seq: 41 } as ApiMessage,
  { id: "m2", role: "assistant", content: "entendido", seq: 42 } as ApiMessage,
];

function renderChat(client = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
})) {
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <ChatView conversation={conversation} agent={agent} />
        </I18nFixture>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

const composer = () => screen.getByPlaceholderText(/Falar com Content Agent/);

beforeEach(() => {
  // jsdom implements no scrolling at all, and the transcript follows the
  // stream. A missing browser API, not a product concern.
  Element.prototype.scrollTo = vi.fn() as unknown as Element["scrollTo"];
  Element.prototype.scrollIntoView = vi.fn();
  listMessages.mockResolvedValue({ items: transcript, limit: 500, total: 2 });
  consolidateMemory.mockResolvedValue({
    candidates: [{ content: "Busca vagas backend", reason: "define o alvo", duplicate_of: null }],
    considered_messages: 2,
    effective_up_to_seq: 42,
    usage: {
      model: "test-model",
      prompt_tokens: 500,
      completion_tokens: 60,
      usage_source: "provider",
      cost_usd: 0.001,
    },
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ChatView and /lembrar", () => {
  it("consolidates instead of sending, and asks about the visible snapshot", async () => {
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");

    await waitFor(() => expect(consolidateMemory).toHaveBeenCalled());
    // The seq of the last message the reader could see. Not a timestamp,
    // not "now": the ceiling the backend applies.
    expect(consolidateMemory.mock.calls[0][1]).toBe(42);
    // The assertion the whole flow exists for.
    expect(streamMessage).not.toHaveBeenCalled();
    expect(createMemory).not.toHaveBeenCalled();

    expect(await screen.findByText(/Busca vagas backend/)).toBeTruthy();
  });

  it("consolidates from the typed command with the menu dismissed", async () => {
    // The menu now intercepts Enter, so the path through
    // `readRememberCommand` is reached when the menu is not in the way:
    // Escape keeps the draft and hands Enter back to the composer. Both
    // doors lead to the same flow, and both are covered — this one was
    // only discovered because a mutation of the typed branch survived.
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar");
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect((composer() as HTMLTextAreaElement).value).toBe("/lembrar");

    await userEvent.keyboard("{Enter}");

    await waitFor(() => expect(consolidateMemory).toHaveBeenCalled());
    expect(consolidateMemory.mock.calls[0][1]).toBe(42);
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("clears the command from the composer once it is accepted", async () => {
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");
    await waitFor(() => expect(consolidateMemory).toHaveBeenCalled());

    expect((composer() as HTMLTextAreaElement).value).toBe("");
  });

  it("still captures directly when the command carries text", async () => {
    createMemory.mockResolvedValue({ id: "mem-1" });
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar prefere Go{Enter}");

    await waitFor(() => expect(createMemory).toHaveBeenCalled());
    expect(createMemory.mock.calls[0][1]).toMatchObject({ content: "prefere Go" });
    // The direct form asks the model nothing, and consolidation is not it.
    expect(consolidateMemory).not.toHaveBeenCalled();
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("sends an ordinary message that merely mentions the word", async () => {
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "preciso lembrar de comprar pão{Enter}");

    await waitFor(() => expect(streamMessage).toHaveBeenCalled());
    expect(consolidateMemory).not.toHaveBeenCalled();
  });

  it("saves only what was confirmed, through the confirmation route", async () => {
    confirmMemoryCandidates.mockResolvedValue([{ id: "mem-1" }]);
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");
    await screen.findByText(/Busca vagas backend/);

    await userEvent.click(screen.getByRole("button", { name: /Salvar memórias/ }));

    await waitFor(() => expect(confirmMemoryCandidates).toHaveBeenCalled());
    expect(confirmMemoryCandidates).toHaveBeenCalledWith("conv-1", ["Busca vagas backend"], 42);
    // Confirmation is a memory write, never a turn.
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("makes the saved memories visible without a reload", async () => {
    // Case B of the hotfix checklist, and a gap the mutation run found:
    // every other assertion here was about the request, so dropping the
    // cache invalidation after a successful save broke nothing that was
    // being watched. The property is real and stated in the hook — the
    // Memory page has to show what was just saved, exactly as a capture
    // from a message does — so it is asserted rather than assumed.
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const invalidate = vi.spyOn(client, "invalidateQueries");
    confirmMemoryCandidates.mockResolvedValue([{ id: "mem-1" }]);

    renderChat(client);
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");
    await screen.findByText(/Busca vagas backend/);
    invalidate.mockClear();

    await userEvent.click(screen.getByRole("button", { name: /Salvar memórias/ }));
    await waitFor(() => expect(confirmMemoryCandidates).toHaveBeenCalled());

    await waitFor(() =>
      expect(
        invalidate.mock.calls.some(([arg]) => {
          const key = (arg as { queryKey?: unknown[] } | undefined)?.queryKey;
          return Array.isArray(key) && key.includes("memories") && key.includes("agent-1");
        }),
      ).toBe(true),
    );
  });

  it("runs the same consolidation when the command is picked from the menu", async () => {
    renderChat();
    await screen.findByText("entendido");

    // Discovered rather than typed: "/" opens the menu, Enter takes the
    // command. The row is the whole action, so it runs — no second Enter.
    await userEvent.type(composer(), "/");
    await userEvent.click(screen.getByRole("option"));

    await waitFor(() => expect(consolidateMemory).toHaveBeenCalled());
    expect(consolidateMemory.mock.calls[0][1]).toBe(42);
    // One flow, two doors. The menu is not a second implementation.
    expect(streamMessage).not.toHaveBeenCalled();
    expect(createMemory).not.toHaveBeenCalled();
    expect(await screen.findByText(/Busca vagas backend/)).toBeTruthy();
    // And the command it typed is gone from the composer.
    expect((composer() as HTMLTextAreaElement).value).toBe("");
  });

  it("surfaces a refusal from the menu path exactly as from the typed one", async () => {
    const { ApiError } = await import("@/lib/api/client");
    consolidateMemory.mockRejectedValue(
      new ApiError(409, { error: { code: "memory_consolidation_disabled", message: "desligada para este agente" } }, "erro"),
    );
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/{Enter}");

    expect(await screen.findByText(/desligada para este agente/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /configurações do agente/ })).toBeTruthy();
    expect(streamMessage).not.toHaveBeenCalled();
  });

  /* ── the hotfix's cases ────────────────────────────────────────────── */

  it("shows an empty result as a result, with nothing technical on screen", async () => {
    // Zero candidates is a valid outcome, not an error. The dialog says so
    // and stops there — no banner, no status code, no stack, and above all
    // none of the transport's vocabulary leaking into a sentence about the
    // conversation.
    consolidateMemory.mockResolvedValue({
      candidates: [],
      considered_messages: 2,
      effective_up_to_seq: 42,
      usage: {
        model: "test-model",
        prompt_tokens: 400,
        completion_tokens: 12,
        usage_source: "provider",
        cost_usd: 0.0004,
      },
    });
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");

    expect(await screen.findByText(/Não encontrei nada nesta conversa/)).toBeTruthy();
    expect(screen.getByText(/Analisei 2 mensagens/)).toBeTruthy();
    // The symptom this hotfix was reported for, asserted as absent.
    expect(screen.queryByText(/non-JSON/i)).toBeNull();
    expect(screen.queryByText(/not JSON/i)).toBeNull();
    expect(screen.queryByText(/404/)).toBeNull();
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("keeps a real backend failure visible instead of dressing it as a result", async () => {
    // The other half of the same rule. A gateway that actually failed is an
    // error, and the fix for the empty case must not have turned every
    // non-success into a shrug.
    const { ApiError } = await import("@/lib/api/client");
    consolidateMemory.mockRejectedValue(
      new ApiError(502, { error: { code: "upstream", message: "LLM provider returned 400" } }, "erro"),
    );
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");

    expect(await screen.findByText(/LLM provider returned 400/)).toBeTruthy();
    // An upstream failure is not a policy refusal: no settings link.
    expect(screen.queryByRole("button", { name: /configurações do agente/ })).toBeNull();
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("keeps an unreadable response an error, with the status in the message", async () => {
    // Exactly what the user saw, end to end: the API answered 404 in plain
    // text because that build had no such route. It must read as a failure,
    // and it must name the status — the fact that diagnoses it.
    const { ApiError } = await import("@/lib/api/client");
    consolidateMemory.mockRejectedValue(
      new ApiError(404, null, "the server answered 404 with a body that is not JSON"),
    );
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");

    expect(await screen.findByText(/answered 404/)).toBeTruthy();
    expect(confirmMemoryCandidates).not.toHaveBeenCalled();
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("saves nothing when the dialog is dismissed", async () => {
    renderChat();
    await screen.findByText("entendido");

    await userEvent.type(composer(), "/lembrar{Enter}");
    await screen.findByText(/Busca vagas backend/);

    await userEvent.click(screen.getByRole("button", { name: "Cancelar" }));

    await waitFor(() => expect(screen.queryByText(/Busca vagas backend/)).toBeNull());
    expect(confirmMemoryCandidates).not.toHaveBeenCalled();
    expect(streamMessage).not.toHaveBeenCalled();
  });
});
