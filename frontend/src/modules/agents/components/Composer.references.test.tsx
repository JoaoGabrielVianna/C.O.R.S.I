// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Wrench } from "lucide-react";

import { Composer } from "./Composer";
import type { ComposerReference } from "@/modules/agents/composer/references";
import { I18nFixture } from "@/lib/i18n";

/**
 * The capability menu, as a person actually operates it.
 *
 * The open/close rule is a pure test next door. What lives here is
 * everything a keyboard and a mouse can do to it — and above all the one
 * promise the batch rests on: choosing a capability attaches it and does
 * NOTHING else. No message, no command, no request.
 */

const onSend = vi.fn(() => true);
const onStop = vi.fn();
const onCommand = vi.fn();

// A row is an integration now, not a capability. What the composer does
// with one is unchanged — attach it, chip it, deduplicate it, send its
// identity — which is why these fixtures only changed shape.
const echo: ComposerReference = {
  kind: "integration",
  id: "system",
  label: "System",
  description: "echo e probe",
  icon: Wrench,
  keywords: ["system", "system.echo"],
  capabilityCount: 2,
};

const probe: ComposerReference = {
  kind: "integration",
  id: "github",
  label: "GitHub",
  description: "repositórios e commits",
  icon: Wrench,
  keywords: ["github", "github.commit.list"],
  capabilityCount: 2,
};

function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  return render(
    <I18nFixture lang="pt">
      <Composer
        onSend={onSend}
        onStop={onStop}
        isStreaming={false}
        placeholder="Falar com o agente…"
        onCommand={onCommand}
        commandContext={{ hasAgent: true }}
        references={[echo, probe]}
        {...props}
      />
    </I18nFixture>,
  );
}

const box = () => screen.getByPlaceholderText("Falar com o agente…") as HTMLTextAreaElement;
const menu = () => screen.queryByRole("listbox");
const options = () => screen.queryAllByRole("option");
const activeOption = () => options().find((o) => o.getAttribute("aria-selected") === "true");
const chips = () => screen.queryAllByRole("button", { name: /^Remover / });

