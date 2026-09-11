// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nProvider } from "./I18nProvider";
import { detectInitialLang } from "./detect";
import { useFormat, useLang, useT } from "./hooks";
import { deepMerge, resolveDictionary, resetMissingKeyWarnings } from "./fallback";
import { LOCALE_TAG, makeLocaleFormat } from "./locale";
import { pt, type Translations } from "./pt";
import { en } from "./en";

/**
 * What this file protects.
 *
 * The toggle worked before any of this existed, so "the switch flips" was
 * never the risk. The risks were quieter: a language that did not survive a
 * reload, a key present in one dictionary and not the other, a date that
 * followed the operating system while the copy followed the reader, and a
 * missing key rendering as nothing at all — a button that silently loses
 * its label reads as a design choice rather than a bug.
 *
 * Each test below is one of those.
 */

const STORAGE_KEY = "corsi.lang";

function setBrowserLanguage(tag: string) {
  Object.defineProperty(window.navigator, "language", {
    value: tag,
    configurable: true,
  });
}

beforeEach(() => {
  window.localStorage.clear();
  resetMissingKeyWarnings();
  setBrowserLanguage("en-US");
});

afterEach(cleanup);

/** A probe that renders one copy key and one formatted value. */
function Probe() {
  const t = useT();
  const fmt = useFormat();
  const [lang, setLang] = useLang();
  return (
    <div>
      <p data-testid="copy">{t.app.sidebar.settings}</p>
      <p data-testid="module">{t.app.modules.agents.home.description}</p>
      <p data-testid="lang">{lang}</p>
      <p data-testid="number">{fmt.number(1234567.5)}</p>
      <p data-testid="money">{fmt.currency(1234.5)}</p>
      <p data-testid="date">{fmt.utcDate("2026-08-12T00:00:00Z", "long")}</p>
      <button type="button" onClick={() => setLang("pt")}>
        to-pt
      </button>
      <button type="button" onClick={() => setLang("en")}>
        to-en
      </button>
    </div>
  );
}

function renderProbe() {
  return render(
    <I18nProvider>
      <Probe />
    </I18nProvider>,
  );
}

describe("i18n · copy", () => {
  /* A */
  it("renders Portuguese copy when the stored language is PT", () => {
    window.localStorage.setItem(STORAGE_KEY, "pt");
    renderProbe();
    expect(screen.getByTestId("copy").textContent).toBe(pt.app.sidebar.settings);
    expect(screen.getByTestId("copy").textContent).toBe("Configurações");
  });

  /* B */
  it("renders English copy when the stored language is EN", () => {
    window.localStorage.setItem(STORAGE_KEY, "en");
    renderProbe();
    expect(screen.getByTestId("copy").textContent).toBe(en.app.sidebar.settings);
    expect(screen.getByTestId("copy").textContent).not.toBe(pt.app.sidebar.settings);
  });

  /* C — the change is reactive: no reload, no remount. */
  it("swaps every string in place when the language changes", async () => {
    const user = userEvent.setup();
    window.localStorage.setItem(STORAGE_KEY, "en");
    renderProbe();

    expect(screen.getByTestId("copy").textContent).toBe(en.app.sidebar.settings);
    await user.click(screen.getByText("to-pt"));

    expect(screen.getByTestId("copy").textContent).toBe(pt.app.sidebar.settings);
    // A module surface, not only the shell: the Agents namespace was the
    // whole reason this sprint existed.
    expect(screen.getByTestId("module").textContent).toBe(
      pt.app.modules.agents.home.description,
    );

    await user.click(screen.getByText("to-en"));
    expect(screen.getByTestId("copy").textContent).toBe(en.app.sidebar.settings);
  });
});

