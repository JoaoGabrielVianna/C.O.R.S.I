import { useState } from "react";
import { Link, useNavigate, useOutletContext } from "react-router-dom";
import { AlertTriangle, BookText, Pencil, Plus, Power, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import type { ApiSource } from "@/modules/agents/api/sources";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { ConfirmDialog } from "@/modules/agents/components/ConfirmDialog";
import { formatTokens } from "@/modules/agents/format";
import { useDeleteSource, useSources, useUpdateSource } from "@/modules/agents/hooks/useSources";
import { useFormat, useT } from "@/lib/i18n";

/**
 * Sources — the agent's library of reference material.
 *
 * ── A library, not a document manager ──────────────────────────────────
 * The whole screen is a shelf: what is here, how big each thing is, and
 * which of them the agent is actually reading. No folders, no tags, no
 * versions, no metadata panel. Adding one of those would be building a
 * document management system to answer a question a list already answers.
 *
 * ── The toggle is the primary action ───────────────────────────────────
 * Switching a source on and off is what happens weekly; editing one is
 * what happens monthly. So the state is the first thing on the row and the
 * edit is behind the title.
 *
 * ── Why the size is always visible ─────────────────────────────────────
 * It is what turns "switch this on" from a free action into a cost
 * decision. Both numbers come from the server — the character count is
 * exact, the token figure is the module's one heuristic — so nothing here
 * is a second opinion about what a turn will carry.
 */
export function AgentSourcesPage() {
  const fmt = useFormat();
  const t = useT();
  const { agent } = useOutletContext<AgentContext>();
  const navigate = useNavigate();
  const query = useSources(agent.id);
  const updateSource = useUpdateSource(agent.id);
  const removeSource = useDeleteSource(agent.id);

  const [pendingDelete, setPendingDelete] = useState<ApiSource | null>(null);
  const [error, setError] = useState<string | null>(null);

  const base = `/app/modules/agents/${agent.id}/sources`;
  const page = query.data;
  const items = page?.items ?? [];

  const toggle = (source: ApiSource) => {
    setError(null);
    updateSource.mutate(
      { id: source.id, body: { enabled: !source.enabled } },
      { onError: (err) => setError(messageOf(err)) },
    );
  };

  const confirmDelete = () => {
    if (!pendingDelete) return;
    setError(null);
    removeSource.mutate(pendingDelete.id, {
      onSuccess: () => setPendingDelete(null),
      onError: (err) => setError(messageOf(err)),
    });
  };

  const busy = updateSource.isPending || removeSource.isPending;
  const over = page ? page.used_characters > page.budget_characters : false;

  return (
    <section className="flex min-h-0 flex-1 flex-col gap-3">
      <header className="flex shrink-0 flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-(--color-foreground)">{t.app.modules.agents.sources.title}</h2>
          <p className="mt-0.5 max-w-2xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.interp.sourcesLead.replace("{agent}", agent.name)}{" "}
            {t.app.modules.agents.interp.sourcesTail}
          </p>
        </div>
        <div className="flex items-center gap-3">
          {page ? (
            <span
              className={cn(
                "font-mono text-[10.5px]",
                over ? "text-(--color-destructive)" : "text-(--color-muted-foreground)",
              )}
            >
              ~{formatTokens(Math.ceil(page.used_characters / 4))} de ~
              {formatTokens(Math.ceil(page.budget_characters / 4))} tokens
            </span>
          ) : null}
          <Button size="sm" asChild>
            <Link to={`${base}/new`}>
              <Plus />
              {t.app.modules.agents.sources.new}
            </Link>
          </Button>
        </div>
      </header>

      {error ? (
        <p className="shrink-0 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2 text-[11.5px] text-(--color-destructive)">
          {error}
        </p>
      ) : null}

      {query.isLoading ? (
        <div className="min-h-0 flex-1 space-y-2">
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="h-[72px] animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40"
            />
          ))}
        </div>
      ) : query.isError ? (
        <Empty
          title={t.app.modules.agents.sources.loadFailed}
          description={messageOf(query.error)}
          action={
            <Button size="sm" variant="outline" onClick={() => void query.refetch()}>
              {t.app.modules.agents.sources.retry}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <Empty
          title={`${agent.name} ainda não tem fontes`}
          description={t.app.modules.agents.sources.empty}
          action={
            <Button size="sm" onClick={() => navigate(`${base}/new`)}>
              {t.app.modules.agents.sources.new}
            </Button>
          }
        />
      ) : (
        <div className="min-h-0 flex-1 space-y-2 overflow-y-auto pr-0.5">
          {page && page.total > items.length ? (
            <p className="rounded-xl border border-dashed border-(--color-border) px-3 py-2 text-[11px] text-(--color-muted-foreground)">
              {t.app.modules.agents.interp.sourcesTruncated
                .replace("{count}", fmt.number(items.length))
                .replace("{total}", fmt.number(page.total))}{" "}
              {t.app.modules.agents.interp.truncatedTail}
            </p>
          ) : null}

          {items.map((source) => (
            <SourceRow
              key={source.id}
              source={source}
              href={`${base}/${source.id}`}
              busy={busy}
              onToggle={() => toggle(source)}
              onDelete={() => {
                setError(null);
                setPendingDelete(source);
              }}
            />
          ))}
        </div>
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title={t.app.modules.agents.sources.confirmDelete}
        subject={pendingDelete?.title}
        description={t.app.modules.agents.sources.confirmBody}
        busy={removeSource.isPending}
        error={error}
        onConfirm={confirmDelete}
        onCancel={() => {
          setPendingDelete(null);
          setError(null);
        }}
      />
    </section>
  );
}

function SourceRow({
  source,
  href,
  busy,
  onToggle,
  onDelete,
}: {
  source: ApiSource;
  href: string;
  busy: boolean;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const fmt = useFormat();
  return (
    <article
      className={cn(
        "group rounded-2xl border border-(--color-border) bg-(--color-card) px-3 py-2.5",
        !source.enabled && "opacity-60",
      )}
    >
      <div className="flex items-start gap-2.5">
        <StateDot source={source} />

        <div className="min-w-0 flex-1">
          <Link
            to={href}
            className="block truncate text-[13px] font-medium text-(--color-foreground) transition-colors hover:text-(--color-brand-600)"
          >
            {source.title}
          </Link>
          {source.description ? (
            <p className="mt-0.5 line-clamp-2 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {source.description}
            </p>
          ) : null}
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="font-mono text-[10.5px] text-(--color-muted-foreground)">
              ~{formatTokens(source.estimated_tokens)} tok ·{" "}
              {fmt.number(source.characters)} car.
            </span>
            <StateChip source={source} />
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity duration-200 focus-within:opacity-100 group-hover:opacity-100">
          <RowAction
            label={source.enabled ? "Desligar" : "Ligar"}
            icon={Power}
            disabled={busy}
            onClick={onToggle}
          />
          <RowAction label={t.app.modules.agents.sources.edit} icon={Pencil} href={href} />
          <RowAction label={t.app.modules.agents.sources.delete} icon={Trash2} disabled={busy} destructive onClick={onDelete} />
        </div>
      </div>

      {/* The permanent warning §4.1 requires. A source larger than the block
          can never appear, no matter what else is switched off, and the user
          has no way to work that out from a size alone. */}
      {source.oversized ? (
        <p className="mt-2 flex items-start gap-1.5 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-2.5 py-1.5 text-[11px] leading-relaxed text-(--color-destructive)">
          <AlertTriangle className="mt-0.5 size-3 shrink-0" />
          {t.app.modules.agents.sources.oversized}
        </p>
      ) : null}
    </article>
  );
}

/** The state, at a glance, on the left where the eye starts. */
function StateDot({ source }: { source: ApiSource }) {
  const tone = !source.enabled
    ? "bg-(--color-muted-foreground)/40"
    : source.in_context
      ? "bg-(--color-brand-500)"
      : "bg-(--color-destructive)/60";
  return <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", tone)} aria-hidden />;
}

/**
 * The state, said exactly.
 *
 * Three distinct facts, and none may be collapsed into another: off is a
 * choice the user made, out-of-budget is one the system made this turn, and
 * too-big is a property of the document itself.
 */
function StateChip({ source }: { source: ApiSource }) {
  const t = useT();
  if (!source.enabled) return <Chip tone="muted">{t.app.modules.agents.sources.off}</Chip>;
  if (source.oversized) return <Chip tone="warn">{t.app.modules.agents.sources.doesNotFit}</Chip>;
  if (!source.in_context) return <Chip tone="warn">{t.app.modules.agents.sources.overBudget}</Chip>;
  return <Chip tone="on">{t.app.modules.agents.sources.inContext}</Chip>;
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

function RowAction({
  label,
  icon: Icon,
  onClick,
  href,
  disabled,
  destructive,
}: {
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  onClick?: () => void;
  href?: string;
  disabled?: boolean;
  destructive?: boolean;
}) {
  const className = cn(
    "inline-flex rounded-lg p-1.5 transition-colors disabled:opacity-40",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
    destructive
      ? "text-(--color-muted-foreground) hover:bg-(--color-destructive)/10 hover:text-(--color-destructive)"
      : "text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
  );

  if (href) {
    return (
      <Link to={href} title={label} aria-label={label} className={className}>
        <Icon className="size-3.5" />
      </Link>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className={className}
    >
      <Icon className="size-3.5" />
    </button>
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
          <BookText className="size-4" />
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
