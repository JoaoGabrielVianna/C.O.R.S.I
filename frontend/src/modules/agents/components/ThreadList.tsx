import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Check, MessageSquarePlus, Pencil, Search, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";
import type { LocaleFormat } from "@/lib/i18n";
import type { Translations } from "@/lib/i18n/pt";
import { cn } from "@/lib/utils";
import type { ApiConversation } from "@/modules/agents/api/conversations";

/**
 * One agent's threads, newest activity first, bucketed by age.
 *
 * The buckets are what make a long list navigable — "ontem" is a far better
 * handle than a timestamp when you are looking for the thread you had
 * yesterday. Search filters across all buckets and collapses the ones that
 * end up empty.
 *
 * ── Rows are links ─────────────────────────────────────────────────────
 * Each thread is an anchor to its own URL, not a button that swaps state.
 * That is what makes a conversation something you can copy, bookmark, open
 * in a new tab and come back to — and what makes the browser's back button
 * mean something inside the module.
 *
 * ── The list knows how long it is ──────────────────────────────────────
 * Search only filters what has been loaded, so a list that quietly stopped
 * at the first page would fail to find an older thread and never say why.
 * `total` is the server's count of the whole collection; when more exist
 * than are loaded, the footer says so and offers the rest.
 */

interface Props {
  conversations: ApiConversation[];
  /** How many exist server-side, loaded or not. */
  total: number;
  /** The thread currently open, if any. */
  activeId: string | null;
  /** Builds the URL of a thread. */
  linkTo: (conversationId: string) => string;
  /** Receives the whole record so the confirmation can name it. */
  onDelete: (conversation: ApiConversation) => void;
  onRename: (id: string, title: string) => void;
  onNew: () => void;
  onNavigate?: () => void;
  canCreate: boolean;
  loading?: boolean;
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
}

/** When a thread was last touched. New threads have no message yet. */
function activityOf(c: ApiConversation): number {
  return new Date(c.last_message_at ?? c.created_at).getTime();
}

type BucketKey = keyof Translations["app"]["modules"]["agents"]["threadList"]["buckets"];

type Bucket = { key: BucketKey; items: ApiConversation[] };

/**
 * Buckets by age against local midnight, not by elapsed hours — something
 * from 23:00 last night belongs in "ontem" at 08:00 today, and an
 * hours-based cutoff would file it under "hoje".
 */
function bucketize(conversations: ApiConversation[], now: Date): Bucket[] {
  const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const day = 86_400_000;

  const buckets: Bucket[] = [
    { key: "today", items: [] },
    { key: "yesterday", items: [] },
    { key: "last7", items: [] },
    { key: "last30", items: [] },
    { key: "older", items: [] },
  ];

  for (const c of conversations) {
    const t = activityOf(c);
    if (t >= midnight) buckets[0].items.push(c);
    else if (t >= midnight - day) buckets[1].items.push(c);
    else if (t >= midnight - 7 * day) buckets[2].items.push(c);
    else if (t >= midnight - 30 * day) buckets[3].items.push(c);
    else buckets[4].items.push(c);
  }
  return buckets.filter((b) => b.items.length > 0);
}

