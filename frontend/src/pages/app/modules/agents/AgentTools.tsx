import { useMemo, useState } from "react";
import { Link, useOutletContext } from "react-router-dom";
import { AlertTriangle, Eye, FlaskConical, Pencil, PlugZap, Wrench } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import type { ApiTool } from "@/modules/agents/api/tools";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { useAgentTools, useSetToolAuthorization } from "@/modules/agents/hooks/useTools";
import { authorizedCount, groupTools, type ToolGroup } from "@/modules/agents/toolGroups";
import { useGitHubStatus } from "@/modules/integrations/hooks/useGitHub";
import { useT } from "@/lib/i18n";

/**
 * Tools — what this agent is allowed to do.
 *
 * ── The screen is an authorization list, not a catalogue ───────────────
 * Every tool the backend implements is on it, and each row is one decision:
 * may this agent use this. Nothing here creates, configures or parameterises
 * a tool — a tool is code, and a screen that pretended otherwise would be
 * offering a capability the running system does not have.
 *
 * ── Default off, and it stays visible ──────────────────────────────────
 * A new agent has nothing authorized. The tools it does not have are shown
 * anyway, switched off, because "what could this agent do" is the question
 * somebody opens this page with, and an empty list answers a different one.
 *
 * ── Grouped by provider, derived and never listed ──────────────────────
 * The rows are grouped by the system behind them, so seven GitHub
 * capabilities read as one integration with seven switches rather than as
 * seven unrelated entries. Both the grouping and the labels are computed
 * from what the backend already sent — see `toolGroups.ts`. There is no
 * list of providers in this file: a capability from an integration that
 * does not exist yet groups correctly the first time it appears.
 *
 * ── Why an integration's connection state is on THIS page ──────────────
 * Because a switch that can be turned on while the integration behind it is
 * not connected is a switch that promises something the next turn will
 * refuse. The rows stay visible and stay operable — the backend is the
 * authority on what EXISTS, and hiding a capability because a credential is
 * missing would be the browser overruling it. What the page adds is the
 * missing sentence, and a way to go fix it.
 *
 * This is a page, not a module, so composing Agents with Integrations here
 * is the screen's own composition — the same move `cmd/corsi` makes on the
 * backend, and the reason neither module imports the other.
 *
 * ── Why the schema is not on the page ──────────────────────────────────
 * The input contract is what the model reads, not what the user decides.
 * Rendering raw JSON Schema here would be showing an implementation detail
 * in the one place a person is making a permission decision. What the row
 * carries instead is what that decision needs: the name, what it does, and
 * whether it only reads.
 */
export function AgentToolsPage() {
  const t = useT();
  const { agent } = useOutletContext<AgentContext>();
  const query = useAgentTools(agent.id);
  const setAuthorization = useSetToolAuthorization(agent.id);
  const [error, setError] = useState<string | null>(null);

  // The one integration whose connection state this build can read. A
  // second one means a second hook here — which is honest work, because
  // that state genuinely has to come from somewhere, and not a table of
  // provider names.
  const github = useGitHubStatus();

  const report = query.data;
  // Derived from `report` rather than from a `report?.items ?? []` above it:
  // that fallback builds a new array on every render, so the memo would
  // never hit and the dependency would be a lie.
  const groups = useMemo(() => groupTools(report?.items ?? []), [report]);
  const total = report?.items.length ?? 0;

  const toggle = (tool: ApiTool) => {
    setError(null);
    setAuthorization.mutate(
      { toolName: tool.name, authorized: !tool.authorized },
      { onError: (err) => setError(messageOf(err)) },
    );
  };

  /**
   * The sentence a group needs beyond its switches.
   *
   * Returns null for a group there is nothing to say about, which is every
   * group whose capabilities need no external system. The GitHub notice is
   * suppressed while its status is still loading, so the page never flashes
   * "not connected" at somebody who is connected.
   */
  const noticeFor = (group: ToolGroup): React.ReactNode => {
    if (group.key !== "github") return null;
    if (github.isLoading || github.data?.connected) return null;
    return (
      <GroupNotice>
        {t.app.modules.agents.tools.notConnected}
        <Link
          to="/app/settings/integrations"
          className="ml-1 font-medium text-(--color-brand-600) underline-offset-4 hover:underline"
        >
          {t.app.modules.agents.tools.connectGitHub}
        </Link>
      </GroupNotice>
    );
  };

  return (
    <section className="flex min-h-0 flex-1 flex-col gap-3">
      <header className="flex shrink-0 flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-(--color-foreground)">{t.app.modules.agents.tools.title}</h2>
          <p className="mt-0.5 max-w-2xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.interp.toolsLead.replace("{agent}", agent.name)}{" "}
            {t.app.modules.agents.interp.toolsTail}
          </p>
        </div>
        {report ? (
          <span className="font-mono text-[10.5px] text-(--color-muted-foreground)">
            {report.authorized_count} de {total} liberadas
          </span>
        ) : null}
      </header>

      {error ? (
        <p className="shrink-0 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2 text-[11.5px] text-(--color-destructive)">
          {error}
        </p>
      ) : null}

      {/* A grant left behind by a deploy that removed the tool. It does not
          authorize anything, and it is shown so it can be cleaned up rather
          than sitting in a table nobody can see. */}
      {report?.stale?.length ? (
        <p className="shrink-0 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2 text-[11.5px] leading-relaxed text-(--color-destructive)">
          <AlertTriangle className="mr-1.5 inline size-3" />
          {t.app.modules.agents.tools.staleGrants}{" "}
          <span className="font-mono">{report.stale.join(", ")}</span>
          {t.app.modules.agents.tools.noAccessTail}
        </p>
      ) : null}

      {query.isLoading ? (
        <div className="min-h-0 flex-1 space-y-2">
          {[0, 1].map((i) => (
            <div
              key={i}
              className="h-[76px] animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40"
            />
          ))}
        </div>
      ) : query.isError ? (
        <Empty
          title={t.app.modules.agents.tools.loadFailed}
          description={messageOf(query.error)}
          action={
            <Button size="sm" variant="outline" onClick={() => void query.refetch()}>
              {t.app.modules.agents.tools.retry}
            </Button>
          }
        />
      ) : total === 0 ? (
        <Empty
          title={t.app.modules.agents.tools.noneInBuild}
          description={t.app.modules.agents.tools.noneInBuildBody}
        />
      ) : (
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto pr-0.5">
          {groups.map((group) => (
            <section key={group.key} className="space-y-2">
              <header className="flex items-baseline justify-between gap-3">
                <h3 className="text-[12.5px] font-semibold text-(--color-foreground)">
                  {group.label}
                </h3>
                <span className="shrink-0 font-mono text-[10px] text-(--color-muted-foreground)">
                  {authorizedCount(group)}/{group.tools.length}
                </span>
              </header>

              {noticeFor(group)}

              {group.tools.map((tool) => (
                <ToolRow
                  key={tool.name}
                  tool={tool}
                  label={tool.shortTitle}
                  busy={setAuthorization.isPending}
                  onToggle={() => toggle(tool)}
                />
              ))}
            </section>
          ))}
        </div>
      )}
    </section>
  );
}

