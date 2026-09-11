import { useEffect, useMemo, useState } from "react";
import { Network } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { buildBrainGraph, type BrainNode } from "@/modules/agents/brain";
import { BrainCanvas } from "@/modules/agents/components/memory/BrainCanvas";
import { MemoryInspector } from "@/modules/agents/components/memory/MemoryInspector";
import { useMemoryContext } from "@/modules/agents/components/memory/memoryContext";
import { useT } from "@/lib/i18n";

/**
 * Brain — the same memories, as the graph they already form.
 *
 * ── What it costs ──────────────────────────────────────────────────────
 * One read of a list this page already had, plus arithmetic. No model call,
 * no embedding, no background job, nothing persisted. Opening this tab is
 * as expensive as opening the list, because it is the list.
 *
 * ── What it can and cannot show ────────────────────────────────────────
 * Every edge here is provenance: the agent holds its origins, an origin
 * holds the memories captured in it. That is the only relation the v1
 * memory model asserts, so it is the only one drawn. There are no topic
 * nodes, because there are no tags — see the header of `brain.ts` for why
 * inventing them was the wrong trade.
 */
export function AgentBrainPage() {
  const t = useT();
  const { agent, page, isLoading, error, refetch, actions, busy, onCreate } = useMemoryContext();
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const memories = useMemo(() => page?.items ?? [], [page]);
  const graph = useMemo(
    () => buildBrainGraph({ id: agent.id, name: agent.name }, memories),
    [agent.id, agent.name, memories],
  );

  /**
   * The selected node, resolved against the current graph rather than kept
   * as an object.
   *
   * Deleting the memory that is open in the inspector is the ordinary case,
   * and this is what makes it a non-event: the node is simply no longer
   * found, the panel closes, and nothing is left describing something that
   * is not on screen. Holding the node itself would have needed an effect
   * to clean up after every refetch.
   */
  const selected = useMemo(
    () => graph.nodes.find((n) => n.id === selectedId) ?? null,
    [graph.nodes, selectedId],
  );

  // Escape closes the inspector, which is what a panel over a canvas is
  // expected to do.
  useEffect(() => {
    if (!selectedId) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setSelectedId(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selectedId]);

  if (isLoading) {
    return (
      <div className="min-h-0 flex-1 animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40" />
    );
  }

  if (error) {
    return (
      <Placeholder title={t.app.modules.agents.memory.loadFailed} description={error}>
        <Button size="sm" variant="outline" onClick={refetch}>
          {t.app.modules.agents.memory.retry}
        </Button>
      </Placeholder>
    );
  }

  if (memories.length === 0) {
    return (
      <Placeholder
        title={t.app.modules.agents.memory.brain.emptyTitle}
        description={t.app.modules.agents.memory.brainEmptyBody.replace("{agent}", agent.name)}
      >
        <Button size="sm" onClick={onCreate}>
          {t.app.modules.agents.memory.new}
        </Button>
      </Placeholder>
    );
  }

  const handleSelect = (node: BrainNode | null) => setSelectedId(node?.id ?? null);

  return (
    <div
      className={
        selected
          ? "grid min-h-0 flex-1 grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_300px]"
          : "flex min-h-0 flex-1"
      }
    >
      <div className="flex min-h-0 flex-col gap-2">
        {/* Keyed on the agent: arriving at a different one is a different
            picture and deserves a fresh pan and zoom. Editing a memory is
            not, and must not move the view. */}
        <BrainCanvas
          key={agent.id}
          graph={graph}
          selectedId={selectedId}
          onSelect={handleSelect}
        />
        <Legend />
      </div>

      {selected ? (
        <MemoryInspector
          node={selected}
          agentId={agent.id}
          actions={actions}
          busy={busy}
          onClose={() => setSelectedId(null)}
        />
      ) : null}
    </div>
  );
}

/**
 * What the colours mean.
 *
 * Without it the dimmed nodes read as a rendering artefact rather than as
 * the statement they are: that part of what was saved is not reaching the
 * model.
 */
function Legend() {
  const t = useT();
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 px-1 text-[10.5px] text-(--color-muted-foreground)">
      <Swatch className="bg-(--color-accent)" label={t.app.modules.agents.memory.brain.legendAgent} />
      <Swatch className="border border-(--color-muted-foreground) bg-(--color-card)" label={t.app.modules.agents.memory.brain.legendOrigin} />
      <Swatch className="bg-(--color-brand-500)" label={t.app.modules.agents.memory.brain.legendInContext} />
      <Swatch className="border border-(--color-border) bg-(--color-muted)" label={t.app.modules.agents.memory.brain.legendOutOfContext} />
      <span>{t.app.modules.agents.memory.brain.hint}</span>
    </div>
  );
}

function Swatch({ className, label }: { className: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={`size-2.5 rounded-full ${className}`} />
      {label}
    </span>
  );
}

function Placeholder({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="min-h-0 flex-1">
      <div className="mx-auto max-w-xl rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-10 text-center">
        <span className="mx-auto flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
          <Network className="size-4" />
        </span>
        <h3 className="mt-3 text-sm font-semibold text-(--color-foreground)">{title}</h3>
        <p className="mx-auto mt-2 max-w-md text-[12px] leading-relaxed text-(--color-muted-foreground)">
          {description}
        </p>
        {children ? <div className="mt-4">{children}</div> : null}
      </div>
    </div>
  );
}
