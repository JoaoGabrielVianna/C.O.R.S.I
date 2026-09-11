/*
 * LEGACY — not mounted by the Agents v1 information architecture.
 *
 * This was the agent registry as a table inside a settings tab, with the
 * editor opening as an expanded row. That was replaced: an agent now has
 * its own address, its own header and its own Settings page, and the form
 * itself lives in `components/AgentForm.tsx`.
 *
 * Kept rather than deleted because this working tree has no version
 * control, so a delete here is not recoverable. Nothing imports it. It is a
 * candidate for removal once the new surfaces have been used in anger.
 */
import { useState } from "react";
import { Bot, Info, Loader2, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { ApiError } from "@/lib/api/client";
import type { ApiAgent } from "@/modules/agents/api/agents";
import type { ApiProvider } from "@/modules/agents/api/providers";
import {
  useAgents,
  useCreateAgent,
  useDeleteAgent,
  useUpdateAgent,
} from "@/modules/agents/hooks/useAgents";
import { useProviders, useProviderModels } from "@/modules/agents/hooks/useProviders";
import { useAgentUsage } from "@/modules/agents/hooks/useUsage";
import { useUsdToBrl } from "@/modules/agents/hooks/useFxRate";
import { Money } from "@/modules/agents/components/Money";
import { ModelIcon } from "@/modules/agents/components/ModelIcon";
import { ExpandedRow, TableScroll, Td, Th } from "@/modules/agents/components/table";
import { shortModelName } from "@/modules/agents/models";
import { formatTokens } from "@/modules/agents/format";

/**
 * The agent registry.
 *
 * An agent is a saved configuration, not a running process: which token
 * (provider), which model, what system prompt, how much history it sees.
 * Editing one changes every future turn in every thread that uses it, while
 * the recorded history stays exactly as it happened.
 *
 * Each row also carries what that agent has cost — an ESTIMATE (the tokens
 * we recorded, priced at the provider's current rates), which is why it is
 * shown with a ~. The exact billed number lives on the token that paid for
 * it, in the Tokens section.
 *
 * A table, not cards: the interesting question is almost always comparative
 * (which agent runs which model, on which token, at what cost), and the
 * editor opens as a row under the agent it edits.
 */

const COLUMNS = 6;

type FormMode = { kind: "create" } | { kind: "edit"; agent: ApiAgent } | null;

export function AgentsPanel() {
  const agentsQuery = useAgents();
  const providersQuery = useProviders();
  const createAgent = useCreateAgent();
  const updateAgent = useUpdateAgent();
  const deleteAgent = useDeleteAgent();
  const fx = useUsdToBrl();
  const [mode, setMode] = useState<FormMode>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const agents = agentsQuery.data ?? [];
  const providers = providersQuery.data ?? [];
  const providersById = new Map(providers.map((p) => [p.id, p]));
  const submitting = createAgent.isPending || updateAgent.isPending;
  const editingId = mode?.kind === "edit" ? mode.agent.id : null;
  const rate = fx.data?.rate ?? null;

  const closeForm = () => {
    setMode(null);
    setFormError(null);
  };

  const handleSubmit = async (values: AgentFormValues) => {
    setFormError(null);
    const body = {
      provider_id: values.providerId,
      name: values.name,
      description: values.description,
      system_prompt: values.systemPrompt,
      model: values.model || undefined,
      temperature: values.temperature,
      history_limit: values.historyLimit,
      max_tokens: values.maxTokens,
    };
    try {
      if (mode?.kind === "edit") {
        await updateAgent.mutateAsync({ id: mode.agent.id, body });
      } else {
        await createAgent.mutateAsync(body);
      }
      closeForm();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const form = (
    <AgentForm
      key={editingId ?? "create"}
      providers={providers}
      initial={mode?.kind === "edit" ? mode.agent : undefined}
      submitLabel={mode?.kind === "edit" ? "Salvar alterações" : "Criar agente"}
      submitting={submitting}
      error={formError}
      onCancel={closeForm}
      onSubmit={handleSubmit}
    />
  );

  return (
    <section className="overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)">
      <header className="flex flex-wrap items-start justify-between gap-3 border-b border-(--color-border) px-4 py-3">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-(--color-foreground)">
            <Bot className="size-4 text-(--color-muted-foreground)" />
            Agentes
            <span className="rounded-full bg-(--color-muted) px-2 py-0.5 font-mono text-[10px] tabular-nums text-(--color-muted-foreground)">
              {agents.length}
            </span>
          </h2>
          <p className="mt-0.5 max-w-xl text-xs leading-relaxed text-(--color-muted-foreground)">
            Cada agente é um modelo com um prompt e um <strong>token</strong> vinculado. Editar um
            muda os turnos futuros; o histórico gravado fica como estava. O custo é uma{" "}
            <strong>estimativa</strong> (tokens gravados × preço do modelo).
          </p>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => (mode?.kind === "create" ? closeForm() : setMode({ kind: "create" }))}
          disabled={providers.length === 0}
        >
          <Plus />
          {mode?.kind === "create" ? "Cancelar" : "Novo agente"}
        </Button>
      </header>

      {providers.length === 0 ? (
        <p className="px-4 py-6 text-center text-xs leading-relaxed text-(--color-muted-foreground)">
          Configure um token primeiro. Um agente precisa saber para onde mandar as mensagens.
        </p>
      ) : null}

      {mode?.kind === "create" ? (
        <div className="border-b border-(--color-border) bg-(--color-muted)/40 p-4">{form}</div>
      ) : null}

      {agentsQuery.isLoading ? (
        <p className="px-4 py-6 text-center text-xs text-(--color-muted-foreground)">Carregando…</p>
      ) : agents.length === 0 && providers.length > 0 && !mode ? (
        <div className="px-4 py-8 text-center">
          <Bot className="mx-auto size-5 text-(--color-muted-foreground)" />
          <p className="mt-2 text-sm text-(--color-foreground)">Nenhum agente ainda</p>
          <p className="mx-auto mt-1 max-w-sm text-xs leading-relaxed text-(--color-muted-foreground)">
            Crie o primeiro e comece a conversar.
          </p>
        </div>
      ) : agents.length > 0 ? (
        <TableScroll>
          <table className="w-full min-w-[560px] border-collapse text-sm">
            <thead>
              <tr className="border-b border-(--color-border)">
                <Th>Agente</Th>
                <Th>Modelo</Th>
                <Th className="hidden md:table-cell">Token</Th>
                <Th className="hidden xl:table-cell">Ajustes</Th>
                <Th numeric>Custo estimado</Th>
                <Th numeric>Ações</Th>
              </tr>
            </thead>
            <tbody>
              {agents.map((a) => (
                <AgentRow
                  key={a.id}
                  agent={a}
                  provider={providersById.get(a.provider_id)}
                  rate={rate}
                  editing={editingId === a.id}
                  editor={form}
                  onEdit={() => setMode({ kind: "edit", agent: a })}
                  onDelete={() => deleteAgent.mutate(a.id)}
                />
              ))}
            </tbody>
          </table>
        </TableScroll>
      ) : null}

      {deleteAgent.error ? (
        <p className="border-t border-(--color-border) px-4 py-2 text-xs text-(--color-destructive)">
          {deleteAgent.error.message}
        </p>
      ) : null}
    </section>
  );
}

function AgentRow({
  agent,
  provider,
  rate,
  editing,
  editor,
  onEdit,
  onDelete,
}: {
  agent: ApiAgent;
  provider: ApiProvider | undefined;
  rate: number | null;
  editing: boolean;
  editor: React.ReactNode;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const usage = useAgentUsage(agent.id);
  const report = usage.data;
  const totalTokens = report ? report.total_prompt_tokens + report.total_completion_tokens : 0;
  const effectiveModel = agent.model || provider?.default_model || "";

  return (
    <>
      <tr
        className={
          "border-t border-(--color-border) transition-colors " +
          (editing ? "bg-(--color-brand-500)/5" : "hover:bg-(--color-muted)/40")
        }
      >
        <Td>
          <p className="truncate font-medium text-(--color-foreground)">{agent.name}</p>
          {agent.description ? (
            <p className="truncate text-[11px] text-(--color-muted-foreground)">
              {agent.description}
            </p>
          ) : null}
        </Td>
        <Td>
          <span className="flex items-center gap-1.5">
            <ModelIcon model={effectiveModel} className="size-4" />
            <span
              className="truncate font-mono text-[11px] text-(--color-muted-foreground)"
              title={effectiveModel || "modelo padrão do token"}
            >
              {effectiveModel ? shortModelName(effectiveModel) : "padrão do token"}
            </span>
          </span>
        </Td>
        <Td className="hidden md:table-cell">
          {provider ? (
            <>
              <p className="truncate text-[11.5px] text-(--color-foreground)">{provider.name}</p>
              <p className="truncate font-mono text-[10.5px] text-(--color-muted-foreground)">
                {provider.api_key_hint}
              </p>
            </>
          ) : (
            <span className="text-[11px] text-(--color-destructive)">provedor removido</span>
          )}
        </Td>
        <Td className="hidden xl:table-cell">
          <span className="font-mono text-[10.5px] tabular-nums text-(--color-muted-foreground)">
            temp {agent.temperature} · mem {agent.history_limit} · máx {agent.max_tokens}
          </span>
        </Td>
        <Td numeric>
          {usage.isLoading ? (
            <Loader2 className="ml-auto size-4 animate-spin text-(--color-muted-foreground)" />
          ) : report ? (
            <>
              {report.priced ? (
                <Money usd={report.estimated_cost} rate={rate} approx />
              ) : (
                <p
                  className="text-sm font-semibold tabular-nums text-(--color-foreground)"
                  title="Sem tabela de preços do provedor agora — mostrando só os tokens."
                >
                  —
                </p>
              )}
              <p className="text-[11px] tabular-nums text-(--color-muted-foreground)">
                {formatTokens(totalTokens)} tokens
              </p>
            </>
          ) : (
            <span className="text-[11px] text-(--color-muted-foreground)">—</span>
          )}
        </Td>
        <Td numeric>
          <div className="flex items-center justify-end gap-1">
            <Button size="sm" variant="ghost" onClick={onEdit}>
              <Pencil className="size-3.5" />
              Editar
            </Button>
            <button
              type="button"
              onClick={onDelete}
              aria-label={`Remover ${agent.name}`}
              className="rounded-lg p-2 text-(--color-muted-foreground) hover:bg-(--color-destructive)/10 hover:text-(--color-destructive)"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
        </Td>
      </tr>

      {editing ? <ExpandedRow colSpan={COLUMNS}>{editor}</ExpandedRow> : null}

      {!editing && agent.system_prompt ? (
        <tr className="border-t border-dashed border-(--color-border)/60">
          <td colSpan={COLUMNS} className="px-3 pb-2.5 pt-0">
            <p className="line-clamp-2 text-[11px] leading-relaxed text-(--color-muted-foreground)">
              {agent.system_prompt}
            </p>
          </td>
        </tr>
      ) : null}
    </>
  );
}

interface AgentFormValues {
  providerId: string;
  name: string;
  description: string;
  systemPrompt: string;
  model: string;
  temperature: number;
  historyLimit: number;
  maxTokens: number;
}

/** A label with an info tooltip, so the knobs explain themselves. */
function FieldLabel({
  htmlFor,
  hint,
  children,
}: {
  htmlFor: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-1.5">
      <Label htmlFor={htmlFor}>{children}</Label>
      <span title={hint} className="cursor-help text-(--color-muted-foreground)">
        <Info className="size-3.5" />
      </span>
    </div>
  );
}

function AgentForm({
  providers,
  initial,
  submitLabel,
  onSubmit,
  onCancel,
  submitting,
  error,
}: {
  providers: ApiProvider[];
  initial?: ApiAgent;
  submitLabel: string;
  onSubmit: (v: AgentFormValues) => void | Promise<void>;
  onCancel: () => void;
  submitting: boolean;
  error: string | null;
}) {
  const [values, setValues] = useState<AgentFormValues>({
    providerId: initial?.provider_id ?? providers[0]?.id ?? "",
    name: initial?.name ?? "",
    description: initial?.description ?? "",
    systemPrompt: initial?.system_prompt ?? "",
    model: initial?.model ?? "",
    temperature: initial?.temperature ?? 0.7,
    historyLimit: initial?.history_limit ?? 40,
    maxTokens: initial?.max_tokens ?? 4096,
  });

  const selectedProvider = providers.find((p) => p.id === values.providerId);
  const modelsMutation = useProviderModels();
  const models = modelsMutation.data ?? [];

  // Load the model catalog for the chosen token so the user picks from a
  // real list instead of typing an id from memory. Best-effort: if it fails
  // (cold gateway, bad key) the free-text fallback stays usable.
  const loadModels = () => {
    if (values.providerId) modelsMutation.mutate(values.providerId);
  };

  return (
    <form
      className="space-y-3 rounded-xl border border-(--color-brand-500)/40 bg-(--color-card) p-4"
      onSubmit={(e) => {
        e.preventDefault();
        void onSubmit(values);
      }}
    >
      <p className="text-xs font-medium text-(--color-foreground)">
        {initial ? `Editando “${initial.name}”` : "Novo agente"}
      </p>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="a-name">Nome</Label>
          <Input
            id="a-name"
            value={values.name}
            onChange={(e) => setValues((v) => ({ ...v, name: e.target.value }))}
            placeholder="Analista financeiro"
          />
        </div>

        {/* Token linkage — the field that binds this agent to a credential. */}
        <div className="space-y-1">
          <FieldLabel
            htmlFor="a-provider"
            hint="O token (provedor LiteLLM) por onde as mensagens deste agente são enviadas e cobradas. Dá para trocar quando quiser."
          >
            Token (provedor)
          </FieldLabel>
          <select
            id="a-provider"
            value={values.providerId}
            onChange={(e) => setValues((v) => ({ ...v, providerId: e.target.value, model: "" }))}
            className="flex h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
          >
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.api_key_hint})
              </option>
            ))}
          </select>
          {selectedProvider ? (
            <p className="font-mono text-[10.5px] text-(--color-muted-foreground)">
              modelo padrão: {selectedProvider.default_model || "—"}
            </p>
          ) : null}
        </div>

        <div className="space-y-1">
          <div className="flex items-center justify-between">
            <FieldLabel
              htmlFor="a-model"
              hint="É o model_name do LiteLLM (ex.: claude-haiku-4-5), não o nome do modelo na Anthropic. Em branco, usa o modelo padrão do token."
            >
              Modelo
            </FieldLabel>
            <button
              type="button"
              onClick={loadModels}
              disabled={!values.providerId || modelsMutation.isPending}
              className="inline-flex items-center gap-1 text-[11px] font-medium text-(--color-brand-700) hover:underline disabled:opacity-50"
            >
              {modelsMutation.isPending ? (
                <Loader2 className="size-3 animate-spin" />
              ) : (
                <RefreshCw className="size-3" />
              )}
              Carregar modelos
            </button>
          </div>
          <div className="flex items-center gap-2">
            <ModelIcon model={values.model || selectedProvider?.default_model} className="size-4" />
            {models.length > 0 ? (
              <select
                id="a-model"
                value={values.model}
                onChange={(e) => setValues((v) => ({ ...v, model: e.target.value }))}
                className="flex h-10 w-full min-w-0 rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
              >
                <option value="">
                  {selectedProvider?.default_model || "modelo padrão do token"}
                </option>
                {models.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.id}
                  </option>
                ))}
              </select>
            ) : (
              <Input
                id="a-model"
                value={values.model}
                onChange={(e) => setValues((v) => ({ ...v, model: e.target.value }))}
                placeholder={selectedProvider?.default_model ?? "modelo padrão do token"}
              />
            )}
          </div>
          {modelsMutation.isError ? (
            <p className="text-[11px] text-(--color-muted-foreground)">
              Não deu para listar os modelos agora — digite o model_name à mão.
            </p>
          ) : null}
        </div>

        <div className="space-y-1">
          <FieldLabel
            htmlFor="a-temp"
            hint="Criatividade da resposta. 0 = determinístico e factual; 2 = mais solto e variado. Para tarefas objetivas, 0.2–0.7."
          >
            Temperatura ({values.temperature})
          </FieldLabel>
          <input
            id="a-temp"
            type="range"
            min={0}
            max={2}
            step={0.1}
            value={values.temperature}
            onChange={(e) => setValues((v) => ({ ...v, temperature: Number(e.target.value) }))}
            className="h-10 w-full accent-(--color-accent)"
          />
        </div>

        <div className="space-y-1">
          <FieldLabel
            htmlFor="a-history"
            hint="Quantas mensagens anteriores são reenviadas ao modelo a cada turno (a memória da conversa). Maior = mais contexto, porém mais tokens (mais custo) por resposta."
          >
            Memória ({values.historyLimit} msgs)
          </FieldLabel>
          <input
            id="a-history"
            type="range"
            min={2}
            max={100}
            step={2}
            value={values.historyLimit}
            onChange={(e) => setValues((v) => ({ ...v, historyLimit: Number(e.target.value) }))}
            className="h-10 w-full accent-(--color-accent)"
          />
        </div>

        <div className="space-y-1">
          <FieldLabel
            htmlFor="a-maxtokens"
            hint="Teto de tokens que a resposta pode ter. Se a resposta bater o teto, ela vem cortada (finish_reason = length)."
          >
            Máx. tokens da resposta
          </FieldLabel>
          <Input
            id="a-maxtokens"
            type="number"
            min={1}
            value={values.maxTokens}
            onChange={(e) => setValues((v) => ({ ...v, maxTokens: Number(e.target.value) }))}
          />
        </div>
      </div>

      <div className="space-y-1">
        <Label htmlFor="a-desc">Descrição</Label>
        <Input
          id="a-desc"
          value={values.description}
          onChange={(e) => setValues((v) => ({ ...v, description: e.target.value }))}
          placeholder="Para que serve este agente"
        />
      </div>

      <div className="space-y-1">
        <Label htmlFor="a-prompt">Prompt de sistema</Label>
        <textarea
          id="a-prompt"
          rows={5}
          value={values.systemPrompt}
          onChange={(e) => setValues((v) => ({ ...v, systemPrompt: e.target.value }))}
          placeholder="Você é o analista financeiro do João. Responda em português, direto ao ponto."
          className="w-full resize-y rounded-xl border border-(--color-border) bg-(--color-card) px-3.5 py-2 text-sm leading-relaxed text-(--color-foreground) placeholder:text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
        />
      </div>

      {error ? <p className="text-xs leading-relaxed text-(--color-destructive)">{error}</p> : null}

      <div className="flex items-center gap-2">
        <Button
          type="submit"
          size="sm"
          disabled={!values.name.trim() || !values.providerId || submitting}
        >
          {submitting ? <Loader2 className="animate-spin" /> : null}
          {submitLabel}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}
