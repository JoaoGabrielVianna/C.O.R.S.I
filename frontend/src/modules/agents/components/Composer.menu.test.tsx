// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { Composer } from "./Composer";
import { I18nFixture } from "@/lib/i18n";

/**
 * The command menu, as a person actually operates it.
 *
 * The open/close rule is already a pure test; what lives here is
 * everything a keyboard and a mouse can do to it, which no pure function
 * can answer: that Escape closes without eating the draft, that Shift+Enter
 * is still a newline while the menu is open, that a click survives the blur
 * it would otherwise cause, and that choosing a command runs it once.
 */

const onSend = vi.fn(() => true);
const onStop = vi.fn();
const onCommand = vi.fn();

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
        {...props}
      />
    </I18nFixture>,
  );
}

const box = () => screen.getByPlaceholderText("Falar com o agente…");
const menu = () => screen.queryByRole("listbox");
const options = () => screen.queryAllByRole("option");
const activeOption = () => options().find((o) => o.getAttribute("aria-selected") === "true");

beforeEach(() => {
  // jsdom implements no scrolling. A missing browser API, not a product
  // concern — the same reason the ChatView test stubs scrollTo.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("the composer command menu", () => {
  it("opens on the trigger and describes the command", async () => {
    renderComposer();
    await userEvent.type(box(), "/");

    expect(menu()).toBeTruthy();
    expect(screen.getByText("Lembrar")).toBeTruthy();
    expect(screen.getByText(/Analisa esta conversa/)).toBeTruthy();
    expect(screen.getByText("/lembrar")).toBeTruthy();
  });

  it("does not open for a slash inside a sentence", async () => {
    renderComposer();
    await userEvent.type(box(), "oi /");
    expect(menu()).toBeNull();
  });

  it("does not open for a URL", async () => {
    renderComposer();
    await userEvent.type(box(), "http://");
    expect(menu()).toBeNull();
  });

  it("filters as the command is typed, and says so when nothing matches", async () => {
    renderComposer();
    await userEvent.type(box(), "/lem");
    expect(options()).toHaveLength(1);

    await userEvent.type(box(), "zzz");
    expect(options()).toHaveLength(0);
    // Said out loud rather than by disappearing: a menu that vanishes
    // mid-typing reads as a bug.
    expect(screen.getByText("Nenhum comando encontrado")).toBeTruthy();
  });

  it("closes on Escape and keeps the draft", async () => {
    renderComposer();
    await userEvent.type(box(), "/lem");
    await userEvent.keyboard("{Escape}");

    expect(menu()).toBeNull();
    // Dismissing a suggestion is not deleting what someone wrote.
    expect((box() as HTMLTextAreaElement).value).toBe("/lem");
    expect(onCommand).not.toHaveBeenCalled();
  });

  it("moves the active option with the arrows, without moving focus", async () => {
    renderComposer();
    await userEvent.type(box(), "/");

    expect(activeOption()).toBeTruthy();
    await userEvent.keyboard("{ArrowDown}");
    expect(activeOption()).toBeTruthy();
    await userEvent.keyboard("{ArrowUp}");
    expect(activeOption()).toBeTruthy();
    // The user is still typing: focus never leaves the textarea, and the
    // active row is announced through aria-activedescendant instead.
    expect(document.activeElement).toBe(box());
    expect(box().getAttribute("aria-activedescendant")).toBe(activeOption()!.id);
  });

  it("runs the command on Enter, and does not send a message", async () => {
    renderComposer();
    await userEvent.type(box(), "/lem{Enter}");

    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(onCommand.mock.calls[0][0]).toMatchObject({ id: "memory.consolidate" });
    expect(onSend).not.toHaveBeenCalled();
    // The token was an instruction and it was carried out.
    expect((box() as HTMLTextAreaElement).value).toBe("");
    expect(menu()).toBeNull();
  });

  it("runs the command on Tab", async () => {
    renderComposer();
    await userEvent.type(box(), "/lem");
    await userEvent.keyboard("{Tab}");

    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
  });

  it("leaves Shift+Enter as a newline", async () => {
    renderComposer();
    await userEvent.type(box(), "/lem");
    await userEvent.keyboard("{Shift>}{Enter}{/Shift}");

    expect(onCommand).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
    expect((box() as HTMLTextAreaElement).value).toBe("/lem\n");
  });

  it("runs the command on click, surviving the blur a click would cause", async () => {
    renderComposer();
    await userEvent.type(box(), "/");

    await userEvent.click(screen.getByRole("option"));

    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
    expect(menu()).toBeNull();
  });

  it("follows the pointer with the active row", async () => {
    renderComposer();
    await userEvent.type(box(), "/");
    await userEvent.hover(screen.getByRole("option"));
    expect(activeOption()).toBe(screen.getByRole("option"));
  });

  it("reports its state to assistive technology", async () => {
    renderComposer();
    expect(box().getAttribute("role")).toBe("combobox");
    expect(box().getAttribute("aria-expanded")).toBe("false");

    await userEvent.type(box(), "/");
    expect(box().getAttribute("aria-expanded")).toBe("true");
    expect(box().getAttribute("aria-controls")).toBe(menu()!.id);
  });

  it("shows an unavailable command as unavailable, with the reason", async () => {
    renderComposer({ commandContext: { hasAgent: false } });
    await userEvent.type(box(), "/");

    const option = screen.getByRole("option");
    expect(option.getAttribute("aria-disabled")).toBe("true");
    expect(screen.getByText("Agente indisponível")).toBeTruthy();

    await userEvent.click(option);
    expect(onCommand).not.toHaveBeenCalled();
  });

  it("still sends an ordinary message, menu or no menu", async () => {
    renderComposer();
    await userEvent.type(box(), "uma pergunta{Enter}");

    // Second argument is the attachment list, empty because nothing was
    // attached. It is asserted rather than ignored: a turn that quietly
    // carried a selection nobody made would be this batch's worst bug.
    expect(onSend).toHaveBeenCalledWith("uma pergunta", []);
    expect(onCommand).not.toHaveBeenCalled();
  });

  it("still sends the typed command through the ordinary path", async () => {
    // The menu is a way to discover the command, not the only way to run
    // it: `/lembrar algo` closes the menu and is sent as it always was.
    renderComposer();
    await userEvent.type(box(), "/lembrar prefere Go");
    expect(menu()).toBeNull();

    await userEvent.keyboard("{Enter}");
    expect(onSend).toHaveBeenCalledWith("/lembrar prefere Go", []);
    expect(onCommand).not.toHaveBeenCalled();
  });

  it("offers no menu at all where no command handler was given", async () => {
    // The compare-mode composer, and anything else that is not a
    // conversation, is left exactly as it was.
    renderComposer({ onCommand: undefined });
    await userEvent.type(box(), "/");
    expect(menu()).toBeNull();
    expect(box().getAttribute("role")).toBeNull();
  });
});
