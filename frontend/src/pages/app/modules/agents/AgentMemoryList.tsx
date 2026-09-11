import { useMemo, useState } from "react";
import { Brain, Pencil, Pin, PinOff, Power, Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import type { ApiMemory } from "@/modules/agents/api/memories";
import { MemoryProvenance } from "@/modules/agents/components/memory/MemoryProvenance";
import { memoryDate } from "@/modules/agents/components/memory/memoryFormat";
import type { MemoryActions } from "@/modules/agents/components/memory/memoryActions";
import { useMemoryContext } from "@/modules/agents/components/memory/memoryContext";
import { useFormat } from "@/lib/i18n";
import { useT } from "@/lib/i18n";

/**
 * Memories — the operational view.
 *
 * ── The counter is the point of this screen ────────────────────────────
 * "3 de 8 no contexto" is the only place anyone finds out that some of what
 * they saved is not reaching the model. A list without it looks identical
 * whether the budget is cutting or not, which makes it a list of things the
 * user *believes* the agent knows.
 *
 * The number is computed server-side by the same function that composes the
 * turn, so it cannot drift into a comforting fiction.
 *
 * ── Why the search is local ────────────────────────────────────────────
 * The whole collection is already here — the endpoint returns it in one
 * read, capped and counted — so filtering is a substring match over an array
 * in memory. A search endpoint would add a round trip, a loading state and a
 * second definition of what matching means, to answer a question the client
 * can already answer instantly.
 */
export function AgentMemoryListPage() {
  const t = useT();
  const fmt = useFormat();
  const { agent, page, isLoading, error, refetch, actions, busy, onCreate } = useMemoryContext();
  const [term, setTerm] = useState("");

  const items = useMemo(() => page?.items ?? [], [page]);
  const filtered = useMemo(() => {
    const q = term.trim().toLowerCase();
    if (!q) return items;
    return items.filter((m) => m.content.toLowerCase().includes(q));
  }, [items, term]);

  const inContext = items.filter((m) => m.in_context).length;

  if (isLoading) {
    return (
      <div className="min-h-0 flex-1 space-y-2">
        {[0, 1, 2].map((i) => (
          <div
            key={i}
            className="h-[68px] animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40"
          />
        ))}
      </div>
    );
  }

  if (error) {
    return (
      <Empty
        title={t.app.modules.agents.memory.loadFailed}
        description={error}
        action={
          <Button size="sm" variant="outline" onClick={refetch}>
            {t.app.modules.agents.memory.retry}
          </Button>
        }
      />
    );
  }

  if (items.length === 0) {
    return (
      <Empty
        title={`${agent.name} ainda não lembra de nada`}
        description={t.app.modules.agents.memory.list.empty}
        action={
          <Button size="sm" onClick={onCreate}>
            {t.app.modules.agents.memory.new}
          </Button>
        }
      />
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2.5">
      <div className="flex shrink-0 flex-wrap items-center gap-2">
        <label className="relative flex min-w-[200px] flex-1 items-center">
          <Search className="pointer-events-none absolute left-2.5 size-3.5 text-(--color-muted-foreground)" />
          <input
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            placeholder={t.app.modules.agents.memory.list.searchPlaceholder}
            aria-label={t.app.modules.agents.memory.list.searchLabel}
            className={cn(
              "h-8 w-full rounded-xl border border-(--color-border) bg-(--color-card) pl-8 pr-3",
              "text-[12.5px] text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
              "outline-none focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
            )}
          />
        </label>
        <p className="shrink-0 font-mono text-[10.5px] text-(--color-muted-foreground)">
          {t.app.modules.agents.memory.contextCount
            .replace("{inContext}", fmt.number(inContext))
            .replace("{total}", fmt.number(items.length))}{" "}
          {fmt.number(page?.used_characters ?? 0)}/
          {fmt.number(page?.budget_characters ?? 0)} car.
        </p>
      </div>

      {/* A capped read is never a silent one. */}
      {page && page.total > page.items.length ? (
        <p className="shrink-0 rounded-xl border border-dashed border-(--color-border) px-3 py-2 text-[11px] text-(--color-muted-foreground)">
          {t.app.modules.agents.interp.memoryTruncated
            .replace("{count}", fmt.number(page.items.length))
            .replace("{total}", fmt.number(page.total))}{" "}
          {t.app.modules.agents.interp.truncatedTail}
        </p>
      ) : null}

      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto pr-0.5">
        {filtered.length === 0 ? (
          <p className="rounded-2xl border border-dashed border-(--color-border) px-4 py-6 text-center text-[12px] text-(--color-muted-foreground)">
            {t.app.modules.agents.interp.memoryNoMatch.replace("{term}", term)}
          </p>
        ) : (
          filtered.map((m) => (
            <MemoryRow key={m.id} memory={m} agentId={agent.id} actions={actions} busy={busy} />
          ))
        )}
      </div>
    </div>
  );
}

function MemoryRow({
  memory,
  agentId,
  actions,
  busy,
}: {
  memory: ApiMemory;
  agentId: string;
  actions: MemoryActions;
  busy: boolean;
}) {
  const t = useT();
  return (
    <article
      className={cn(
        "group rounded-2xl border border-(--color-border) bg-(--color-card) px-3 py-2.5",
        "transition-colors duration-[250ms] [transition-timing-function:var(--ease-premium)]",
        !memory.enabled && "opacity-60",
      )}
    >
      <div className="flex items-start gap-2">
        {memory.pinned ? (
          <Pin className="mt-0.5 size-3.5 shrink-0 text-(--color-brand-600)" />
        ) : null}
        <p className="min-w-0 flex-1 whitespace-pre-wrap break-words text-[13px] leading-relaxed text-(--color-foreground)">
          {memory.content}
        </p>

        <div className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity duration-200 focus-within:opacity-100 group-hover:opacity-100">
          <RowAction
            label={memory.pinned ? "Desafixar" : "Fixar"}
            icon={memory.pinned ? PinOff : Pin}
            disabled={busy}
            onClick={() => actions.togglePinned(memory)}
          />
          <RowAction
            label={memory.enabled ? "Desligar" : "Ligar"}
            icon={Power}
            disabled={busy}
            onClick={() => actions.toggleEnabled(memory)}
          />
          <RowAction
            label={t.app.modules.agents.memory.list.edit}
            icon={Pencil}
            disabled={busy}
            onClick={() => actions.startEdit(memory)}
          />
          <RowAction
            label={t.app.modules.agents.memory.list.delete}
            icon={Trash2}
            disabled={busy}
            destructive
            onClick={() => actions.startDelete(memory)}
          />
        </div>
      </div>

      <div className="mt-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <MemoryProvenance memory={memory} agentId={agentId} />
        <span className="text-[11px] text-(--color-muted-foreground)">
          · {memoryDate(memory.created_at)}
        </span>
        <StateChip memory={memory} />
      </div>
    </article>
  );
}

/**
 * The state of one memory, said exactly.
 *
 * Three distinct facts, and collapsing any two of them would be a lie the
 * user cannot detect: off is a choice they made, out-of-budget is one the
 * system made, and in-context is neither.
 */
function StateChip({ memory }: { memory: ApiMemory }) {
  const t = useT();
  if (!memory.enabled) {
    return <Chip tone="muted">{t.app.modules.agents.memory.list.off}</Chip>;
  }
  if (!memory.in_context) {
    return <Chip tone="warn">{t.app.modules.agents.memory.list.overBudget}</Chip>;
  }
  return <Chip tone="on">{t.app.modules.agents.memory.list.inContext}</Chip>;
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
  disabled,
  destructive,
}: {
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  onClick: () => void;
  disabled?: boolean;
  destructive?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className={cn(
        "rounded-lg p-1.5 transition-colors disabled:opacity-40",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
        destructive
          ? "text-(--color-muted-foreground) hover:bg-(--color-destructive)/10 hover:text-(--color-destructive)"
          : "text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
      )}
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
          <Brain className="size-4" />
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
