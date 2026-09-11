import { useState } from "react";
import { Link, Navigate, useNavigate, useOutletContext, useParams } from "react-router-dom";
import { Bot, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { useT } from "@/lib/i18n";
import type { ApiConversation } from "@/modules/agents/api/conversations";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { ChatView } from "@/modules/agents/components/ChatView";
import { ConfirmDialog } from "@/modules/agents/components/ConfirmDialog";
import { ThreadList } from "@/modules/agents/components/ThreadList";
import {
  useAgentConversations,
  useConversation,
  useCreateConversation,
  useDeleteConversation,
  useRenameConversation,
} from "@/modules/agents/hooks/useConversations";

/**
 * An agent's conversations: the list, and whichever one the URL names.
 *
 * Two routes render this — `/conversations` and `/c/:conversationId` — so
 * opening a thread is a navigation with a real address rather than a state
 * change nobody can link to or come back to.
 *
 * ── Layout ─────────────────────────────────────────────────────────────
 * The page claims the height the shell left it and never scrolls itself;
 * only the transcript and the thread column move. A chat whose composer
 * drifts off-screen as you scroll is unusable. Below `lg` there is no room
 * for a permanent thread column, so it becomes a drawer — hiding it outright
 * would leave no way to switch threads on a narrow window.
 */
export function AgentConversationsPage() {
  const t = useT();
  const { agent } = useOutletContext<AgentContext>();
  const { conversationId } = useParams<{ conversationId: string }>();
  const navigate = useNavigate();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<ApiConversation | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [createError, setCreateError] = useState<string | null>(null);

  const list = useAgentConversations(agent.id);
  const createConversation = useCreateConversation();
  const deleteConversation = useDeleteConversation();
  const renameConversation = useRenameConversation();

  const base = `/app/modules/agents/${agent.id}`;
  const fromList = conversationId
    ? list.conversations.find((c) => c.id === conversationId)
    : undefined;

  /**
   * A link can name a thread the list has not paged in — a bookmark from
   * months ago is the ordinary case — so the record is fetched directly
   * when the page does not already hold it. Enabled only then, so the
   * common path costs no extra request.
   */
  const direct = useConversation(conversationId, Boolean(conversationId) && !fromList);
  const open = fromList ?? direct.data;

  /**
   * A thread id that resolves to nothing: deleted, or owned by a workspace
   * this one is not. The server answers both the same way and so does this
   * — an id in a URL is never authorisation.
   */
  const missing = Boolean(conversationId) && !open && !list.isLoading && !direct.isLoading;

  /**
   * A real thread reached through the wrong agent's URL. Its own agent is
   * the right place for it, so the link is repaired rather than refused:
   * the address in the bar ends up naming what is actually on screen.
   */
  const wrongAgent = open && open.agent_id !== agent.id ? open : null;

  const startConversation = async () => {
    setCreateError(null);
    try {
      const created = await createConversation.mutateAsync({ agentId: agent.id });
      setDrawerOpen(false);
      navigate(`${base}/c/${created.id}`);
    } catch (err) {
      // Reported, not swallowed: the previous version awaited this without
      // a catch, so a failure left an unhandled rejection and a screen that
      // did not change.
      setCreateError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const confirmDelete = async () => {
    if (!pendingDelete) return;
    setDeleteError(null);
    const removed = pendingDelete.id;
    try {
      await deleteConversation.mutateAsync(removed);
      setPendingDelete(null);
      // Deleting the thread you are reading has to move you somewhere that
      // still exists, and the agent's list is the nearest valid place.
      if (removed === conversationId) navigate(`${base}/conversations`, { replace: true });
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : String(err));
    }
  };

  const threadList = (
    <ThreadList
      conversations={list.conversations}
      total={list.total}
      activeId={open?.id ?? null}
      linkTo={(id) => `${base}/c/${id}`}
      onDelete={(c) => {
        setDeleteError(null);
        setPendingDelete(c);
      }}
      onRename={(id, title) => renameConversation.mutate({ id, title })}
      onNew={() => void startConversation()}
      onNavigate={() => setDrawerOpen(false)}
      canCreate={!createConversation.isPending}
      loading={list.isLoading}
      hasMore={list.hasMore}
      loadingMore={list.loadingMore}
      onLoadMore={list.loadMore}
    />
  );

  if (wrongAgent) {
    return (
      <Navigate to={`/app/modules/agents/${wrongAgent.agent_id}/c/${wrongAgent.id}`} replace />
    );
  }

  return (
    <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-[248px_minmax(0,1fr)]">
      <aside className="hidden min-h-0 lg:block">{threadList}</aside>

      <section className="flex min-h-0 flex-col rounded-2xl border border-(--color-border) bg-(--color-card) p-3 shadow-(--shadow-card) sm:p-4">
        {createError ? (
          <p className="mb-2 shrink-0 text-[11.5px] text-(--color-destructive)">{createError}</p>
        ) : null}

        {open ? (
          <ChatView
            key={open.id}
            conversation={open}
            agent={agent}
            onOpenThreads={() => setDrawerOpen(true)}
          />
        ) : missing ? (
          <Placeholder
            title={t.app.modules.agents.conversations.notFound.title}
            description={t.app.modules.agents.conversations.notFound.description}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link to={`${base}/conversations`}>{t.app.modules.agents.conversations.notFound.cta}</Link>
              </Button>
            }
          />
        ) : list.isLoading ? (
          <Placeholder title={t.app.modules.agents.common.loading} />
        ) : list.conversations.length === 0 ? (
          <Placeholder
            title={t.app.modules.agents.conversations.empty.title.replace("{agent}", agent.name)}
            description={t.app.modules.agents.conversations.empty.description}
            action={
              <Button
                size="sm"
                onClick={() => void startConversation()}
                disabled={createConversation.isPending}
              >
                {t.app.modules.agents.home.newConversation}
              </Button>
            }
          />
        ) : (
          <Placeholder
            title={t.app.modules.agents.conversations.pick.title}
            description={t.app.modules.agents.conversations.pick.description}
            action={
              <Button
                size="sm"
                onClick={() => void startConversation()}
                disabled={createConversation.isPending}
              >
                {t.app.modules.agents.home.newConversation}
              </Button>
            }
          />
        )}
      </section>

      {/* Thread drawer — the narrow-window stand-in for the aside above. */}
      {drawerOpen ? (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div
            aria-hidden
            onClick={() => setDrawerOpen(false)}
            className="absolute inset-0 bg-black/40 backdrop-blur-sm"
          />
          <div className="absolute inset-y-0 left-0 flex w-[280px] max-w-[85vw] flex-col border-r border-(--color-border) bg-(--color-card) p-3">
            <div className="mb-2 flex shrink-0 items-center justify-between">
              <p className="min-w-0 truncate font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                {agent.name}
              </p>
              <button
                type="button"
                onClick={() => setDrawerOpen(false)}
                aria-label={t.app.modules.agents.conversations.closeDrawer}
                className="rounded-lg p-1.5 text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)"
              >
                <X className="size-4" />
              </button>
            </div>
            <div className="min-h-0 flex-1">{threadList}</div>
          </div>
        </div>
      ) : null}

      <ConfirmDialog
        open={pendingDelete !== null}
        title={t.app.modules.agents.conversations.confirmDelete.title}
        subject={pendingDelete?.title || t.app.modules.agents.common.untitled}
        description={t.app.modules.agents.conversations.confirmDelete.description}
        busy={deleteConversation.isPending}
        error={deleteError}
        onConfirm={() => void confirmDelete()}
        onCancel={() => {
          setPendingDelete(null);
          setDeleteError(null);
        }}
      />
    </div>
  );
}

function Placeholder({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center">
      <span className="flex size-11 items-center justify-center rounded-xl bg-(--color-muted)">
        <Bot className="size-5 text-(--color-muted-foreground)" />
      </span>
      <p className="text-sm font-medium text-(--color-foreground)">{title}</p>
      {description ? (
        <p className="max-w-sm text-xs leading-relaxed text-(--color-muted-foreground)">
          {description}
        </p>
      ) : null}
      {action}
    </div>
  );
}
