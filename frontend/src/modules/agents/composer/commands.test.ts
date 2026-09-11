import { describe, expect, it } from "vitest";
import { pt } from "@/lib/i18n/pt";
import { en } from "@/lib/i18n/en";

import {
  COMPOSER_COMMANDS,
  __assertRegistryIsConsistent,
  availabilityOf,
  filterCommands,
  type ComposerCommand,
} from "./commands";

/**
 * The registry, which is data rather than behaviour.
 *
 * What is worth testing about a table is that it does not contradict
 * itself, that searching it finds what a person would expect to find, and
 * that it never lists something the product cannot actually do.
 */

const remember = () => COMPOSER_COMMANDS.find((c) => c.id === "memory.consolidate")!;

describe("the composer command registry", () => {
  it("has /lembrar, with the identity a menu can render", () => {
    const c = remember();
    expect(c.trigger).toBe("/lembrar");
    // The label and the description are no longer here: they are copy, and
    // copy lives in the dictionary, looked up by this id. What the registry
    // owns is the part that must not move with language.
    expect(pt.app.modules.agents.composer.commands[c.id].label).toBe("Lembrar");
    expect(en.app.modules.agents.composer.commands[c.id].label).toBe("Remember");
    expect(pt.app.modules.agents.composer.commands[c.id].description.length).toBeGreaterThan(10);
    // A component, not an emoji and not a string: emoji are content, and
    // an icon name would need a lookup table nobody has written.
    expect(c.icon).toBeTruthy();
    expect(["function", "object"]).toContain(typeof c.icon);
    expect(c.keywords.length).toBeGreaterThan(0);
  });

  it("lists nothing that does not exist yet", () => {
    // A menu that advertises what the product cannot do teaches people to
    // stop reading it. `/fonte`, `/limpar` and the rest are not here
    // because they are not real.
    expect(COMPOSER_COMMANDS).toHaveLength(1);
  });

  it("has unique ids and unique triggers", () => {
    expect(() => __assertRegistryIsConsistent(COMPOSER_COMMANDS)).not.toThrow();
  });

  it("refuses a duplicate rather than picking one of them", () => {
    // Both silent resolutions — first wins, last wins — are a menu that
    // does something other than what the table says.
    const twice = [remember(), { ...remember(), trigger: "/outro" }] as ComposerCommand[];
    expect(() => __assertRegistryIsConsistent(twice)).toThrow(/id is duplicated/);

    const sameTrigger = [remember(), { ...remember(), id: "memory.consolidate" }] as ComposerCommand[];
    expect(() => __assertRegistryIsConsistent(sameTrigger)).toThrow(/duplicated/);
  });
});

describe("filterCommands", () => {
  it("finds the command by what the user is typing: the trigger", () => {
    for (const q of ["", "l", "le", "lem", "lembrar"]) {
      expect(filterCommands(q)).toHaveLength(1);
    }
  });

  it("finds it by keyword without needing the dictionary", () => {
    // The keywords deliberately carry the word in both languages, so the
    // command is reachable whichever language the reader is in even before
    // any label is handed to the filter.
    expect(filterCommands("memory")).toHaveLength(1);
    expect(filterCommands("lembrar")).toHaveLength(1);
    expect(filterCommands("consolidar")).toHaveLength(1);
  });

  it("also matches a translated label when one is supplied", () => {
    const labels = { "memory.consolidate": en.app.modules.agents.composer.commands["memory.consolidate"] };
    expect(filterCommands("Remember", undefined, labels)).toHaveLength(1);
    // And the id it resolves to is the same one, in either language.
    expect(filterCommands("Remember", undefined, labels)[0].id).toBe("memory.consolidate");
  });

  it("ignores case and accents", () => {
    // The registry says "memória"; someone typing without the accent is
    // looking for the same thing.
    expect(filterCommands("MEMORIA")).toHaveLength(1);
    expect(filterCommands("memória")).toHaveLength(1);
  });

  it("returns nothing for a query that matches nothing", () => {
    expect(filterCommands("zzz")).toHaveLength(0);
    expect(filterCommands("github")).toHaveLength(0);
  });
});

describe("availabilityOf", () => {
  it("offers the command when there is an agent to run it against", () => {
    expect(availabilityOf(remember(), { hasAgent: true })).toEqual({ available: true });
  });

  it("explains itself when there is not", () => {
    const got = availabilityOf(remember(), { hasAgent: false });
    expect(got.available).toBe(false);
    // A key, not a sentence: the menu turns it into words in the reader's
    // language, and this module stays free of copy.
    expect(got.reason).toBe("noAgent");
    expect(pt.app.modules.agents.composer.unavailable.noAgent).toBeTruthy();
    expect(en.app.modules.agents.composer.unavailable.noAgent).toBeTruthy();
  });
});