describe("i18n · persistence", () => {
  /* D */
  it("persists the choice to localStorage", async () => {
    const user = userEvent.setup();
    renderProbe();
    await user.click(screen.getByText("to-pt"));
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe("pt");
  });

  /* E — bootstrap: a fresh tree reads the stored choice before painting. */
  it("restores the stored language on a fresh mount", () => {
    window.localStorage.setItem(STORAGE_KEY, "pt");
    renderProbe();
    expect(screen.getByTestId("lang").textContent).toBe("pt");
    // Resolved synchronously, so the first render is already correct and
    // there is no flash of the previous language.
    expect(screen.getByTestId("copy").textContent).toBe(pt.app.sidebar.settings);
  });

  it("falls back to the browser language, then to the default", () => {
    setBrowserLanguage("pt-BR");
    expect(detectInitialLang()).toBe("pt");

    setBrowserLanguage("fr-FR");
    expect(detectInitialLang()).toBe("en");
  });

  it("ignores a stored value that is not a supported language", () => {
    window.localStorage.setItem(STORAGE_KEY, "de");
    setBrowserLanguage("pt-BR");
    // Not "de", and not a crash: an unsupported stored value is discarded
    // and detection runs as though nothing had been stored.
    expect(detectInitialLang()).toBe("pt");
  });

  it("survives localStorage being unavailable", () => {
    const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => detectInitialLang()).not.toThrow();
    spy.mockRestore();
  });
});

describe("i18n · fallback", () => {
  /* F */
  it("falls back to the canonical dictionary for a key the target lacks", () => {
    const partial = { app: { sidebar: { settings: "Settings" } } } as unknown as Translations;
    const merged = resolveDictionary(pt, partial);
    expect(merged.app.sidebar.settings).toBe("Settings");
    // Present only in PT: it renders Portuguese rather than vanishing.
    expect(merged.app.sidebar.modules).toBe(pt.app.sidebar.modules);
  });

  it("replaces arrays wholesale rather than merging them element-wise", () => {
    const merged = deepMerge({ seals: ["a", "b", "c"] }, { seals: ["x"] });
    expect(merged.seals).toEqual(["x"]);
  });

  /* G — a missing key is loud, never blank. */
  it("renders the key path and warns instead of rendering nothing", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const guarded = resolveDictionary(
      { app: { sidebar: {} } } as unknown as Translations,
      {} as Translations,
      true,
    );
    const value = (guarded as unknown as Record<string, Record<string, Record<string, string>>>)
      .app.sidebar.settings;

    expect(value).toBe("app.sidebar.settings");
    expect(value).not.toBeUndefined();
    expect(warn).toHaveBeenCalledWith("[i18n] missing translation key: app.sidebar.settings");
    warn.mockRestore();
  });

  it("reports each missing key once, not once per render", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const guarded = resolveDictionary({ a: {} } as unknown as Translations, {} as Translations, true);
    const read = () => (guarded as unknown as Record<string, Record<string, string>>).a.missing;
    read();
    read();
    read();
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });
});

describe("i18n · dictionary parity", () => {
  /**
   * TypeScript already refuses an EN dictionary that does not satisfy
   * `typeof pt`, so this is the belt to that braces — it catches a shape
   * divergence introduced through a cast, which the compiler would wave
   * through.
   */
  function paths(node: unknown, prefix = ""): string[] {
    if (typeof node !== "object" || node === null || Array.isArray(node)) return [prefix];
    return Object.entries(node).flatMap(([k, v]) =>
      paths(v, prefix ? `${prefix}.${k}` : k),
    );
  }

  it("EN and PT expose exactly the same key paths", () => {
    const ptPaths = paths(pt).sort();
    const enPaths = paths(en).sort();
    expect(enPaths).toEqual(ptPaths);
  });

  it("no leaf is left empty in either language", () => {
    const empties: string[] = [];
    const walk = (node: unknown, prefix: string, lang: string) => {
      if (typeof node === "string") {
        if (node.trim() === "") empties.push(`${lang}:${prefix}`);
        return;
      }
      if (Array.isArray(node) || typeof node !== "object" || node === null) return;
      for (const [k, v] of Object.entries(node)) walk(v, prefix ? `${prefix}.${k}` : k, lang);
    };
    walk(pt, "", "pt");
    walk(en, "", "en");
    expect(empties).toEqual([]);
  });
});

