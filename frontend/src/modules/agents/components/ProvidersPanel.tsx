import { useState } from "react";
import { CheckCircle2, KeyRound, Loader2, Plug, Trash2, TriangleAlert, XCircle } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { ApiError } from "@/lib/api/client";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { ApiProvider } from "@/modules/agents/api/providers";
import { formatBRL, formatUSD } from "@/modules/agents/format";
import { FxFooter, Money } from "@/modules/agents/components/Money";
import { ExpandedRow, TableScroll, Td, Th } from "@/modules/agents/components/table";
import { useUsdToBrl } from "@/modules/agents/hooks/useFxRate";
import { useProviderSpend } from "@/modules/agents/hooks/useUsage";
import {
  useCreateProvider,
  useDeleteProvider,
  useProviderModels,
  useProviders,
  useUpdateProvider,
} from "@/modules/agents/hooks/useProviders";

/**
 * Where the LiteLLM credential lives — and what it has cost.
 *
 * The key is write-only by design: the backend seals it and never returns
 * it, so the edit form shows a hint like `...a3f9` and an empty field.
 * Leaving that field blank keeps the stored key; typing in it replaces it.
 *
 * Each row also carries that key's REAL billed spend, read from the gateway.
 * It lives here rather than in a separate usage list because it is a fact
 * about the connection — repeating its name elsewhere just to hang a
 * number off it is the duplication this replaces.
 *
 * A table, not cards: every provider answers the same four questions, and
 * columns let them be compared at a glance. Rotating a key or testing a
 * connection opens a row underneath the record it belongs to.
 */

const COLUMNS = 5;

export function ProvidersPanel() {
  const t = useT();
  const providersQuery = useProviders();
  const createProvider = useCreateProvider();
  const deleteProvider = useDeleteProvider();
  const fx = useUsdToBrl();
  const [showForm, setShowForm] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const providers = providersQuery.data ?? [];
  const rate = fx.data?.rate ?? null;

  return (
    <section className="overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)">
      <header className="flex flex-wrap items-start justify-between gap-3 border-b border-(--color-border) px-4 py-3">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-(--color-foreground)">
            <KeyRound className="size-4 text-(--color-muted-foreground)" />
            {t.app.modules.agents.providersPanel.title}
            <span className="rounded-full bg-(--color-muted) px-2 py-0.5 font-mono text-[10px] tabular-nums text-(--color-muted-foreground)">
              {providers.length}
            </span>
          </h2>
          <p className="mt-0.5 max-w-xl text-xs leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.providersPanel.description}{" "}
            <strong>{t.app.modules.agents.providersPanel.descriptionReal}</strong>{" "}
            {t.app.modules.agents.providersPanel.descriptionTail}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => setShowForm((v) => !v)}>
          <Plug />
          {showForm ? t.app.modules.agents.providersPanel.cancel : t.app.modules.agents.providersPanel.new}
        </Button>
      </header>

      {showForm ? (
        <div className="border-b border-(--color-border) bg-(--color-muted)/40 p-4">
          <ProviderForm
            submitting={createProvider.isPending}
            error={formError}
            onCancel={() => {
              setShowForm(false);
              setFormError(null);
            }}
            onSubmit={async (values) => {
              setFormError(null);
              try {
                await createProvider.mutateAsync({
                  name: values.name,
                  base_url: values.baseUrl,
                  api_key: values.apiKey,
                  default_model: values.defaultModel,
                });
                setShowForm(false);
              } catch (err) {
                setFormError(err instanceof ApiError ? err.message : String(err));
              }
            }}
          />
        </div>
      ) : null}

      {providersQuery.isLoading ? (
        <p className="px-4 py-6 text-center text-xs text-(--color-muted-foreground)">{t.app.modules.agents.common.loading}</p>
      ) : providers.length === 0 && !showForm ? (
        <EmptyHint />
      ) : providers.length > 0 ? (
        <TableScroll>
          <table className="w-full min-w-[520px] border-collapse text-sm">
            <thead>
              <tr className="border-b border-(--color-border)">
                <Th>{t.app.modules.agents.providersPanel.columns.provider}</Th>
                <Th className="hidden md:table-cell">{t.app.modules.agents.providersPanel.columns.credential}</Th>
                <Th className="hidden lg:table-cell">{t.app.modules.agents.providersPanel.columns.defaultModel}</Th>
                <Th numeric>{t.app.modules.agents.providersPanel.columns.spend}</Th>
                <Th numeric>{t.app.modules.agents.providersPanel.columns.actions}</Th>
              </tr>
            </thead>
            <tbody>
              {providers.map((p) => (
                <ProviderRow
                  key={p.id}
                  provider={p}
                  rate={rate}
                  onDelete={() => deleteProvider.mutate(p.id)}
                />
              ))}
            </tbody>
          </table>
        </TableScroll>
      ) : null}

      {providers.length > 0 ? (
        <footer className="border-t border-(--color-border) px-4 py-2">
          <FxFooter fx={fx} />
        </footer>
      ) : null}

      {deleteProvider.error ? (
        <p className="border-t border-(--color-border) px-4 py-2 text-xs text-(--color-destructive)">
          {deleteProvider.error.message}
        </p>
      ) : null}
    </section>
  );
}