export function ThreadList({
  conversations,
  total,
  activeId,
  linkTo,
  onDelete,
  onRename,
  onNew,
  onNavigate,
  canCreate,
  loading,
  hasMore,
  loadingMore,
  onLoadMore,
}: Props) {
  const t = useT();
  const fmt = useFormat();
  const [query, setQuery] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return conversations;
    return conversations.filter((c) => c.title.toLowerCase().includes(q));
  }, [conversations, query]);

  // `new Date()` at render time is fine here: the buckets only need to be
  // right for the render that is happening, and any state change that
  // could move a thread between buckets re-renders anyway.
  const buckets = useMemo(() => bucketize(filtered, new Date()), [filtered]);

  return (
    <div className="flex h-full flex-col gap-2">
      <Button onClick={onNew} disabled={!canCreate} className="w-full justify-start" size="sm">
        <MessageSquarePlus />
        {t.app.modules.agents.threadList.new}
      </Button>

      {conversations.length > 4 ? (
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-(--color-muted-foreground)" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t.app.modules.agents.threadList.searchPlaceholder}
            className={cn(
              "h-8 w-full rounded-lg border border-(--color-border) bg-(--color-card) pl-8 pr-7 text-xs",
              "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
              "outline-none transition-colors focus:border-(--color-brand-500)",
            )}
          />
          {query ? (
            <button
              type="button"
              onClick={() => setQuery("")}
              aria-label={t.app.modules.agents.threadList.clearSearch}
              className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1 text-(--color-muted-foreground) hover:text-(--color-foreground)"
            >
              <X className="size-3" />
            </button>
          ) : null}
        </div>
      ) : null}

      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto pr-0.5">
        {loading ? (
          <p className="px-2 py-3 text-[11.5px] text-(--color-muted-foreground)">{t.app.modules.agents.common.loading}</p>
        ) : conversations.length === 0 ? (
          <p className="px-2 py-3 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.threadList.empty}
          </p>
        ) : filtered.length === 0 ? (
          <p className="px-2 py-3 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.threadList.noMatch.replace("{query}", query)}
            {hasMore ? t.app.modules.agents.threadList.noMatchLoaded : "."}
          </p>
        ) : (
          buckets.map((bucket) => (
            <div key={bucket.key} className="space-y-0.5">
              <p className="px-2 pb-0.5 font-mono text-[9.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                {t.app.modules.agents.threadList.buckets[bucket.key]}
              </p>
              {bucket.items.map((c) => (
                <ThreadRow
                  key={c.id}
                  conversation={c}
                  href={linkTo(c.id)}
                  active={c.id === activeId}
                  editing={editingId === c.id}
                  onNavigate={onNavigate}
                  onDelete={() => onDelete(c)}
                  onStartEdit={() => setEditingId(c.id)}
                  onCancelEdit={() => setEditingId(null)}
                  onCommitEdit={(title) => {
                    onRename(c.id, title);
                    setEditingId(null);
                  }}
                  fmt={fmt}
                />
              ))}
            </div>
          ))
        )}

        {/* What is not on screen, stated. The alternative is a list that
            simply ends and lets the reader assume that is all there is. */}
        {!loading && conversations.length > 0 && conversations.length < total ? (
          <div className="space-y-1.5 px-2 pt-1">
            <p className="font-mono text-[10px] tabular-nums text-(--color-muted-foreground)">
              {t.app.modules.agents.threadList.countOf
                .replace("{loaded}", fmt.number(conversations.length))
                .replace("{total}", fmt.number(total))}
            </p>
            {hasMore && onLoadMore ? (
              <Button
                size="sm"
                variant="outline"
                className="w-full"
                disabled={loadingMore}
                onClick={onLoadMore}
              >
                {loadingMore ? t.app.modules.agents.common.loading : t.app.modules.agents.threadList.loadMore}
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function ThreadRow({
  conversation,
  href,
  active,
  editing,
  onNavigate,
  onDelete,
  onStartEdit,
  onCancelEdit,
  onCommitEdit,
  fmt,
}: {
  conversation: ApiConversation;
  href: string;
  active: boolean;
  editing: boolean;
  onNavigate?: () => void;
  onDelete: () => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
  onCommitEdit: (title: string) => void;
  fmt: LocaleFormat;
}) {
  const t = useT();
  // The editor is a separate component so its draft starts from the title
  // via useState on mount. Syncing it with an effect instead would mean a
  // setState inside one, and a wasted render every time it opens.
  if (editing) {
    return (
      <TitleEditor
        initialTitle={conversation.title}
        onCancel={onCancelEdit}
        onCommit={onCommitEdit}
      />
    );
  }

  return (
    <div
      className={cn(
        "group flex items-center gap-0.5 rounded-lg px-2 py-1.5",
        "transition-colors duration-200 [transition-timing-function:var(--ease-premium)]",
        active ? "bg-(--color-muted)" : "hover:bg-(--color-muted)/60",
      )}
    >
      <Link
        to={href}
        onClick={onNavigate}
        aria-current={active ? "page" : undefined}
        className="min-w-0 flex-1 text-left"
      >
        <p
          className={cn(
            "truncate text-[12.5px] leading-tight",
            active ? "font-medium text-(--color-foreground)" : "text-(--color-foreground)/85",
          )}
          title={conversation.title || t.app.modules.agents.common.untitled}
        >
          {conversation.title || t.app.modules.agents.common.untitled}
        </p>
        <p className="mt-0.5 truncate text-[10.5px] text-(--color-muted-foreground)">
          {relativeActivity(conversation, t, fmt)}
        </p>
      </Link>

      <div className="flex shrink-0 items-center opacity-0 transition-opacity duration-200 group-hover:opacity-100 focus-within:opacity-100">
        <RowAction label={t.app.modules.agents.threadList.rename} icon={Pencil} onClick={onStartEdit} />
        <RowAction label={t.app.modules.agents.threadList.deleteRow} icon={Trash2} onClick={onDelete} destructive />
      </div>
    </div>
  );
}

/**
 * Last activity, in words. A thread with no turn yet says so.
 *
 * The date at the end used to be pinned to `pt-BR` regardless of the
 * language on screen, so an English reader got "12 de ago." under an
 * English label. It follows the reader now — the instant is unchanged,
 * only how it is written.
 */
function relativeActivity(
  c: ApiConversation,
  t: Translations,
  fmt: LocaleFormat,
): string {
  const words = t.app.modules.agents.threadList.activity;
  if (!c.last_message_at) return words.none;
  const then = new Date(c.last_message_at);
  const minutes = Math.round((Date.now() - then.getTime()) / 60_000);
  if (minutes < 1) return words.now;
  if (minutes < 60) return words.minutes.replace("{n}", fmt.number(minutes));
  const hours = Math.round(minutes / 60);
  if (hours < 24) return words.hours.replace("{n}", fmt.number(hours));
  return fmt.date(then, "short");
}

function TitleEditor({
  initialTitle,
  onCancel,
  onCommit,
}: {
  initialTitle: string;
  onCancel: () => void;
  onCommit: (title: string) => void;
}) {
  const t = useT();
  const [draft, setDraft] = useState(initialTitle);
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus and select are DOM work, not state — the legitimate use of an
  // effect here.
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  return (
    <div className="flex items-center gap-1 rounded-lg bg-(--color-muted) px-1.5 py-1">
      <input
        ref={inputRef}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && draft.trim()) onCommit(draft.trim());
          if (e.key === "Escape") onCancel();
        }}
        onBlur={onCancel}
        className="min-w-0 flex-1 bg-transparent px-1 text-[12.5px] text-(--color-foreground) outline-none"
      />
      <button
        type="button"
        // onMouseDown, not onClick: the input's onBlur fires first and
        // would unmount this button before a click could land.
        onMouseDown={(e) => {
          e.preventDefault();
          if (draft.trim()) onCommit(draft.trim());
        }}
        aria-label={t.app.modules.agents.threadList.saveTitle}
        className="rounded p-1 text-(--color-muted-foreground) hover:text-(--color-foreground)"
      >
        <Check className="size-3" />
      </button>
    </div>
  );
}

function RowAction({
  label,
  icon: Icon,
  onClick,
  destructive,
}: {
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  onClick: () => void;
  destructive?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className={cn(
        "rounded p-1 text-(--color-muted-foreground) transition-colors",
        destructive
          ? "hover:bg-(--color-destructive)/10 hover:text-(--color-destructive)"
          : "hover:text-(--color-foreground)",
      )}
    >
      <Icon className="size-3" />
    </button>
  );
}