describe("i18n · locale formatting", () => {
  /* I */
  it("formats numbers, money and dates in the reader's locale", async () => {
    const user = userEvent.setup();
    window.localStorage.setItem(STORAGE_KEY, "pt");
    renderProbe();

    expect(screen.getByTestId("number").textContent).toBe("1.234.567,5");
    expect(screen.getByTestId("money").textContent).toContain("1.234,50");
    const ptDate = screen.getByTestId("date").textContent;

    await user.click(screen.getByText("to-en"));

    expect(screen.getByTestId("number").textContent).toBe("1,234,567.5");
    expect(screen.getByTestId("money").textContent).toContain("1,234.50");
    expect(screen.getByTestId("date").textContent).not.toBe(ptDate);
  });

  it("keeps the currency itself invariant — only its rendering moves", () => {
    // Switching the UI to English does not convert anyone's spend to
    // dollars. Both must still be Brazilian reais.
    expect(makeLocaleFormat("pt").currency(10)).toContain("R$");
    expect(makeLocaleFormat("en").currency(10)).toContain("R$");
  });

  it("renders the same instant in both languages, written differently", () => {
    const iso = "2026-08-12T15:30:00Z";
    const a = makeLocaleFormat("pt").utcDate(iso, "long");
    const b = makeLocaleFormat("en").utcDate(iso, "long");
    expect(a).not.toBe(b);
    // Same day in both: only the words and the order changed.
    expect(a).toContain("12");
    expect(b).toContain("12");
    expect(a).toContain("2026");
    expect(b).toContain("2026");
  });

  it("renders the em dash for absent values rather than Invalid Date", () => {
    const fmt = makeLocaleFormat("en");
    expect(fmt.date(null)).toBe("—");
    expect(fmt.date("not-a-date")).toBe("—");
    expect(fmt.number(null)).toBe("—");
    expect(fmt.currency(undefined)).toBe("—");
  });

  /**
   * The two languages genuinely disagree about zero, and this is why the
   * helper asks `Intl.PluralRules` instead of testing `count === 1`.
   *
   * CLDR files Portuguese zero under `one` — "0 conversa" — while English
   * files it under `other`: "0 conversations". A hand-rolled `=== 1` check
   * gets Portuguese wrong at exactly the count an empty list shows most.
   */
  it("selects plural forms by the locale's rules, not by count === 1", () => {
    const ptForms = { one: "{count} conversa", other: "{count} conversas" };
    const ptFmt = makeLocaleFormat("pt");
    expect(ptFmt.plural(1, ptForms)).toBe("1 conversa");
    expect(ptFmt.plural(2, ptForms)).toBe("2 conversas");
    expect(ptFmt.plural(0, ptForms)).toBe("0 conversa");

    const enForms = { one: "{count} conversation", other: "{count} conversations" };
    const enFmt = makeLocaleFormat("en");
    expect(enFmt.plural(1, enForms)).toBe("1 conversation");
    expect(enFmt.plural(2, enForms)).toBe("2 conversations");
    expect(enFmt.plural(0, enForms)).toBe("0 conversations");
  });

  it("maps each language to exactly one BCP-47 tag", () => {
    expect(LOCALE_TAG).toEqual({ pt: "pt-BR", en: "en-US" });
  });
});

describe("i18n · technical identity", () => {
  /* J — some strings are not copy, and translating them would be a bug. */
  it("does not translate identifiers, model names or product names", () => {
    // Same in both dictionaries by design: these are names, not words.
    expect(en.app.sidebar.items.jobRadar).toBe(pt.app.sidebar.items.jobRadar);
    expect(en.app.sidebar.items.finance).toBe(pt.app.sidebar.items.finance);
    expect(en.app.sidebar.items.agents).toBe(pt.app.sidebar.items.agents);
    expect(en.hero.title).toBe(pt.hero.title);
    expect(en.app.modules.agents.home.title).toBe("Agents");
    expect(pt.app.modules.agents.home.title).toBe("Agents");
  });

  it("keeps the language codes themselves untranslated", () => {
    expect(pt.lang.pt).toBe("PT");
    expect(en.lang.pt).toBe("PT");
    expect(pt.lang.en).toBe("EN");
    expect(en.lang.en).toBe("EN");
  });
});

describe("i18n · document language", () => {
  it("stamps the html lang attribute so assistive tech reads the right voice", async () => {
    const user = userEvent.setup();
    window.localStorage.setItem(STORAGE_KEY, "en");
    renderProbe();
    expect(document.documentElement.lang).toBe("en-US");

    await user.click(screen.getByText("to-pt"));
    expect(document.documentElement.lang).toBe("pt-BR");
  });
});

describe("i18n · provider contract", () => {
  it("refuses to render a consumer outside the provider", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Probe />)).toThrow(/must be used inside <I18nProvider>/);
    spy.mockRestore();
  });
});
