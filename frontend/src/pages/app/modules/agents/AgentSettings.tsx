import { BudgetCard } from "@/modules/agents/components/budget/BudgetCard";
import { useState } from "react";
import { Link, useNavigate, useOutletContext } from "react-router-dom";
import { Trash2, Wrench } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { useT } from "@/lib/i18n";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { AgentForm } from "@/modules/agents/components/AgentForm";
import { ConfirmDialog } from "@/modules/agents/components/ConfirmDialog";
import { MemoryPolicyCard } from "@/modules/agents/components/memory/MemoryPolicyCard";
import { useDeleteAgent, useUpdateAgent } from "@/modules/agents/hooks/useAgents";
import { useProviders } from "@/modules/agents/hooks/useProviders";
import { useAgentTools } from "@/modules/agents/hooks/useTools";

/**
 * What the agent is: its instructions, its model, its knobs.
 *
 * Instructions come first and at full height because they are the agent's
 * definition, not one setting among eight. Everything that has a working
 * default is folded into "Avançado", which starts closed.
 *
 * Editing here changes every future turn in every thread that uses this
 * agent. It does not rewrite anything already recorded: the model is
 * stamped per message, so an old turn keeps reading as the turn it was.
 */
export function AgentSettingsPage() {
  const t = useT();
  const { agent } = useOutletContext<AgentContext>();
  const navigate = useNavigate();
  const providersQuery = useProviders();
  const updateAgent = useUpdateAgent();
  const deleteAgent = useDeleteAgent();

  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const providers = providersQuery.data ?? [];

  return (
    <section className="mx-auto min-h-0 w-full max-w-3xl space-y-6 overflow-y-auto pb-2">
      <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card) sm:p-5">
        {/* Remounted on the agent's updated_at so a save leaves the form
            holding what the server actually stored, not the draft. */}
        <AgentForm
          key={agent.updated_at}
          variant="settings"
          providers={providers}
          initial={agent}
          submitLabel={t.app.modules.agents.settings.submitLabel}
          submitting={updateAgent.isPending}
          error={error}
          onSubmit={async (values) => {
            setError(null);
            setSaved(false);
            try {
              await updateAgent.mutateAsync({
                id: agent.id,
                body: {
                  provider_id: values.providerId,
                  name: values.name,
                  description: values.description,
                  system_prompt: values.systemPrompt,
                  model: values.model || undefined,
                  temperature: values.temperature,
                  history_limit: values.historyLimit,
                  max_tokens: values.maxTokens,
                },
              });
              setSaved(true);
            } catch (err) {
              setError(err instanceof ApiError ? err.message : String(err));
            }
          }}
        />
        {saved ? (
          <p className="mt-3 text-[11.5px] text-(--color-muted-foreground)">
            {t.app.modules.agents.settings.saved}
          </p>
        ) : null}
      </div>

      {/* Its own card: everything in the form above changes how the agent
          behaves, and this changes whether it may run at all. */}
      <BudgetCard agent={agent} />

      {/* And its own card for the same reason: the form above decides how
          the agent answers, this decides what may be remembered about the
          person asking, across every future conversation. */}
      <MemoryPolicyCard agent={agent} />

      <ToolsCard agentId={agent.id} />

      <div className="rounded-2xl border border-(--color-destructive)/30 bg-(--color-card) p-4 shadow-(--shadow-card) sm:p-5">
        <h2 className="text-[13px] font-semibold text-(--color-foreground)">{t.app.modules.agents.settings.danger.title}</h2>
        <p className="mt-1 max-w-xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
          {t.app.modules.agents.settings.danger.body}
        </p>
        <div className="mt-3">
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setDeleteError(null);
              setConfirmingDelete(true);
            }}
          >
            <Trash2 />
            {t.app.modules.agents.common.remove}
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={confirmingDelete}
        title={t.app.modules.agents.settings.danger.confirmTitle}
        subject={agent.name}
        description={t.app.modules.agents.settings.danger.confirmBody}
        confirmLabel={t.app.modules.agents.common.remove}
        busy={deleteAgent.isPending}
        error={deleteError}
        onConfirm={async () => {
          setDeleteError(null);
          try {
            await deleteAgent.mutateAsync(agent.id);
            setConfirmingDelete(false);
            navigate("/app/modules/agents", { replace: true });
          } catch (err) {
            setDeleteError(err instanceof ApiError ? err.message : String(err));
          }
        }}
        onCancel={() => {
          setConfirmingDelete(false);
          setDeleteError(null);
        }}
      />
    </section>
  );
}

/**
 * Tools, as a pointer rather than a second set of controls.
 *
 * The switches live on the agent's Tools tab, and duplicating them here
 * would create two places that grant the same permission — which is one
 * place too many for a decision about what an agent is allowed to do. What
 * Settings owes the reader is the state: how many capabilities this agent
 * has, and where to change it.
 */
function ToolsCard({ agentId }: { agentId: string }) {
  const t = useT();
  const query = useAgentTools(agentId);
  const report = query.data;

  return (
    <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card) sm:p-5">
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-(--color-muted) text-(--color-muted-foreground)">
          <Wrench className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-[13px] font-semibold text-(--color-foreground)">{t.app.modules.agents.settings.tools.title}</h2>
          <p className="mt-1 max-w-xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {report
              ? report.authorized_count === 0
                ? t.app.modules.agents.settings.tools.none
                : t.app.modules.agents.settings.tools.count
                    .replace("{authorized}", String(report.authorized_count))
                    .replace("{total}", String(report.items.length))
              : t.app.modules.agents.common.loading}
          </p>
        </div>
        <Button size="sm" variant="outline" asChild className="shrink-0">
          <Link to={`/app/modules/agents/${agentId}/tools`}>{t.app.modules.agents.common.manage}</Link>
        </Button>
      </div>
    </div>
  );
}
