import { describe, expect, it } from "vitest";

import {
  REMEMBER_COMMAND,
  readRememberCommand,
  rememberIntentHint,
} from "@/modules/agents/memoryCapture";

/**
 * The capture command, at the only level where it can be tested.
 *
 * `/lembrar` is a frontend concept end to end: the backend has no notion of
 * it, and its whole promise — the command never becomes a turn — is decided
 * here, before any request exists. `TestSavingAMemoryNeverCallsTheProvider`
 * holds the other half, that the endpoint the command calls instead never
 * reaches the LLM port.
 *
 * The regression these tests exist for: `readRememberCommand` used to answer
 * with `null` both for "this is not the command" and for "this is the command
 * with nothing to store", and the caller could only read that as "ordinary
 * message". So `/lembrar` on its own was sent to the provider, which answered
 * that it cannot save anything. The three-way answer is what makes that
 * collapse impossible to write by accident.
 *
 * The third case has since changed meaning — the bare command consolidates
 * the conversation rather than reporting a missing argument — and the tests
 * changed with it. What did not change is the property underneath: whatever
 * follows the command token, the draft is never a message to the model.
 */

describe("readRememberCommand", () => {
  it("captures the text after the command", () => {
    expect(readRememberCommand("/lembrar Agents antes de Finance")).toEqual({
      kind: "capture",
      content: "Agents antes de Finance",
    });
  });

  it("ignores case and surrounding whitespace", () => {
    expect(readRememberCommand("   /LEMBRAR   prefere respostas diretas   ")).toEqual({
      kind: "capture",
      content: "prefere respostas diretas",
    });
  });

  it("accepts a line break as the separator", () => {
    expect(readRememberCommand("/lembrar\nfato em outra linha")).toEqual({
      kind: "capture",
      content: "fato em outra linha",
    });
  });

  it("reads the bare command as a request to consolidate, never as a message", () => {
    // Two regressions in one assertion. The bare command used to be
    // indistinguishable from an ordinary message, and ordinary messages go
    // to the provider; then it meant "you forgot the text". It now means
    // what the user actually wants: read this conversation.
    expect(readRememberCommand("/lembrar")).toEqual({ kind: "consolidate" });
    expect(readRememberCommand("  /lembrar  ")).toEqual({ kind: "consolidate" });
    expect(readRememberCommand("/Lembrar")).toEqual({ kind: "consolidate" });
    expect(readRememberCommand("/lembrar\n")).toEqual({ kind: "consolidate" });
  });

  it("leaves a word that merely starts with the command as a message", () => {
    expect(readRememberCommand("/lembrarei disso")).toEqual({ kind: "none" });
  });

  it("leaves ordinary text as a message", () => {
    expect(readRememberCommand("salve na memória que X")).toEqual({ kind: "none" });
    expect(readRememberCommand("preciso lembrar de comprar pão")).toEqual({ kind: "none" });
    expect(readRememberCommand("")).toEqual({ kind: "none" });
  });

  it("never routes the command itself to the provider", () => {
    // The invariant, stated as one property: whatever follows the command
    // token, the draft is either captured or held — it is never a turn.
    // A caller that sends `kind: "none"` is therefore correct by
    // construction, which is what the old two-state answer could not give.
    for (const draft of ["/lembrar", "/lembrar ", "/lembrar x", "/LEMBRAR\ty", "  /lembrar  z"]) {
      expect(readRememberCommand(draft).kind).not.toBe("none");
    }
  });
});

describe("rememberIntentHint", () => {
  it("says nothing about the command itself, in either form", () => {
    // Both forms now do something the moment Enter is pressed, so neither
    // needs a note explaining why nothing happened.
    expect(rememberIntentHint("/lembrar")).toBeNull();
  });

  it("says nothing when the command is complete", () => {
    expect(rememberIntentHint("/lembrar um fato")).toBeNull();
  });

  it("points at the mechanism when the draft only sounds like a capture", () => {
    expect(rememberIntentHint("salve na memória que X")).toContain(REMEMBER_COMMAND);
    expect(rememberIntentHint("anota aí")).toContain("Salvar na memória");
  });

  it("stays quiet on ordinary messages", () => {
    expect(rememberIntentHint("como está o projeto?")).toBeNull();
    expect(rememberIntentHint("")).toBeNull();
  });
});
