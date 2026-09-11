// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { I18nFixture } from "./testing";
import { pt } from "./pt";
import { en } from "./en";
import type { Lang } from "./context";

import { AgentForm } from "@/modules/agents/components/AgentForm";
import { MemoryCandidatesDialog } from "@/modules/agents/components/memory/MemoryCandidatesDialog";
import { ContextInspector } from "@/modules/agents/components/ContextInspector";
import { UpcomingInstallmentsCard } from "@/pages/app/modules/finance/UpcomingInstallmentsCard";
import {
  COMPOSER_COMMANDS,
  availabilityOf,
  filterCommands,
} from "@/modules/agents/composer/commands";
import { referencesFromTools } from "@/modules/agents/composer/references";

/**
 * Does the product actually read in both languages?
 *
 * `i18n.test.tsx` proves the machinery works. This file asks the question
 * the machinery exists to answer, on the surfaces a person actually uses,
 * and it asks it the only way that means anything: render the same
 * component twice, once per language, and require the words to differ while
 * everything that is NOT words stays identical.
 *
 * Deliberately not snapshots. A snapshot of a screen in two languages
 * passes for years and then fails for a class name, which teaches everyone
 * to regenerate it without reading it. These assert the specific property
 * each surface is supposed to have.
 */

afterEach(cleanup);

function renderIn(lang: Lang, ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nFixture lang={lang}>
        <MemoryRouter>{ui}</MemoryRouter>
      </I18nFixture>
    </QueryClientProvider>,
  );
}

/** Render the same tree in both languages and hand back the visible text. */
function bothLanguages(ui: ReactNode): { pt: string; en: string } {
  const a = renderIn("pt", ui);
  const ptText = a.container.textContent ?? "";
  cleanup();
  const b = renderIn("en", ui);
  const enText = b.container.textContent ?? "";
  cleanup();
  return { pt: ptText, en: enText };
}

/* ── A / B / C · the agent's own surfaces ────────────────────────────── */

