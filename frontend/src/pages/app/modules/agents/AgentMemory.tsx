import { useFormat } from "@/lib/i18n";
import { useCallback, useMemo, useState } from "react";
import { NavLink, Outlet, useOutletContext } from "react-router-dom";
import { Brain, Network, Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import type { ApiMemory } from "@/modules/agents/api/memories";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { ConfirmDialog } from "@/modules/agents/components/ConfirmDialog";
import { MemoryDialog } from "@/modules/agents/components/memory/MemoryDialog";
import type { MemoryActions } from "@/modules/agents/components/memory/memoryActions";
import type { MemoryContext } from "@/modules/agents/components/memory/memoryContext";
import { useT } from "@/lib/i18n";
import {
  useCreateMemory,
  useDeleteMemory,
  useMemories,
  useUpdateMemory,
} from "@/modules/agents/hooks/useMemories";

/**
 * Memory — one collection, two ways to look at it.
 *
 *   Memories   the operational view: read, search, edit, turn off, delete
 *   Brain      the exploratory view: the same rows as a graph of origins
 *
 * ── Why the tabs are routes ────────────────────────────────────────────
 * The module decided that a tab is an address, for the same
 * reasons everywhere else: a view worth switching to is worth linking to,
 * and reloading should land where you were. `/memory` and `/memory/brain`
 * cost one route each and keep that rule intact.
 *
 * ── Why the query and the dialogs live here ────────────────────────────
 * Both tabs read the same list and offer the same four actions. Holding
 * them in the layout means switching views does not refetch, does not lose
 * an open editor, and cannot produce two components disagreeing about which
 * memory is being deleted.
 */
export function AgentMemoryPage() {
  const t = useT();
  const fmt = useFormat();
  const { agent } = useOutletContext<AgentContext>();
  const query = useMemories(agent.id);

  const createMemory = useCreateMemory(agent.id);
  const updateMemory = useUpdateMemory(agent.id);
  const removeMemory = useDeleteMemory(agent.id);

  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<ApiMemory | null>(null);
  const [deleting, setDeleting] = useState<ApiMemory | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);
  /** Failures of the two immediate toggles, which have no dialog to land in. */
  const [actionError, setActionError] = useState<string | null>(null);

  const toggle = useCallback(
    (m: ApiMemory, body: { enabled?: boolean; pinned?: boolean }) => {
      setActionError(null);
      updateMemory.mutate(
        { id: m.id, body },
        { onError: (err) => setActionError(messageOf(err)) },
      );
    },
    [updateMemory],
  );

  const actions = useMemo<MemoryActions>(
    () => ({
      startEdit: (m) => {
        setDialogError(null);
        setEditing(m);
      },
      startDelete: (m) => {
        setDialogError(null);
        setDeleting(m);
      },
      toggleEnabled: (m) => toggle(m, { enabled: !m.enabled }),
      togglePinned: (m) => toggle(m, { pinned: !m.pinned }),
    }),
    [toggle],
  );

  const context: MemoryContext = {
    agent,
    page: query.data,
    isLoading: query.isLoading,
    error: query.isError ? messageOf(query.error) : null,
    refetch: () => void query.refetch(),
    actions,
    busy: updateMemory.isPending || removeMemory.isPending,
    onCreate: () => {
      setDialogError(null);
      setCreating(true);
    },
  };

  return (
    <section className="flex min-h-0 flex-1 flex-col gap-3">
      <header className="flex shrink-0 flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-(--color-foreground)">{t.app.modules.agents.memory.title}</h2>
          <p className="mt-0.5 max-w-2xl text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.interp.memoryPageLead.replace("{agent}", agent.name)}{" "}
            {t.app.modules.agents.interp.memoryPageTail.replace(
              "{budget}",
              fmt.number(query.data?.budget_characters ?? 4000),
            )}
          </p>
        </div>
        <Button size="sm" onClick={context.onCreate}>
          <Plus />
          {t.app.modules.agents.memory.new}
        </Button>
      </header>

      <nav aria-label={t.app.modules.agents.memory.viewsLabel} className="flex shrink-0 items-center gap-1">
        <MemoryTab to="." end icon={Brain} label={t.app.modules.agents.memory.views.list} />
        <MemoryTab to="brain" icon={Network} label={t.app.modules.agents.memory.views.brain} />
      </nav>

      {actionError ? (
        <p className="shrink-0 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2 text-[11.5px] text-(--color-destructive)">
          {actionError}
        </p>
      ) : null}

      <Outlet context={context} />

      {creating ? (
        <MemoryDialog
          title={t.app.modules.agents.memory.dialog.new}
          agentName={agent.name}
          busy={createMemory.isPending}
          error={dialogError}
          onCancel={() => setCreating(false)}
          onConfirm={(content) => {
            setDialogError(null);
            createMemory.mutate(
              { content },
              {
                onSuccess: () => setCreating(false),
                onError: (err) => setDialogError(messageOf(err)),
              },
            );
          }}
        />
      ) : null}

      {editing ? (
        <MemoryDialog
          title={t.app.modules.agents.memory.dialog.edit}
          agentName={agent.name}
          initialContent={editing.content}
          busy={updateMemory.isPending}
          error={dialogError}
          onCancel={() => setEditing(null)}
          onConfirm={(content) => {
            setDialogError(null);
            updateMemory.mutate(
              { id: editing.id, body: { content } },
              {
                onSuccess: () => setEditing(null),
                onError: (err) => setDialogError(messageOf(err)),
              },
            );
          }}
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        title={t.app.modules.agents.memory.dialog.confirmDelete}
        subject={deleting?.content}
        description={t.app.modules.agents.memory.dialog.confirmBody}
        busy={removeMemory.isPending}
        error={dialogError}
        onCancel={() => {
          setDeleting(null);
          setDialogError(null);
        }}
        onConfirm={() => {
          if (!deleting) return;
          setDialogError(null);
          removeMemory.mutate(deleting.id, {
            onSuccess: () => setDeleting(null),
            onError: (err) => setDialogError(messageOf(err)),
          });
        }}
      />
    </section>
  );
}

function MemoryTab({
  to,
  end,
  icon: Icon,
  label,
}: {
  to: string;
  end?: boolean;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  label: string;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          "inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-[11.5px] font-medium",
          "transition-colors duration-[250ms] [transition-timing-function:var(--ease-premium)]",
          isActive
            ? "bg-(--color-muted) text-(--color-foreground)"
            : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
        )
      }
    >
      <Icon className="size-3.5" />
      {label}
    </NavLink>
  );
}

function messageOf(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
