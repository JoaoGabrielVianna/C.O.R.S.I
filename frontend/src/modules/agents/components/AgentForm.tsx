import { useState } from "react";
import { ChevronDown, Info, Loader2, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { useFormat, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { ApiAgent } from "@/modules/agents/api/agents";
import type { ApiProvider } from "@/modules/agents/api/providers";
import { ModelIcon } from "@/modules/agents/components/ModelIcon";
import { useProviderModels } from "@/modules/agents/hooks/useProviders";

/**
 * The agent form, in the two shapes an agent is ever edited in.
 *
 * ── create ─────────────────────────────────────────────────────────────
 * Name, description, instructions, model. Four fields, because deciding to
 * have an agent should not require deciding its sampling temperature. The
 * rest keeps the domain defaults and is one click away afterwards.
 *
 * The provider is the exception the backend forces: an agent cannot exist
 * without one. So it is asked only when there is a genuine choice — with a
 * single provider configured, the form states which one it will use instead
 * of presenting a select with one option.
 *
 * ── settings ───────────────────────────────────────────────────────────
 * Everything, with instructions first and at full size. The instructions
 * ARE the agent; a five-line textarea buried between two sliders said the
 * opposite. The knobs move into a section that starts closed.
 *
 * ── Vocabulary ─────────────────────────────────────────────────────────
 * `history_limit` is called Histórico here and nowhere Memória. It is how
 * many past messages of THIS conversation are replayed to the model, dies
 * with the conversation, and is a different thing from Agent Memory, which
 * does not exist yet. Two things called memory, one of them a slider, is
 * the collision this rename exists to prevent.
 *
 * The provider is called Provedor, not Token: "token" already means the
 * unit models are billed in and the ceiling on a reply, and one screen
 * cannot carry three meanings of it.
 */

export interface AgentFormValues {
  providerId: string;
  name: string;
  description: string;
  systemPrompt: string;
  model: string;
  /** Null is "no preference": nothing is sent and the provider decides. */
  temperature: number | null;
  historyLimit: number;
  maxTokens: number;
}

/**
 * Mirrors the domain defaults in `domain/agent.go`.
 *
 * Temperature is deliberately absent. The backend has no default for it and
 * must not: the valid value is per-model and value-exact, so any number this
 * form pre-filled would be a guess that produces a 502 on some models. See
 * ApiAgent.temperature.
 */
const DEFAULTS = { historyLimit: 40, maxTokens: 4096 } as const;

/**
 * The value the slider shows once someone turns the preference ON.
 *
 * It is a STARTING POINT for an explicit choice, never a default: it is only
 * ever read when the operator has just said they want to set one.
 */
const TEMPERATURE_SEED = 1;

/** The backend caps the system prompt here (`domain/agent.go`). */
const SYSTEM_PROMPT_MAX = 20_000;

function initialAgentValues(
  agent: ApiAgent | undefined,
  providers: ApiProvider[],
): AgentFormValues {
  return {
    providerId: agent?.provider_id ?? providers[0]?.id ?? "",
    name: agent?.name ?? "",
    description: agent?.description ?? "",
    systemPrompt: agent?.system_prompt ?? "",
    model: agent?.model ?? "",
    // `?? DEFAULTS.temperature` here was a real bug waiting: it would read
    // an agent that expressed no preference as 0.7 and then SAVE 0.7 on the
    // next edit, silently re-creating the incompatibility on every visit to
    // this screen. Null in, null out.
    temperature: agent?.temperature ?? null,
    historyLimit: agent?.history_limit ?? DEFAULTS.historyLimit,
    maxTokens: agent?.max_tokens ?? DEFAULTS.maxTokens,
  };
}

export function AgentForm({
  variant,
  providers,
  initial,
  submitLabel,
  submitting,
  error,
  onSubmit,
  onCancel,
}: {
  variant: "create" | "settings";
  providers: ApiProvider[];
  initial?: ApiAgent;
  submitLabel: string;
  submitting: boolean;
  error: string | null;
  onSubmit: (v: AgentFormValues) => void | Promise<void>;
  onCancel?: () => void;
}) {
  const t = useT();
  const fmt = useFormat();
  const [values, setValues] = useState<AgentFormValues>(() =>
    initialAgentValues(initial, providers),
  );
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const set = <K extends keyof AgentFormValues>(k: K, v: AgentFormValues[K]) =>
    setValues((current) => ({ ...current, [k]: v }));

  const selectedProvider = providers.find((p) => p.id === values.providerId);
  const modelsMutation = useProviderModels();
  const models = modelsMutation.data ?? [];
  const creating = variant === "create";
  const promptTooLong = values.systemPrompt.length > SYSTEM_PROMPT_MAX;

  const providerField =
    providers.length > 1 || !creating ? (
      <div className="space-y-1">
        <FieldLabel
          htmlFor="a-provider"
          hint={t.app.modules.agents.form.providerHint}
        >
          {t.app.modules.agents.form.provider}
        </FieldLabel>
        <select
          id="a-provider"
          value={values.providerId}
          onChange={(e) => {
            set("providerId", e.target.value);
            set("model", "");
          }}
          className={selectClass}
        >
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
    ) : null;

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        void onSubmit(values);
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="a-name">{t.app.modules.agents.form.name}</Label>
          <Input
            id="a-name"
            value={values.name}
            onChange={(e) => set("name", e.target.value)}
            placeholder={t.app.modules.agents.form.namePlaceholder}
          />
        </div>

        <div className="space-y-1">
          <Label htmlFor="a-desc">{t.app.modules.agents.form.description}</Label>
          <Input
            id="a-desc"
            value={values.description}
            onChange={(e) => set("description", e.target.value)}
            placeholder={t.app.modules.agents.form.descriptionPlaceholder}
          />
        </div>

        <ModelField
          value={values.model}
          providerId={values.providerId}
          providerDefault={selectedProvider?.default_model}
          models={models}
          loading={modelsMutation.isPending}
          failed={modelsMutation.isError}
          onLoad={() => values.providerId && modelsMutation.mutate(values.providerId)}
          onChange={(m) => set("model", m)}
        />

        {providerField}
      </div>

      {/* Only shown when there is nothing to choose: the agent still needs a
          provider, so the form says which one rather than staying silent. */}
      {creating && providers.length === 1 ? (
        <p className="text-[11.5px] text-(--color-muted-foreground)">
          {t.app.modules.agents.form.singleProviderLead} <strong>{providers[0].name}</strong>
          {t.app.modules.agents.form.singleProviderTail}
        </p>
      ) : null}

      {/* Instructions: the definition of the agent, at the size that says so. */}
      <div className="space-y-1">
        <div className="flex items-baseline justify-between gap-2">
          <Label htmlFor="a-prompt">{t.app.modules.agents.form.instructions}</Label>
          <span
            className={cn(
              "font-mono text-[10px] tabular-nums",
              promptTooLong ? "text-(--color-destructive)" : "text-(--color-muted-foreground)",
            )}
          >
            {fmt.number(values.systemPrompt.length)} /{" "}
            {fmt.number(SYSTEM_PROMPT_MAX)}
          </span>
        </div>
        <textarea
          id="a-prompt"
          rows={creating ? 6 : 14}
          value={values.systemPrompt}
          onChange={(e) => set("systemPrompt", e.target.value)}
          placeholder={t.app.modules.agents.form.instructionsPlaceholder}
          className="w-full resize-y rounded-xl border border-(--color-border) bg-(--color-card) px-3.5 py-2.5 text-sm leading-relaxed text-(--color-foreground) placeholder:text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20"
        />
        <p className="text-[11px] leading-relaxed text-(--color-muted-foreground)">
          Quem o agente é e como ele responde. Vale para todo turno futuro; o histórico já gravado
          fica como está.
        </p>
      </div>

      {/* The knobs. Closed by default: they have sane defaults and are not
          part of deciding to have an agent. */}
      {!creating ? (
        <div className="rounded-xl border border-(--color-border)">
          <button
            type="button"
            onClick={() => setAdvancedOpen((v) => !v)}
            aria-expanded={advancedOpen}
            className="flex w-full items-center gap-2 px-3.5 py-2.5 text-left text-[13px] font-medium text-(--color-foreground)"
          >
            <ChevronDown
              className={cn(
                "size-4 text-(--color-muted-foreground) transition-transform duration-200",
                advancedOpen ? "rotate-0" : "-rotate-90",
              )}
            />
            {t.app.modules.agents.form.advanced}
            <span className="ml-auto font-mono text-[10.5px] tabular-nums text-(--color-muted-foreground)">
              {t.app.modules.agents.form.advancedSummary
                .replace(
                  "{temp}",
                  values.temperature === null
                    ? t.app.modules.agents.form.temperatureAuto
                    : String(values.temperature),
                )
                .replace("{history}", String(values.historyLimit))
                .replace("{max}", String(values.maxTokens))}
            </span>
          </button>

          {advancedOpen ? (
            <div className="grid gap-4 border-t border-(--color-border) p-3.5 sm:grid-cols-2">
              <div className="space-y-1">
                <FieldLabel
                  htmlFor="a-temp"
                  hint={t.app.modules.agents.form.temperatureHint}
                >
                  {values.temperature === null
                    ? t.app.modules.agents.form.temperatureAutoLabel
                    : t.app.modules.agents.form.temperature.replace(
                        "{value}",
                        String(values.temperature),
                      )}
                </FieldLabel>
                {/*
                  The checkbox is the whole point of this control, not a
                  refinement of it: without a way to say "no preference" the
                  form can only ever send a number, and a number is what
                  claude-opus-* answers 400 to unless it happens to be 1.
                */}
                <label className="flex items-center gap-2 text-[11.5px] text-(--color-muted-foreground)">
                  <input
                    type="checkbox"
                    checked={values.temperature === null}
                    onChange={(e) =>
                      set("temperature", e.target.checked ? null : TEMPERATURE_SEED)
                    }
                    className="accent-(--color-accent)"
                  />
                  {t.app.modules.agents.form.temperatureAutoHint}
                </label>
                <input
                  id="a-temp"
                  type="range"
                  min={0}
                  max={2}
                  step={0.1}
                  value={values.temperature ?? TEMPERATURE_SEED}
                  disabled={values.temperature === null}
                  onChange={(e) => set("temperature", Number(e.target.value))}
                  className="h-10 w-full accent-(--color-accent) disabled:opacity-40"
                />
              </div>

              <div className="space-y-1">
                <FieldLabel
                  htmlFor="a-history"
                  hint={t.app.modules.agents.form.historyHint}
                >
                  Histórico ({values.historyLimit} mensagens)
                </FieldLabel>
                <input
                  id="a-history"
                  type="range"
                  min={2}
                  max={100}
                  step={2}
                  value={values.historyLimit}
                  onChange={(e) => set("historyLimit", Number(e.target.value))}
                  className="h-10 w-full accent-(--color-accent)"
                />
                <p className="text-[10.5px] leading-relaxed text-(--color-muted-foreground)">
                  {t.app.modules.agents.form.historyLabel}
                </p>
              </div>

              <div className="space-y-1">
                <FieldLabel
                  htmlFor="a-maxtokens"
                  hint={t.app.modules.agents.form.maxTokensHint}
                >
                  {t.app.modules.agents.form.maxTokens}
                </FieldLabel>
                <Input
                  id="a-maxtokens"
                  type="number"
                  min={1}
                  value={values.maxTokens}
                  onChange={(e) => set("maxTokens", Number(e.target.value))}
                />
              </div>
            </div>
          ) : null}
        </div>
      ) : null}

      {error ? <p className="text-xs leading-relaxed text-(--color-destructive)">{error}</p> : null}

      <div className="flex items-center gap-2">
        <Button
          type="submit"
          size="sm"
          disabled={!values.name.trim() || !values.providerId || promptTooLong || submitting}
        >
          {submitting ? <Loader2 className="animate-spin" /> : null}
          {submitLabel}
        </Button>
        {onCancel ? (
          <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
            {t.app.modules.agents.form.cancel}
          </Button>
        ) : null}
      </div>
    </form>
  );
}

const selectClass =
  "flex h-10 w-full min-w-0 rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm text-(--color-foreground) shadow-(--shadow-soft) outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20";

/**
 * The model field, with the catalog loaded on demand.
 *
 * The list is not fetched automatically: it is a real round trip to a
 * third-party gateway, and most visits to this form do not change the
 * model. When it cannot be read, the free-text field stays usable — the
 * gateway being cold must not block configuring an agent.
 */
function ModelField({
  value,
  providerId,
  providerDefault,
  models,
  loading,
  failed,
  onLoad,
  onChange,
}: {
  value: string;
  providerId: string;
  providerDefault: string | undefined;
  models: Array<{ id: string }>;
  loading: boolean;
  failed: boolean;
  onLoad: () => void;
  onChange: (model: string) => void;
}) {
  const t = useT();
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <FieldLabel
          htmlFor="a-model"
          hint={t.app.modules.agents.form.modelHint}
        >
          {t.app.modules.agents.form.model}
        </FieldLabel>
        <button
          type="button"
          onClick={onLoad}
          disabled={!providerId || loading}
          className="inline-flex items-center gap-1 text-[11px] font-medium text-(--color-brand-700) hover:underline disabled:opacity-50"
        >
          {loading ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
          Carregar modelos
        </button>
      </div>
      <div className="flex items-center gap-2">
        <ModelIcon model={value || providerDefault} className="size-4" />
        {models.length > 0 ? (
          <select
            id="a-model"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            className={selectClass}
          >
            <option value="">{providerDefault || "padrão do provedor"}</option>
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id}
              </option>
            ))}
          </select>
        ) : (
          <Input
            id="a-model"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={providerDefault ?? "padrão do provedor"}
          />
        )}
      </div>
      {failed ? (
        <p className="text-[11px] text-(--color-muted-foreground)">
          {t.app.modules.agents.form.modelListFailed}
        </p>
      ) : null}
    </div>
  );
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