beforeEach(() => {
  // jsdom implements no scrolling. A missing browser API, not a product
  // concern — the same reason the ChatView test stubs scrollTo.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("the composer capability menu", () => {
  it("opens on @ and describes what can be attached", async () => {
    renderComposer();
    await userEvent.type(box(), "@");

    expect(menu()).toBeTruthy();
    expect(screen.getByText("System")).toBeTruthy();
    expect(screen.getByText(/echo e probe/)).toBeTruthy();
  });

  it("opens after text, which a slash command never does", async () => {
    renderComposer();
    await userEvent.type(box(), "analise isso usando @");
    expect(menu()).toBeTruthy();
    expect(screen.getByRole("listbox").getAttribute("aria-label")).toBe("Integrações");
  });

  it("does not open inside an email address", async () => {
    renderComposer();
    await userEvent.type(box(), "escreva para joao@corsi.dev");
    expect(menu()).toBeNull();
  });

  it("filters by name, by identity and by description", async () => {
    renderComposer();
    // By how it reads.
    await userEvent.type(box(), "@syst");
    expect(options()).toHaveLength(1);
    expect(screen.getByText("System")).toBeTruthy();

    // By the canonical name of one of its capabilities, which is how
    // somebody looks for a verb when they have forgotten the system.
    await userEvent.clear(box());
    await userEvent.type(box(), "@github.commit");
    expect(options()).toHaveLength(1);
    expect(screen.getByText("GitHub")).toBeTruthy();

    await userEvent.clear(box());
    await userEvent.type(box(), "@zzz");
    // Said out loud rather than by disappearing.
    expect(options()).toHaveLength(0);
    expect(screen.getByText("Nenhuma integração encontrada")).toBeTruthy();
  });

  it("closes on Escape and keeps the draft", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech");
    await userEvent.keyboard("{Escape}");

    expect(menu()).toBeNull();
    expect(box().value).toBe("@ech");
    expect(chips()).toHaveLength(0);
  });

  it("moves the active option with the arrows, without moving focus", async () => {
    renderComposer();
    await userEvent.type(box(), "@");

    const first = activeOption();
    await userEvent.keyboard("{ArrowDown}");
    expect(activeOption()).not.toBe(first);
    await userEvent.keyboard("{ArrowUp}");
    expect(activeOption()).toBe(first);

    expect(document.activeElement).toBe(box());
    expect(box().getAttribute("aria-activedescendant")).toBe(activeOption()!.id);
  });

  /* ── the promise ───────────────────────────────────────────────────── */

  it("attaches on Enter and does NOT send the turn", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech{Enter}");

    expect(chips().map((c) => c.getAttribute("aria-label"))).toEqual(["Remover System"]);
    // The whole point. Enter here chose a capability; it did not answer.
    expect(onSend).not.toHaveBeenCalled();
    expect(onCommand).not.toHaveBeenCalled();
    expect(menu()).toBeNull();
    // And the person carries straight on typing.
    expect(document.activeElement).toBe(box());
  });

  it("attaches on Tab, and Shift+Enter is still a newline", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech{Tab}");
    expect(chips()).toHaveLength(1);

    await userEvent.type(box(), "@{Shift>}{Enter}{/Shift}");
    expect(onSend).not.toHaveBeenCalled();
    expect(box().value).toContain("\n");
  });

  it("attaches on click, surviving the blur the click would cause", async () => {
    renderComposer();
    await userEvent.type(box(), "@");
    await userEvent.click(screen.getByText("GitHub"));

    expect(chips().map((c) => c.getAttribute("aria-label"))).toEqual(["Remover GitHub"]);
    expect(onSend).not.toHaveBeenCalled();
  });

  it("leaves no trace of the token in the draft", async () => {
    renderComposer();
    await userEvent.type(box(), "analise os PRs @ech{Enter}");

    // `@ech` reaching the model as text would be this feature's central
    // failure: a reference that turned back into a string to be guessed at.
    expect(box().value).toBe("analise os PRs ");
    expect(box().value).not.toContain("@");
  });

  it("does not duplicate a capability that is already attached", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech{Enter}");
    await userEvent.type(box(), "@ech{Enter}");

    expect(chips()).toHaveLength(1);
  });

  it("attaches several, and removing one leaves the others", async () => {
    renderComposer();
    await userEvent.type(box(), "@syst{Enter}");
    await userEvent.type(box(), "@git{Enter}");
    expect(chips()).toHaveLength(2);

    await userEvent.click(screen.getByRole("button", { name: "Remover System" }));
    expect(chips().map((c) => c.getAttribute("aria-label"))).toEqual(["Remover GitHub"]);
  });

  it("removes the last chip on Backspace in an empty box", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech{Enter}");
    expect(box().value).toBe("");

    await userEvent.keyboard("{Backspace}");
    expect(chips()).toHaveLength(0);
  });

  it("does not eat a chip when there is text to delete", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech{Enter}");
    await userEvent.type(box(), "oi");
    await userEvent.keyboard("{Backspace}");

    expect(box().value).toBe("o");
    expect(chips()).toHaveLength(1);
  });

  /* ── sending ───────────────────────────────────────────────────────── */

  it("sends the text and the attachments, then clears both", async () => {
    renderComposer();
    await userEvent.type(box(), "@ech{Enter}");
    await userEvent.type(box(), "compara isso{Enter}");

    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend.mock.calls[0]).toEqual([
      "compara isso",
      [expect.objectContaining({ kind: "integration", id: "system" })],
    ]);
    expect(box().value).toBe("");
    // Attachments belong to the turn that went. Carrying them into the next
    // one would scope a turn nobody scoped.
    expect(chips()).toHaveLength(0);
  });

  it("keeps the attachments when the send is refused", async () => {
    // The draft is kept on refusal, and what was attached to it is part of
    // the draft. Losing it would make a refused send destroy a choice.
    const refuse = vi.fn(() => false);
    renderComposer({ onSend: refuse });
    await userEvent.type(box(), "@ech{Enter}");
    await userEvent.type(box(), "algo{Enter}");

    expect(refuse).toHaveBeenCalledTimes(1);
    expect(box().value).toBe("algo");
    expect(chips()).toHaveLength(1);
  });

  it("touches the network for nothing at all", async () => {
    // Opening, filtering and attaching are local operations over a list the
    // page already had. A menu that fetched would be a menu that stutters,
    // and one that reached the provider would make discovery cost money —
    // the cheapest thing in the product must stay free.
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    renderComposer();
    await userEvent.type(box(), "@ec");
    await userEvent.keyboard("{ArrowDown}{ArrowUp}{Enter}");

    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });

  /* ── the empty and absent cases ────────────────────────────────────── */

  it("opens and says so when the agent has nothing to attach", async () => {
    renderComposer({ references: [], emptyReferencesMessage: "Nenhuma ferramenta disponível." });
    await userEvent.type(box(), "@");

    // Better than a menu that refuses to open, which is indistinguishable
    // from a broken one.
    expect(screen.getByText("Nenhuma ferramenta disponível.")).toBeTruthy();
    expect(options()).toHaveLength(0);
  });

  it("offers no capability menu at all where none were given", async () => {
    renderComposer({ references: undefined });
    await userEvent.type(box(), "@");
    expect(menu()).toBeNull();
  });

  /* ── the two triggers do not collide ───────────────────────────────── */

  it("keeps / a command menu and @ a capability menu", async () => {
    renderComposer();

    await userEvent.type(box(), "/");
    expect(screen.getByRole("listbox").getAttribute("aria-label")).toBe("Comandos");
    expect(screen.getByText("Lembrar")).toBeTruthy();
    expect(screen.queryByText("System")).toBeNull();

    await userEvent.clear(box());
    await userEvent.type(box(), "@");
    expect(screen.getByRole("listbox").getAttribute("aria-label")).toBe("Integrações");
    expect(screen.getByText("System")).toBeTruthy();
    expect(screen.queryByText("Lembrar")).toBeNull();
  });

  it("runs a command on selection and attaches a capability on selection", async () => {
    // The contrast, pinned in one test: `/` executes, `@` does not. If
    // either promise ever flips, this is what says so.
    renderComposer();

    await userEvent.type(box(), "/lem{Enter}");
    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();

    await userEvent.type(box(), "@ech{Enter}");
    expect(onCommand).toHaveBeenCalledTimes(1); // unchanged
    expect(onSend).not.toHaveBeenCalled();
    expect(chips()).toHaveLength(1);
  });
});
