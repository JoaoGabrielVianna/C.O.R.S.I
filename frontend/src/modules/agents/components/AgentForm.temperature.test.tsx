// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { afterEach, expect, it } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";
import { AgentForm, type AgentFormValues } from "@/modules/agents/components/AgentForm";

/**
 * Temperature is a PREFERENCE, and "none" is one of its states.
 *
 * ── What these guard ───────────────────────────────────────────────────
 * The backend stopped choosing a temperature because the valid value is
 * per-model and value-exact — measured, claude-opus-4-7 refuses 0.0, 0.5 and
 * 0.7 and accepts only 1, while claude-haiku-4-5 accepts all four. An agent
 * created with the old 0.7 default answered 502 on its first turn.
 *
 * That fix is undone by one `??` in this file. An edit screen that reads
 * null as 0.7 and then submits 0.7 re-creates the incompatibility every time
 * somebody opens Settings and presses Save — without anyone touching the
 * control, and without the backend being able to tell it apart from a
 * deliberate choice.
 */

afterEach(cleanup);

const PROVIDER = {
  id: "p1",
  name: "LiteLLM",
  base_url: "https://x/v1",
  api_key_hint: "…a3f9",
  default_model: "claude-opus-4-7",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

const AGENT = {
  id: "a1",
  workspace_id: "w1",
  provider_id: "p1",
  name: "Palace",
  description: "",
  system_prompt: "",
  model: "claude-opus-4-7",
  temperature: null,
  max_tokens: 4096,
  history_limit: 40,
  accent: "cyan",
  budget: { daily_token_limit: null, daily_cost_limit_usd: null },
  memory_policy: { mode: "off", notes: "" },
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

function renderForm(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nFixture lang="pt">
        <MemoryRouter>{ui}</MemoryRouter>
      </I18nFixture>
    </QueryClientProvider>,
  );
}

function formFor(
  agent: unknown,
  onSubmit: (v: AgentFormValues) => void,
) {
  return (
    <AgentForm
      variant="settings"
      providers={[PROVIDER] as never}
      initial={agent as never}
      submitLabel="Salvar"
      submitting={false}
      error={null}
      onSubmit={onSubmit}
    />
  );
}

/** Open the "Advanced" disclosure, which is where the control lives. */
function openAdvanced() {
  fireEvent.click(screen.getByText("Avançado"));
}

it("does not invent a temperature for an agent that expressed no preference", () => {
  let submitted: AgentFormValues | null = null;
  renderForm(formFor(AGENT, (v) => (submitted = v)));

  fireEvent.submit(screen.getByRole("button", { name: "Salvar" }).closest("form")!);

  expect(submitted).not.toBeNull();
  expect(submitted!.temperature).toBeNull();
});

it("keeps an explicit preference exactly as it was stored", () => {
  let submitted: AgentFormValues | null = null;
  renderForm(formFor({ ...AGENT, temperature: 0.5 }, (v) => (submitted = v)));

  fireEvent.submit(screen.getByRole("button", { name: "Salvar" }).closest("form")!);

  expect(submitted!.temperature).toBe(0.5);
});

it("can turn a preference off, which is what returns an agent to the model default", () => {
  let submitted: AgentFormValues | null = null;
  renderForm(formFor({ ...AGENT, temperature: 0.7 }, (v) => (submitted = v)));

  openAdvanced();
  // The checkbox is the only way to express "no preference". Without it the
  // form can only ever submit a number.
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.submit(screen.getByRole("button", { name: "Salvar" }).closest("form")!);

  expect(submitted!.temperature).toBeNull();
});

it("can turn a preference on, and does not start from the old 0.7", () => {
  let submitted: AgentFormValues | null = null;
  renderForm(formFor(AGENT, (v) => (submitted = v)));

  openAdvanced();
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.submit(screen.getByRole("button", { name: "Salvar" }).closest("form")!);

  // A number, because the operator just asked for one — and specifically not
  // 0.7, the value that caused the defect.
  expect(submitted!.temperature).toBe(1);
});

it("disables the slider while there is no preference", () => {
  const { container } = renderForm(formFor(AGENT, () => {}));
  openAdvanced();

  // By id, not by role: the Advanced panel has several range inputs and
  // this test is about exactly one of them. Checked as a DOM property
  // rather than with a jest-dom matcher, which this suite does not install.
  const slider = container.querySelector<HTMLInputElement>("#a-temp");
  expect(slider?.disabled).toBe(true);
});
