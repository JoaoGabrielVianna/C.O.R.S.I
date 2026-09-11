import { useState } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { PageHeader } from "@/components/workspace";
import { ApiError } from "@/lib/api/client";
import { useT } from "@/lib/i18n";
import { AgentForm } from "@/modules/agents/components/AgentForm";
import { useCreateAgent } from "@/modules/agents/hooks/useAgents";
import { useProviders } from "@/modules/agents/hooks/useProviders";

/**
 * Creating an agent.
 *
 * Four decisions: what it is called, what it is for, how it should behave,
 * and which model answers. Temperature, reply ceiling and history window
 * all have defaults that work, and asking about them here would mean
 * understanding the whole architecture before owning a single agent.
 *
 * The one field the domain forces is the provider, and it is asked only
 * when there is more than one to choose from — see AgentForm.
 *
 * On success it goes straight into the agent's conversations, because
 * creating one is almost always the first half of talking to it.
 */
export function NewAgentPage() {
  const t = useT();
  const navigate = useNavigate();
  const providersQuery = useProviders();
  const createAgent = useCreateAgent();
  const [error, setError] = useState<string | null>(null);

  const providers = providersQuery.data ?? [];

  if (providersQuery.isLoading) {
    return <p className="text-xs text-(--color-muted-foreground)">{t.app.modules.agents.common.loading}</p>;
  }
  // Without a provider there is nothing to create against; the Home says so
  // properly, so send the request back there rather than showing a form
  // that cannot be submitted.
  if (providers.length === 0) {
    return <Navigate to="/app/modules/agents" replace />;
  }

  return (
    <div className="mx-auto w-full max-w-2xl space-y-5">
      <Link
        to="/app/modules/agents"
        className="inline-flex items-center gap-1 font-mono text-[10.5px] uppercase tracking-[0.18em] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
      >
        <ArrowLeft className="size-3" />
        {t.app.modules.agents.home.title}
      </Link>

      <PageHeader
        className="shrink-0"
        title={t.app.modules.agents.new.title}
        description={t.app.modules.agents.new.description}
      />

      <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card) sm:p-5">
        <AgentForm
          variant="create"
          providers={providers}
          submitLabel={t.app.modules.agents.new.submitLabel}
          submitting={createAgent.isPending}
          error={error}
          onCancel={() => navigate("/app/modules/agents")}
          onSubmit={async (values) => {
            setError(null);
            try {
              const created = await createAgent.mutateAsync({
                provider_id: values.providerId,
                name: values.name,
                description: values.description,
                system_prompt: values.systemPrompt,
                model: values.model || undefined,
              });
              navigate(`/app/modules/agents/${created.id}/conversations`, { replace: true });
            } catch (err) {
              setError(err instanceof ApiError ? err.message : String(err));
            }
          }}
        />
      </div>

      <p className="text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.new.footnote}
      </p>
    </div>
  );
}