function EmptyHint() {
  const t = useT();
  return (
    <div className="px-4 py-8 text-center">
      <KeyRound className="mx-auto size-5 text-(--color-muted-foreground)" />
      <p className="mt-2 text-sm text-(--color-foreground)">{t.app.modules.agents.providersPanel.empty.title}</p>
      <p className="mx-auto mt-1 max-w-sm text-xs leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.providersPanel.empty.body}
      </p>
    </div>
  );
}

function ProviderRow({
  provider,
  rate,
  onDelete,
}: {
  provider: ApiProvider;
  rate: number | null;
  onDelete: () => void;
}) {
  const t = useT();
  const testConnection = useProviderModels();
  const updateProvider = useUpdateProvider();
  const spend = useProviderSpend(provider.id);
  const [rotating, setRotating] = useState(false);
  const [newKey, setNewKey] = useState("");

  const expanded = rotating || testConnection.isSuccess || testConnection.isError;

  return (
    <>
      <tr className="border-t border-(--color-border) transition-colors hover:bg-(--color-muted)/40">
        <Td>
          <p className="truncate font-medium text-(--color-foreground)">{provider.name}</p>
          <p className="truncate font-mono text-[11px] text-(--color-muted-foreground)">
            {provider.base_url}
          </p>
        </Td>
        <Td className="hidden md:table-cell">
          <span className="font-mono text-[11px] text-(--color-muted-foreground)">
            {provider.api_key_hint}
          </span>
        </Td>
        <Td className="hidden lg:table-cell">
          <span className="font-mono text-[11px] text-(--color-muted-foreground)">
            {provider.default_model || "—"}
          </span>
        </Td>
        <Td numeric>
          {spend.isLoading ? (
            <Loader2 className="ml-auto size-4 animate-spin text-(--color-muted-foreground)" />
          ) : spend.isError ? (
            <span
              className="inline-flex items-center gap-1 text-[11px] text-(--color-muted-foreground)"
              title={spend.error instanceof Error ? spend.error.message : t.app.modules.agents.providersPanel.unavailable}
            >
              <TriangleAlert className="size-3.5" />
              {t.app.modules.agents.providersPanel.unavailable}
            </span>
          ) : spend.data ? (
            <>
              <Money usd={spend.data.spend} rate={rate} />
              {spend.data.max_budget != null && spend.data.max_budget > 0 ? (
                <BudgetBar spent={spend.data.spend} budget={spend.data.max_budget} rate={rate} />
              ) : null}
            </>
          ) : null}
        </Td>
        <Td numeric>
          <div className="flex items-center justify-end gap-1">
            <Button
              size="sm"
              variant="outline"
              onClick={() => testConnection.mutate(provider.id)}
              disabled={testConnection.isPending}
            >
              {testConnection.isPending ? <Loader2 className="animate-spin" /> : null}
              {t.app.modules.agents.providersPanel.test}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setRotating((v) => !v)}>
              {t.app.modules.agents.providersPanel.rotate}
            </Button>
            <button
              type="button"
              onClick={onDelete}
              aria-label={t.app.modules.agents.providersPanel.removeProvider.replace("{name}", provider.name)}
              className="rounded-lg p-2 text-(--color-muted-foreground) hover:bg-(--color-destructive)/10 hover:text-(--color-destructive)"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
        </Td>
      </tr>

      {expanded ? (
        <ExpandedRow colSpan={COLUMNS}>
          <div className="space-y-2">
            {rotating ? (
              <div className="flex items-end gap-2">
                <div className="flex-1 space-y-1">
                  <Label htmlFor={`key-${provider.id}`}>{t.app.modules.agents.providersPanel.newKey}</Label>
                  <Input
                    id={`key-${provider.id}`}
                    type="password"
                    autoComplete="off"
                    placeholder="sk-…"
                    value={newKey}
                    onChange={(e) => setNewKey(e.target.value)}
                  />
                </div>
                <Button
                  size="md"
                  disabled={!newKey.trim() || updateProvider.isPending}
                  onClick={async () => {
                    await updateProvider.mutateAsync({
                      id: provider.id,
                      body: { api_key: newKey.trim() },
                    });
                    setNewKey("");
                    setRotating(false);
                  }}
                >
                  {t.app.modules.agents.providersPanel.save}
                </Button>
              </div>
            ) : null}

            {testConnection.isSuccess ? (
              <div className="flex items-start gap-2 rounded-lg bg-(--color-card) px-2.5 py-2">
                <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-(--color-brand-600)" />
                <p className="text-[11px] leading-relaxed text-(--color-muted-foreground)">
                  {t.app.modules.agents.providersPanel.connected.replace(
                    "{count}",
                    String(testConnection.data.length),
                  )}{" "}
                  <span className="font-mono">
                    {testConnection.data
                      .slice(0, 6)
                      .map((m) => m.id)
                      .join(", ")}
                    {testConnection.data.length > 6 ? "…" : ""}
                  </span>
                </p>
              </div>
            ) : null}

            {testConnection.isError ? (
              <div className="flex items-start gap-2 rounded-lg border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-2.5 py-2">
                <XCircle className="mt-0.5 size-3.5 shrink-0 text-(--color-destructive)" />
                <p className="text-[11px] leading-relaxed text-(--color-destructive)">
                  {testConnection.error.message}
                </p>
              </div>
            ) : null}
          </div>
        </ExpandedRow>
      ) : null}
    </>
  );
}

