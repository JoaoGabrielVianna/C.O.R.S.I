// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nFixture } from "@/lib/i18n";
import { MemoryRouter } from "react-router-dom";

import type { ApiAgent } from "@/modules/agents/api/agents";
import type {
  ApiConversation,
  ApiMessage,
  FinishReason,
} from "@/modules/agents/api/conversations";
import { ChatView } from "./ChatView";

/**
 * What the product offers after a turn that did not finish.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *	A CLOCK IS NOT A PERSON
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── The incident ───────────────────────────────────────────────────────
 * A 30-second router deadline killed a Palace turn that had already written
 * three memories. The backend filed it as `aborted`, which is what it also
 * files when the user presses stop, so the transcript said "Resposta
 * interrompida." to somebody who had interrupted nothing and offered them
 * no way forward. They typed "Resposta interrompida. continue" into the
 * composer — a NEW QUESTION, the exact route Safe Resume exists to remove.
 *
 * R2 split the terminal. This file is the user-visible half of that split,
 * and the two assertions are opposite on purpose:
 *
 *	deadline  →  says the reply did not finish  →  offers Continue
 *	aborted   →  says the reply was interrupted →  offers NOTHING
 *
 * The negative one matters as much as the positive. A user who pressed stop
 * asked for the turn to end, and a product that then offered to carry on
 * would be arguing with them.
 */

const streamMessage = vi.fn();
const streamResume = vi.fn();
const listMessages = vi.fn();

vi.mock("@/modules/agents/api/stream", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/agents/api/stream")>()),
  streamMessage: (...args: unknown[]) => streamMessage(...args),
  streamResume: (...args: unknown[]) => streamResume(...args),
}));

vi.mock("@/modules/agents/api/conversations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/agents/api/conversations")>()),
  listMessages: (...args: unknown[]) => listMessages(...args),
}));

const agent = { id: "agent-1", name: "Palace", model: "test-model" } as ApiAgent;
const conversation = { id: "conv-1", agent_id: "agent-1", title: "Sintética" } as ApiConversation;

/** A thread whose last answer ended for the given reason. */
function transcriptEndingIn(reason: FinishReason): ApiMessage[] {
  return [
    { id: "m1", role: "user", content: "faz a alteração e me conta", seq: 41 } as ApiMessage,
    {
      id: "m2",
      role: "assistant",
      content: "Primeira parte da resposta.",
      seq: 42,
      finish_reason: reason,
    } as ApiMessage,
  ];
}

function renderChat() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
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

beforeEach(() => {
  // jsdom implements no scrolling at all, and the transcript follows the
  // stream. A missing browser API, not a product concern.
  Element.prototype.scrollTo = vi.fn() as unknown as Element["scrollTo"];
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("what a turn that did not finish offers", () => {
  it("offers Continue after an infrastructure deadline, and resumes rather than asking again", async () => {
    listMessages.mockResolvedValue({ items: transcriptEndingIn("deadline"), limit: 500, total: 2 });
    streamResume.mockResolvedValue(undefined);
    renderChat();

    await screen.findByText("Primeira parte da resposta.");

    // The sentence does not blame the reader, and names no middleware, no
    // context and no status code.
    expect(screen.getByText(/A resposta não terminou/)).toBeTruthy();
    expect(screen.getByText("A resposta não chegou ao fim.")).toBeTruthy();

    const button = screen.getByRole("button", { name: "Continuar" });
    await userEvent.click(button);

    // ── THE ASSERTION THE INCIDENT IS ABOUT ────────────────────────────
    //
    // A resume, naming the interrupted turn. NOT a message: a message is a
    // new question, and "continue" typed into a composer is what created a
    // second Room and a second list the first time this happened.
    expect(streamResume).toHaveBeenCalledTimes(1);
    expect(streamResume.mock.calls[0][0]).toBe("conv-1");
    expect(streamResume.mock.calls[0][1]).toBe("m2");
    expect(streamMessage).not.toHaveBeenCalled();
  });

  it("offers Continue after the tool-round ceiling, with its own sentence", async () => {
    listMessages.mockResolvedValue({
      items: transcriptEndingIn("tool_round_limit"),
      limit: 500,
      total: 2,
    });
    renderChat();

    await screen.findByText("Primeira parte da resposta.");

    // The pre-existing behaviour, unchanged, and a DIFFERENT sentence: the
    // ceiling means the turn ran out of steps, not that it never finished.
    expect(screen.getByText(/Este turno parou no meio/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continuar" })).toBeTruthy();
  });

  it("offers nothing after the user stopped the turn on purpose", async () => {
    listMessages.mockResolvedValue({ items: transcriptEndingIn("aborted"), limit: 500, total: 2 });
    renderChat();

    await screen.findByText("Primeira parte da resposta.");

    expect(screen.getByText("Resposta interrompida.")).toBeTruthy();
    // No Continue. They asked for it to stop.
    expect(screen.queryByRole("button", { name: "Continuar" })).toBeNull();
    // And not the other sentence either: the reply was not left unfinished
    // by anything, it was ended.
    expect(screen.queryByText("A resposta não chegou ao fim.")).toBeNull();
  });

  it("offers nothing after a turn that simply finished", async () => {
    listMessages.mockResolvedValue({ items: transcriptEndingIn("stop"), limit: 500, total: 2 });
    renderChat();

    await screen.findByText("Primeira parte da resposta.");

    expect(screen.queryByRole("button", { name: "Continuar" })).toBeNull();
    expect(screen.queryByText("Resposta interrompida.")).toBeNull();
    expect(screen.queryByText("A resposta não chegou ao fim.")).toBeNull();
  });
});
