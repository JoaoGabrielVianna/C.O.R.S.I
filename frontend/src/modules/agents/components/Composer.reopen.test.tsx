// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { Composer } from "./Composer";
import type { ComposerReference } from "@/modules/agents/composer/references";
import { I18nFixture } from "@/lib/i18n";

/**
 * Reopening a menu that was closed.
 *
 * ── The bug this file exists for ───────────────────────────────────────
 * Dismissal used to be keyed on the token's QUERY. Pressing Escape on `@`
 * stored the empty string, and every later `@` — a new occurrence, typed on
 * purpose, often minutes later — matched that dismissal and silently did
 * nothing. Blur was worse: it stored `token?.query ?? ""`, so blurring an
 * empty composer poisoned the empty query for both triggers at once.
 *
 * Every case below is written against the OCCURRENCE, which is what the
 * user actually experiences: this `@`, here, now. The same cases run for
 * both triggers, because the state machine is shared and a fix that only
 * covered one would be a fix somebody undoes.
 */

const onSend = vi.fn(() => true);
const onStop = vi.fn();
const onCommand = vi.fn();

const references: ComposerReference[] = [
  {
    kind: "integration",
    id: "github",
    label: "GitHub",
    description: "repositórios, commits e arquivos",
    icon: () => null,
    keywords: ["github"],
    capabilityCount: 3,
  },
];

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
        references={references}
        {...props}
      />
    </I18nFixture>,
  );
}

const box = () => screen.getByPlaceholderText("Falar com o agente…");
const menu = () => screen.queryByRole("listbox");

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

// The two triggers are the same state machine. Running the suite twice
// rather than testing one and trusting the other is what would have caught
// this the first time.
const triggers = [
  { name: "@", char: "@" },
  { name: "/", char: "/" },
] as const;

for (const trigger of triggers) {
  describe(`reopening the ${trigger.name} menu`, () => {
    /* A */
    it("opens on the trigger", async () => {
      renderComposer();
      await userEvent.type(box(), trigger.char);
      expect(menu()).toBeTruthy();
    });

    /* B */
    it("closes on Escape without touching the draft", async () => {
      renderComposer();
      await userEvent.type(box(), trigger.char);
      await userEvent.keyboard("{Escape}");

      expect(menu()).toBeNull();
      expect((box() as HTMLTextAreaElement).value).toBe(trigger.char);
    });

    /* C — and the loop that must not happen */
    it("stays closed after Escape while nothing changes", async () => {
      renderComposer();
      await userEvent.type(box(), trigger.char);
      await userEvent.keyboard("{Escape}");

      // Moving the caret is not changing anything.
      await userEvent.click(box());
      expect(menu()).toBeNull();
    });

    /* D — the reported bug, exactly */
    it("opens again after the token is deleted and retyped", async () => {
      renderComposer();
      await userEvent.type(box(), trigger.char);
      await userEvent.keyboard("{Escape}");
      expect(menu()).toBeNull();

      await userEvent.keyboard("{Backspace}");
      await userEvent.type(box(), trigger.char);

      expect(menu()).toBeTruthy();
    });

    /* F — blur must not poison the next occurrence */
    it("opens again after a blur", async () => {
      renderComposer(
        // A second focusable target, so the blur is a real one.
        {},
      );
      await userEvent.type(box(), trigger.char);
      await userEvent.tab();
      expect(menu()).toBeNull();

      await userEvent.clear(box());
      await userEvent.type(box(), trigger.char);
      expect(menu()).toBeTruthy();
    });

    /* F, the sharper half: blurring an EMPTY composer dismissed nothing,
       and used to dismiss the empty query for every trigger. */
    it("opens after blurring an empty composer", async () => {
      renderComposer();
      await userEvent.click(box());
      await userEvent.tab();

      await userEvent.type(box(), trigger.char);
      expect(menu()).toBeTruthy();
    });

    /* G */
    it("reopens when a query with no matches is edited into one that has them", async () => {
      renderComposer();
      await userEvent.type(box(), `${trigger.char}zzzz`);
      // No matches: the menu is open and says so, or is closed — either way
      // the point is what happens after Escape and an edit.
      await userEvent.keyboard("{Escape}");
      expect(menu()).toBeNull();

      await userEvent.keyboard("{Backspace}{Backspace}{Backspace}{Backspace}");
      expect(menu()).toBeTruthy();
    });

    /* H — the sharp case: two occurrences alive at once */
    it("opens for a different occurrence the caret jumps straight into", async () => {
      // Two tokens in one draft, both spelling the same empty query. The
      // caret moves from one to the other in a single selection change, so
      // there is never a moment without a token — which is what tells an
      // occurrence apart from its text. A dismissal keyed on the query
      // would consider the second one already closed.
      if (trigger.char !== "@") return; // `/` only opens at the head.

      renderComposer();
      const el = box() as HTMLTextAreaElement;
      await userEvent.type(el, "@ @");
      await userEvent.keyboard("{Escape}");
      expect(menu()).toBeNull();

      // Straight into the first token, in one selection change. A click is
      // how a person does it, and it is the event the composer syncs the
      // caret from.
      el.setSelectionRange(1, 1);
      fireEvent.click(el);

      expect(menu()).toBeTruthy();
    });

    /* H — two occurrences that spell the same thing are still two */
    it("treats a second identical trigger later in the draft as its own occurrence", async () => {
      renderComposer();
      await userEvent.type(box(), trigger.char);
      await userEvent.keyboard("{Escape}");
      expect(menu()).toBeNull();

      // A second one, further along. `/` only opens at the head of the
      // draft, so this types a fresh draft for it rather than asserting a
      // rule that trigger does not have.
      await userEvent.clear(box());
      await userEvent.type(box(), trigger.char === "@" ? `analise ${trigger.char}` : trigger.char);

      expect(menu()).toBeTruthy();
    });

    /* J */
    it("leaves Shift+Enter as a newline while the menu is open", async () => {
      renderComposer();
      await userEvent.type(box(), trigger.char);
      await userEvent.keyboard("{Shift>}{Enter}{/Shift}");

      expect(onSend).not.toHaveBeenCalled();
      expect((box() as HTMLTextAreaElement).value).toContain("\n");
    });

    /* K */
    it("sends on Enter once the menu is closed", async () => {
      renderComposer();
      await userEvent.type(box(), `${trigger.char}x`);
      await userEvent.keyboard("{Escape}");
      await userEvent.keyboard("{Enter}");

      expect(onSend).toHaveBeenCalledTimes(1);
    });
  });
}

