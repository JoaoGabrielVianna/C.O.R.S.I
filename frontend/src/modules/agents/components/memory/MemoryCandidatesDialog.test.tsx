// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { ApiMemoryCandidates } from "@/modules/agents/api/memories";
import { MemoryCandidatesDialog, type ConsolidationPhase } from "./MemoryCandidatesDialog";
import { I18nFixture } from "@/lib/i18n";

/**
 * The dialog where a proposal becomes a memory, or does not.
 *
 * Everything asserted here is a promise made to the user rather than a
 * rendering detail: nothing is saved without a click, what gets saved is
 * what they edited, a duplicate arrives unselected, cancelling saves
 * nothing, and an unknown cost is never drawn as zero.
 *
 * The dialog owns no request. It is handed a phase and returns a decision,
 * which is what makes every one of those promises testable without a
 * network, a query client or a router.
 */

const onConfirm = vi.fn();
const onCancel = vi.fn();
const onOpenSettings = vi.fn();

function ready(over: Partial<ApiMemoryCandidates> = {}): ConsolidationPhase {
  return {
    status: "ready",
    data: {
      candidates: [
        { content: "Prefere trabalhar com Go", reason: "orienta sugestões futuras", duplicate_of: null },
        { content: "Busca vagas nos EUA", reason: "define o alvo da busca", duplicate_of: null },
      ],
      considered_messages: 12,
      effective_up_to_seq: 42,
      usage: {
        model: "test-model",
        prompt_tokens: 500,
        completion_tokens: 60,
        usage_source: "provider",
        cost_usd: 0.0012,
      },
      ...over,
    },
  };
}

function renderDialog(phase: ConsolidationPhase, props: { saving?: boolean; saveError?: string } = {}) {
  return render(
    <I18nFixture lang="pt">
      <MemoryCandidatesDialog
        agentName="Content Agent"
        phase={phase}
        saving={props.saving}
        saveError={props.saveError ?? null}
        onConfirm={onConfirm}
        onCancel={onCancel}
        onOpenSettings={onOpenSettings}
      />
    </I18nFixture>,
  );
}

const saveButton = () => screen.getByRole("button", { name: /Salvar memórias/ });
const boxes = () => screen.getAllByRole("checkbox");
const texts = () => screen.getAllByRole("textbox");

afterEach(() => {
  cleanup();
  onConfirm.mockReset();
  onCancel.mockReset();
  onOpenSettings.mockReset();
});

describe("MemoryCandidatesDialog", () => {
  it("says it is reading the conversation while it waits", () => {
    renderDialog({ status: "loading" });
    expect(screen.getByText(/Analisando a conversa/)).toBeTruthy();
    // And promises nothing yet.
    expect(screen.queryByRole("button", { name: /Salvar memórias/ })).toBeNull();
  });

  it("says nothing was worth keeping, without inventing a candidate", () => {
    renderDialog(ready({ candidates: [], considered_messages: 6 }));
    expect(screen.getByText(/Não encontrei nada nesta conversa/)).toBeTruthy();
    expect(screen.getByText(/Analisei 6 mensagens/)).toBeTruthy();
    expect(screen.queryByRole("checkbox")).toBeNull();
  });

  it("offers the way out when the policy refused", async () => {
    renderDialog({ status: "error", message: "a consolidação está desligada", kind: "policy" });
    await userEvent.click(screen.getByRole("button", { name: /configurações do agente/ }));
    expect(onOpenSettings).toHaveBeenCalled();
  });

  it("shows a budget refusal as what it is, without a settings link", () => {
    renderDialog({ status: "error", message: "limite diário de tokens atingido", kind: "budget" });
    expect(screen.getByText(/limite diário/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /configurações do agente/ })).toBeNull();
  });

  it("selects every new candidate and confirms the ones still checked", async () => {
    renderDialog(ready());
    expect(screen.getByText("2 selecionadas")).toBeTruthy();

    await userEvent.click(boxes()[1]);
    expect(screen.getByText("1 selecionada")).toBeTruthy();

    await userEvent.click(saveButton());
    expect(onConfirm).toHaveBeenCalledWith(["Prefere trabalhar com Go"], 42);
  });

  it("saves the text as the user edited it, not as the model wrote it", async () => {
    renderDialog(ready());

    await userEvent.click(boxes()[1]);
    await userEvent.clear(texts()[0]);
    await userEvent.type(texts()[0], "Prefere Go para backend");
    await userEvent.click(saveButton());

    expect(onConfirm).toHaveBeenCalledWith(["Prefere Go para backend"], 42);
  });

  it("leaves a duplicate unselected and says why", async () => {
    renderDialog(
      ready({
        candidates: [
          { content: "Prefere trabalhar com Go", reason: "r", duplicate_of: "memory-1" },
          { content: "Busca vagas nos EUA", reason: "r", duplicate_of: null },
        ],
      }),
    );

    expect((boxes()[0] as HTMLInputElement).checked).toBe(false);
    expect((boxes()[1] as HTMLInputElement).checked).toBe(true);
    expect(screen.getByText(/já existe na memória/)).toBeTruthy();

    await userEvent.click(saveButton());
    expect(onConfirm).toHaveBeenCalledWith(["Busca vagas nos EUA"], 42);
  });

  it("cannot confirm an empty selection", async () => {
    renderDialog(ready());
    await userEvent.click(boxes()[0]);
    await userEvent.click(boxes()[1]);

    expect((saveButton() as HTMLButtonElement).disabled).toBe(true);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("saves nothing when cancelled", async () => {
    renderDialog(ready());
    await userEvent.click(screen.getByRole("button", { name: "Cancelar" }));

    expect(onCancel).toHaveBeenCalled();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("keeps the review on screen when the save fails", () => {
    renderDialog(ready(), { saveError: "banco fora do ar" });
    expect(screen.getByText(/banco fora do ar/)).toBeTruthy();
    // The selection and the edits are still there to try again with.
    expect(boxes()).toHaveLength(2);
  });

  it("shows what the analysis cost", () => {
    renderDialog(ready());
    expect(screen.getByText(/560 tokens/)).toBeTruthy();
    expect(screen.getByText(/US\$ 0\.0012/)).toBeTruthy();
  });

  it("never draws an unknown cost as zero", () => {
    renderDialog(
      ready({
        usage: {
          model: "test-model",
          prompt_tokens: 500,
          completion_tokens: 60,
          usage_source: "estimated",
          cost_usd: null,
        },
      }),
    );
    expect(screen.getByText(/custo desconhecido/)).toBeTruthy();
    expect(screen.queryByText(/US\$ 0/)).toBeNull();
  });
});