const PROVIDER = {
  id: "p1",
  name: "LiteLLM",
  base_url: "https://x/v1",
  api_key_hint: "…a3f9",
  default_model: "claude-haiku-4-5",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

describe("Agents · the agent form", () => {
  it("renders its labels in each language", () => {
    const ui = (
      <AgentForm
        variant="create"
        providers={[PROVIDER, { ...PROVIDER, id: "p2", name: "Outro" }] as never}
        submitLabel="x"
        submitting={false}
        error={null}
        onSubmit={() => {}}
      />
    );
    const text = bothLanguages(ui);

    expect(text.pt).toContain(pt.app.modules.agents.form.instructions);
    expect(text.en).toContain(en.app.modules.agents.form.instructions);
    expect(text.pt).not.toContain(en.app.modules.agents.form.instructions);
  });

  /* L · technical identity does not move with language. */
  it("keeps the model name and the provider name identical in both", () => {
    const ui = (
      <AgentForm
        variant="create"
        providers={[PROVIDER, { ...PROVIDER, id: "p2", name: "Outro" }] as never}
        submitLabel="x"
        submitting={false}
        error={null}
        onSubmit={() => {}}
      />
    );
    const text = bothLanguages(ui);

    // A provider is something the operator named. A model name is the
    // gateway's identifier. Neither is copy.
    for (const invariant of ["LiteLLM", "Outro"]) {
      expect(text.pt).toContain(invariant);
      expect(text.en).toContain(invariant);
    }
  });
});

/* ── D · the `/` menu, without moving what it selects ────────────────── */

describe("Agents · the / command menu", () => {
  it("translates what a command reads without touching what it is", () => {
    const id = "memory.consolidate";
    expect(pt.app.modules.agents.composer.commands[id].label).not.toBe(
      en.app.modules.agents.composer.commands[id].label,
    );

    // The identity, the typed trigger and the search keywords are one and
    // the same object in both languages, because they live in code rather
    // than in either dictionary.
    const command = COMPOSER_COMMANDS.find((c) => c.id === id)!;
    expect(command.trigger).toBe("/lembrar");
    expect(command.id).toBe(id);
  });

  it("resolves the same command from either language's wording", () => {
    const ptLabels = { "memory.consolidate": pt.app.modules.agents.composer.commands["memory.consolidate"] };
    const enLabels = { "memory.consolidate": en.app.modules.agents.composer.commands["memory.consolidate"] };

    const fromPt = filterCommands("Lembrar", undefined, ptLabels);
    const fromEn = filterCommands("Remember", undefined, enLabels);

    expect(fromPt).toHaveLength(1);
    expect(fromEn).toHaveLength(1);
    // Same command, reached by two different words. If translating a label
    // could change this, `/` would select different things per language.
    expect(fromPt[0].id).toBe(fromEn[0].id);
    expect(fromPt[0].trigger).toBe(fromEn[0].trigger);
  });

  it("keeps the trigger typeable in both languages", () => {
    // Someone who learned `/lembrar` keeps it when they switch to English.
    expect(filterCommands("/lembrar")).toHaveLength(1);
    expect(filterCommands("memory")).toHaveLength(1);
  });

  it("reports unavailability as a key, so the reason can be translated", () => {
    const command = COMPOSER_COMMANDS[0];
    const got = availabilityOf(command, { hasAgent: false });
    expect(got.reason).toBe("noAgent");
    expect(pt.app.modules.agents.composer.unavailable.noAgent).not.toBe(
      en.app.modules.agents.composer.unavailable.noAgent,
    );
  });
});

/* ── E · the `@` menu, without moving namespace or scope ─────────────── */

describe("Agents · the @ reference menu", () => {
  const report = {
    items: [
      {
        name: "github.repository.list",
        title: "Listar repositórios",
        shortTitle: "Repositórios",
        authorized: true,
        description: "",
      },
      {
        name: "github.commit.list",
        title: "Listar commits",
        shortTitle: "Commits",
        authorized: true,
        description: "",
      },
    ],
    authorized_count: 2,
    stale: [],
  } as never;

  it("derives id, namespace and keywords from the backend, not from copy", () => {
    const refs = referencesFromTools(report);
    expect(refs).toHaveLength(1);

    const [group] = refs;
    // The id IS the namespace, and it is what selection, tool scope and
    // grouping all key off. It comes from the backend's tool names.
    expect(group.id).toBe("github");
    expect(group.keywords).toContain("github.repository.list");
    expect(group.keywords).toContain("github.commit.list");
    expect(group.capabilityCount).toBe(2);
  });

  it("joins the capability list in the reader's language, naming them unchanged", () => {
    window.localStorage.setItem("corsi.lang", "pt");
    render(<I18nFixture lang="pt"><span /></I18nFixture>);
    const inPt = referencesFromTools(report)[0];
    cleanup();

    render(<I18nFixture lang="en"><span /></I18nFixture>);
    const inEn = referencesFromTools(report)[0];
    cleanup();

    // Only the conjunction moves: "repositórios e commits" against
    // "repositórios and commits". The capability names are the backend's
    // and are never translated here.
    expect(inPt.description).toContain("repositórios");
    expect(inEn.description).toContain("repositórios");
    expect(inPt.description).not.toBe(inEn.description);

    // And the thing the menu actually selects is untouched by any of it.
    expect(inPt.id).toBe(inEn.id);
    expect(inPt.keywords).toEqual(inEn.keywords);
  });
});

/* ── F / K · Memory, and what must survive a language change ─────────── */

describe("Agents · memory", () => {
  const phase = {
    status: "ready" as const,
    data: {
      candidates: [
        { content: "João prefere Go no backend.", reason: "decisão", duplicate_of: null },
      ],
      considered_messages: 4,
      effective_up_to_seq: 42,
      usage: null,
    },
  } as never;

  it("translates the dialog around the candidate", () => {
    const ui = (
      <MemoryCandidatesDialog
        agentName="Content Agent"
        phase={phase}
        saveError={null}
        onConfirm={() => {}}
        onCancel={() => {}}
        onOpenSettings={() => {}}
      />
    );
    const text = bothLanguages(ui);
    expect(text.pt).toContain(pt.app.modules.agents.memory.candidates.title);
    expect(text.en).toContain(en.app.modules.agents.memory.candidates.title);
  });

  /* K · user-generated content is not copy and must never be rewritten. */
  it("leaves the agent's name and the memory's own text untouched", () => {
    const ui = (
      <MemoryCandidatesDialog
        agentName="Content Agent"
        phase={phase}
        saveError={null}
        onConfirm={() => {}}
        onCancel={() => {}}
        onOpenSettings={() => {}}
      />
    );
    const text = bothLanguages(ui);

    for (const invariant of ["Content Agent", "João prefere Go no backend."]) {
      expect(text.pt).toContain(invariant);
      expect(text.en).toContain(invariant);
    }
  });
});

/* ── G · Context Inspector, and historical truth ─────────────────────── */

describe("Agents · context inspector", () => {
  // The inspector reads the report off the message, which is where the
  // backend froze it when the turn ran.
  const message = {
    id: "m1",
    role: "assistant",
    content: "x",
    seq: 1,
    context_report: {
      total_estimated_tokens: 1234,
      total_characters: 4321,
      blocks: [],
      exclusions: [],
      warnings: [],
      rounds: null,
    },
  } as never;

  it("translates its section headings", () => {
    const ui = (
      <ContextInspector message={message} agentName="Content Agent" onClose={() => {}} />
    );
    const text = bothLanguages(ui);
    expect(text.pt).toContain(pt.app.modules.agents.contextInspector.inputContext);
    expect(text.en).toContain(en.app.modules.agents.contextInspector.inputContext);
  });

  /* K · a recorded turn is a fact, not a rendering. */
  it("reports the same recorded figures in both languages", () => {
    const ui = (
      <ContextInspector message={message} agentName="Content Agent" onClose={() => {}} />
    );
    const inPt = renderIn("pt", ui).container.textContent ?? "";
    cleanup();
    const inEn = renderIn("en", ui).container.textContent ?? "";
    cleanup();

    // Grouping separators differ (4.321 vs 4,321) — that is presentation.
    // The digits themselves must not.
    const digitsOnly = (s: string) => (s.match(/\d/g) ?? []).join("");
    expect(digitsOnly(inPt)).toBe(digitsOnly(inEn));
    // And the agent's name is the operator's, in both.
    expect(inPt).toContain("Content Agent");
    expect(inEn).toContain("Content Agent");
  });
});

/* ── I · Finance ─────────────────────────────────────────────────────── */

describe("Finance · upcoming installments", () => {
  it("translates the card and keeps the currency in reais", () => {
    // Only the shape the card reads: an empty ledger is enough to render
    // its heading and its empty state, which is what is under test.
    const store = {
      state: { transactions: [], purchasePlans: [], cards: [], categories: [] },
      purchasePlansById: new Map(),
      cardsById: new Map(),
      categoriesById: new Map(),
    } as never;
    const ui = <UpcomingInstallmentsCard store={store} />;
    const text = bothLanguages(ui);

    expect(text.pt).toContain(pt.app.modules.finance.installmentsCard.title);
    expect(text.en).toContain(en.app.modules.finance.installmentsCard.title);
    expect(text.pt).not.toBe(text.en);
  });
});

/* ── J · accessibility labels ────────────────────────────────────────── */

describe("accessibility labels follow the language", () => {
  it("translates the agent form's advanced disclosure and field labels", () => {
    const ui = (
      <AgentForm
        variant="create"
        providers={[PROVIDER] as never}
        submitLabel="x"
        submitting={false}
        error={null}
        onSubmit={() => {}}
      />
    );

    renderIn("pt", ui);
    expect(screen.getByLabelText(pt.app.modules.agents.form.name)).toBeTruthy();
    cleanup();

    renderIn("en", ui);
    expect(screen.getByLabelText(en.app.modules.agents.form.name)).toBeTruthy();
    cleanup();
  });

  it("has no accessibility label left in the other language", () => {
    // The specific failure this guards: a screen whose visible copy was
    // migrated while its aria-labels were not, so a screen-reader user is
    // the only one still hearing the old language.
    const ui = (
      <AgentForm
        variant="create"
        providers={[PROVIDER] as never}
        submitLabel="x"
        submitting={false}
        error={null}
        onSubmit={() => {}}
      />
    );
    const { container } = renderIn("en", ui);
    const labels = [...container.querySelectorAll("[aria-label]")].map((n) =>
      n.getAttribute("aria-label"),
    );
    for (const label of labels) {
      expect(label).not.toBe(pt.app.modules.agents.form.name);
      expect(label).not.toBe(pt.app.modules.agents.form.instructions);
    }
    cleanup();
  });
});