// One more way two occurrences can be told apart: they are different
// triggers. Replacing the whole draft in a single edit means the token
// never becomes null in between, so the trigger is the only thing that
// differs — `/` at position 0 with an empty query, then `@` at position 0
// with an empty query.
describe("switching from one trigger to the other in a single edit", () => {
  it("opens the reference menu after the command menu was dismissed", async () => {
    renderComposer();
    const el = box() as HTMLTextAreaElement;
    await userEvent.type(el, "/");
    await userEvent.keyboard("{Escape}");
    expect(menu()).toBeNull();

    // Select all and overtype: one input event, no token-less moment.
    el.setSelectionRange(0, el.value.length);
    fireEvent.change(el, { target: { value: "@", selectionStart: 1, selectionEnd: 1 } });

    expect(menu()).toBeTruthy();
  });
});

/* E — choosing something, then triggering again */
describe("reopening after a selection", () => {
  it("opens for a new @ after one was chosen", async () => {
    renderComposer();
    await userEvent.type(box(), "@");
    await userEvent.keyboard("{Enter}");
    expect(menu()).toBeNull();
    expect(screen.getByText("GitHub")).toBeTruthy();

    await userEvent.type(box(), "@");
    expect(menu()).toBeTruthy();
  });

  it("opens for a new / after a command was run", async () => {
    renderComposer();
    await userEvent.type(box(), "/");
    await userEvent.keyboard("{Enter}");
    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(menu()).toBeNull();

    await userEvent.type(box(), "/");
    expect(menu()).toBeTruthy();
  });

  /* L — a click must not be eaten by the blur it causes */
  it("chooses on click without the blur closing the menu first", async () => {
    renderComposer();
    await userEvent.type(box(), "@");
    await userEvent.click(screen.getByText("GitHub"));

    expect(screen.getAllByText("GitHub").length).toBeGreaterThan(0);
    expect(menu()).toBeNull();
  });
});

/* I — composition events are still respected */
describe("input method editors", () => {
  // The guard is `e.nativeEvent.isComposing`, which is a property of the
  // keyboard event itself and not a mode the element remembers. userEvent
  // cannot set it, so the event is dispatched directly — testing the real
  // mechanism rather than a mode this component does not have.
  it("does not send on the Enter that commits a composition", async () => {
    renderComposer();
    const el = box() as HTMLTextAreaElement;
    await userEvent.type(el, "こん");
    onSend.mockClear();

    const committing = new KeyboardEvent("keydown", { key: "Enter", bubbles: true });
    Object.defineProperty(committing, "isComposing", { value: true });
    el.dispatchEvent(committing);

    expect(onSend).not.toHaveBeenCalled();

    // And the next Enter, with no composition in flight, still sends.
    await userEvent.keyboard("{Enter}");
    expect(onSend).toHaveBeenCalledTimes(1);
  });
});