/** The sentence a group needs beyond its switches. */
function GroupNotice({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex items-start gap-2 rounded-xl border border-amber-400/30 bg-amber-400/10 px-3 py-2 text-[11.5px] leading-relaxed text-(--color-foreground)">
      <PlugZap className="mt-0.5 size-3 shrink-0 text-amber-500" aria-hidden />
      <span>{children}</span>
    </p>
  );
}

function ToolRow({
  tool,
  label,
  busy,
  onToggle,
}: {
  tool: ApiTool;
  /**
   * The capability without its provider — the group heading already says
   * which system it is. Always derived from the backend's own title; this
   * component never composes one.
   */
  label: string;
  busy: boolean;
  onToggle: () => void;
}) {
  const t = useT();
  const EffectIcon = tool.effect === "read" ? Eye : Pencil;
  return (
    <article
      className={cn(
        "rounded-2xl border border-(--color-border) bg-(--color-card) px-3 py-2.5",
        !tool.authorized && "opacity-70",
      )}
    >
      <div className="flex items-start gap-2.5">
        <span
          className={cn(
            "mt-1.5 size-2 shrink-0 rounded-full",
            tool.authorized ? "bg-(--color-brand-500)" : "bg-(--color-muted-foreground)/40",
          )}
          aria-hidden
        />

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-[13px] font-medium text-(--color-foreground)">{label}</span>
            <span className="font-mono text-[10.5px] text-(--color-muted-foreground)">
              {tool.name}
            </span>
          </div>
          <p className="mt-0.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {tool.description}
          </p>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <Chip tone={tool.effect === "read" ? "muted" : "warn"}>
              <EffectIcon className="mr-1 inline size-2.5" />
              {tool.effect === "read" ? "só leitura" : "escreve"}
            </Chip>
            {/* Said out loud rather than hidden. A tool that exists to prove
                the machinery is not a product capability, and the row is the
                honest place to say so. */}
            {tool.internal ? (
              <Chip tone="muted">
                <FlaskConical className="mr-1 inline size-2.5" />
                {t.app.modules.agents.tools.internalTest}
              </Chip>
            ) : null}
            <Chip tone={tool.authorized ? "on" : "muted"}>
              {tool.authorized ? "liberada" : "bloqueada"}
            </Chip>
          </div>
        </div>

        <Button
          size="sm"
          variant={tool.authorized ? "outline" : "primary"}
          disabled={busy}
          onClick={onToggle}
          className="shrink-0"
        >
          {tool.authorized ? "Bloquear" : "Liberar"}
        </Button>
      </div>
    </article>
  );
}

function Chip({ tone, children }: { tone: "on" | "warn" | "muted"; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "rounded-full px-1.5 py-px font-mono text-[9.5px] uppercase tracking-wider",
        tone === "on" && "bg-(--color-brand-50) text-(--color-brand-700)",
        tone === "warn" && "bg-(--color-muted) text-(--color-foreground)/70",
        tone === "muted" && "bg-(--color-muted) text-(--color-muted-foreground)",
      )}
    >
      {children}
    </span>
  );
}

function Empty({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="min-h-0 flex-1">
      <div className="mx-auto max-w-xl rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-10 text-center">
        <span className="mx-auto flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
          <Wrench className="size-4" />
        </span>
        <h3 className="mt-3 text-sm font-semibold text-(--color-foreground)">{title}</h3>
        <p className="mx-auto mt-2 max-w-md text-[12px] leading-relaxed text-(--color-muted-foreground)">
          {description}
        </p>
        {action ? <div className="mt-4">{action}</div> : null}
      </div>
    </div>
  );
}

function messageOf(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