/** How much of a capped key has been burned. Only shown when a cap exists. */
function BudgetBar({
  spent,
  budget,
  rate,
}: {
  spent: number;
  budget: number;
  rate: number | null;
}) {
  const t = useT();
  const pct = Math.min(100, Math.round((spent / budget) * 100));
  const hot = pct >= 80;
  const cap = rate != null ? formatBRL(budget * rate) : formatUSD(budget);
  const label = t.app.modules.agents.providersPanel.budgetOf
    .replace("{pct}", String(pct))
    .replace("{cap}", cap);
  return (
    <div className="ml-auto mt-1 w-24" title={label}>
      <div className="h-1 w-full overflow-hidden rounded-full bg-(--color-muted)">
        <div
          className={hot ? "h-full bg-(--color-destructive)" : "h-full bg-(--color-brand-500)"}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-0.5 text-[10px] tabular-nums text-(--color-muted-foreground)">
        {label}
      </p>
    </div>
  );
}

interface FormValues {
  name: string;
  baseUrl: string;
  apiKey: string;
  defaultModel: string;
}

function ProviderForm({
  onSubmit,
  onCancel,
  submitting,
  error,
}: {
  onSubmit: (v: FormValues) => void | Promise<void>;
  onCancel: () => void;
  submitting: boolean;
  error: string | null;
}) {
  const t = useT();
  const [values, setValues] = useState<FormValues>({
    name: "",
    baseUrl: "",
    apiKey: "",
    defaultModel: "",
  });

  const set = (k: keyof FormValues) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setValues((v) => ({ ...v, [k]: e.target.value }));

  const complete =
    values.name.trim() && values.baseUrl.trim() && values.apiKey.trim() && values.defaultModel.trim();

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        void onSubmit(values);
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="p-name">{t.app.modules.agents.providersPanel.form.name}</Label>
          <Input id="p-name" value={values.name} onChange={set("name")} placeholder="LiteLLM" />
        </div>
        <div className="space-y-1">
          <Label htmlFor="p-url">{t.app.modules.agents.providersPanel.form.baseUrl}</Label>
          <Input
            id="p-url"
            value={values.baseUrl}
            onChange={set("baseUrl")}
            placeholder="https://litellm.seu-dominio.dev/v1"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="p-key">{t.app.modules.agents.providersPanel.form.credential}</Label>
          <Input
            id="p-key"
            type="password"
            autoComplete="off"
            value={values.apiKey}
            onChange={set("apiKey")}
            placeholder="sk-…"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="p-model">{t.app.modules.agents.providersPanel.form.defaultModel}</Label>
          <Input
            id="p-model"
            value={values.defaultModel}
            onChange={set("defaultModel")}
            placeholder="claude-opus-5"
          />
        </div>
      </div>

      <p className="text-[11px] leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.providersPanel.form.hintLead} <span className="font-mono">/chat/completions</span>.{" "}
        {t.app.modules.agents.providersPanel.form.hintTail} <span className="font-mono">/v1</span>.
      </p>

      {error ? (
        <p className={cn("text-xs leading-relaxed text-(--color-destructive)")}>{error}</p>
      ) : null}

      <div className="flex items-center gap-2">
        <Button type="submit" size="sm" disabled={!complete || submitting}>
          {submitting ? <Loader2 className="animate-spin" /> : null}
          {t.app.modules.agents.providersPanel.form.submit}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
          {t.app.modules.agents.providersPanel.form.cancel}
        </Button>
      </div>
    </form>
  );
}
