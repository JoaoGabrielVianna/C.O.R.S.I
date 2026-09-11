import { describe, expect, it } from "vitest";

import { readReferenceToken, readTriggerToken, removeTriggerToken } from "./trigger";

/**
 * When the command menu is open, expressed as a function of the caret.
 *
 * Every case here is written as (text, caret) rather than as a string,
 * because the caret is half the question: the same draft opens the menu or
 * does not depending on where the cursor sits, and a test that only passed
 * strings would be testing something the component does not ask.
 */

/** `text|with a pipe` reads as the caret position, which is how a person
 *  describes it and how a reviewer checks it. */
function at(marked: string) {
  const caret = marked.indexOf("|");
  return { value: marked.replace("|", ""), caret };
}

function token(marked: string) {
  const { value, caret } = at(marked);
  return readTriggerToken(value, caret);
}

describe("readTriggerToken", () => {
  it("opens on the trigger itself, with an empty query", () => {
    expect(token("/|")).toEqual({ trigger: "/", query: "", start: 0, end: 1 });
  });

  it("carries what has been typed so far", () => {
    expect(token("/l|")?.query).toBe("l");
    expect(token("/lem|")?.query).toBe("lem");
    expect(token("/lembrar|")?.query).toBe("lembrar");
  });

  it("ignores whitespace before the trigger", () => {
    expect(token(" /|")?.query).toBe("");
    expect(token("   /lem|")?.query).toBe("lem");
    expect(token("\n/lem|")?.query).toBe("lem");
  });

  it("stays closed when the trigger is not the first thing typed", () => {
    // A sentence that happens to contain a slash is a sentence. Popping a
    // menu over it would be the interface interrupting a person who was
    // not talking to it.
    expect(token("oi /|")).toBeNull();
    expect(token("oi /lem|")).toBeNull();
  });

  it("stays closed inside a URL", () => {
    expect(token("http://|")).toBeNull();
    expect(token("veja http://x|")).toBeNull();
  });

  it("stays closed with no trigger at all", () => {
    expect(token("lembrar disso|")).toBeNull();
    expect(token("|")).toBeNull();
  });

  it("is closed with the caret before the trigger", () => {
    // About to type in front of it is not being inside it.
    expect(token("|/lem")).toBeNull();
  });

  it("is open with the caret in the middle of the token", () => {
    // The whole token is the query, so editing the middle of a command
    // still shows the command it spells.
    expect(token("/lem|brar")).toEqual({ trigger: "/", query: "lembrar", start: 0, end: 8 });
  });

  it("closes once the command has an argument", () => {
    // `/lembrar algo` is the manual capture form, not a menu.
    expect(token("/lembrar algo|")).toBeNull();
    expect(token("/lembrar |algo")).toBeNull();
  });

  it("is still open with the caret at the end of the token, before its argument", () => {
    expect(token("/lembrar| algo")).toEqual({ trigger: "/", query: "lembrar", start: 0, end: 8 });
  });

  it("closes when the trigger is deleted", () => {
    expect(token("lem|")).toBeNull();
  });

  it("refuses a caret outside the string", () => {
    expect(readTriggerToken("/lem", 99)).toBeNull();
    expect(readTriggerToken("/lem", -1)).toBeNull();
  });
});

describe("removeTriggerToken", () => {
  it("removes the command and clears a draft that was only the command", () => {
    const { value, caret } = at("/lembrar|");
    const t = readTriggerToken(value, caret)!;
    expect(removeTriggerToken(value, t)).toBe("");
  });

  it("keeps whatever else the person had typed", () => {
    const { value, caret } = at("/lem| resto do texto");
    const t = readTriggerToken(value, caret)!;
    expect(removeTriggerToken(value, t)).toBe(" resto do texto");
  });
});

function reference(marked: string) {
  const { value, caret } = at(marked);
  return readReferenceToken(value, caret);
}

/**
 * When the capability menu is open.
 *
 * The rule is deliberately different from `/`, and the difference is
 * semantic rather than cosmetic: a slash command is an instruction to the
 * composer and therefore starts the draft, while `@` is a reference inside
 * a sentence and has to work in the middle of one. "Compare isso usando @"
 * is exactly the moment somebody wants to attach a capability.
 */
describe("readReferenceToken", () => {
  it("opens on the trigger itself, with an empty query", () => {
    expect(reference("@|")).toEqual({ trigger: "@", query: "", start: 0, end: 1 });
  });

  it("carries what has been typed so far", () => {
    expect(reference("@ec|")?.query).toBe("ec");
    expect(reference("@system.echo|")?.query).toBe("system.echo");
  });

  it("opens after text, which is the whole difference from a slash", () => {
    expect(reference("analise @|")?.query).toBe("");
    expect(reference("compare isso usando @git|")?.query).toBe("git");
    // Several spaces, a newline: any whitespace starts a new word.
    expect(reference("oi  @|")?.query).toBe("");
    expect(reference("linha\n@|")?.query).toBe("");
  });

  it("stays closed in an email address", () => {
    // The `@` continues a word rather than starting one. A small,
    // deterministic rule — not an attempt to parse the internet's grammar
    // for addresses.
    expect(reference("joao@corsi.dev|")).toBeNull();
    expect(reference("escreva para joao@corsi.dev|")).toBeNull();
    expect(reference("joao@|")).toBeNull();
  });

  it("stays closed where the @ is part of another token", () => {
    expect(reference("npm i pkg@1.2.3|")).toBeNull();
    expect(reference("https://x.dev/u@handle|")).toBeNull();
  });

  it("is closed with the caret before the trigger", () => {
    expect(reference("|@ec")).toBeNull();
  });

  it("is open with the caret in the middle of the token", () => {
    expect(reference("@ec|ho")).toEqual({ trigger: "@", query: "echo", start: 0, end: 5 });
  });

  it("closes once a word follows it", () => {
    expect(reference("@echo algo|")).toBeNull();
  });

  it("refuses a caret outside the string", () => {
    expect(readReferenceToken("@ec", 99)).toBeNull();
    expect(readReferenceToken("@ec", -1)).toBeNull();
  });

  it("does not answer for a slash, and the command reader does not answer for an @", () => {
    // The two menus can never both be open, and it is asserted rather than
    // assumed: one open menu reading the other's token would select the
    // wrong thing with the same keystroke.
    expect(reference("/lem|")).toBeNull();
    expect(token("@ec|")).toBeNull();
  });
});

describe("removeTriggerToken, for references", () => {
  it("removes the token and leaves the sentence around it", () => {
    const { value, caret } = at("analise @git|");
    const t = readReferenceToken(value, caret)!;
    // The trailing space stays: it is where the caret was and where the
    // person keeps typing. What must not survive is `@git` itself.
    expect(removeTriggerToken(value, t)).toBe("analise ");
  });

  it("clears a draft that was only the token", () => {
    const { value, caret } = at("@git|");
    const t = readReferenceToken(value, caret)!;
    expect(removeTriggerToken(value, t)).toBe("");
  });
});
