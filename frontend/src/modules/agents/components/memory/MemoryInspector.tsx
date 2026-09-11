import { Link } from "react-router-dom";
import {
  Bot,
  Brain,
  MessagesSquare,
  Pencil,
  Pin,
  PinOff,
  Power,
  Trash2,
  Unlink,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import type { BrainNode } from "@/modules/agents/brain";
import { MemoryProvenance } from "./MemoryProvenance";
import { memoryDate } from "./memoryFormat";
import type { MemoryActions } from "./memoryActions";
import { useT } from "@/lib/i18n";

/**
 * The panel beside the Brain: what the selected node is, and what can be
 * done to it without leaving the graph.
 *
 * ── Why a panel and not a modal ────────────────────────────────────────
 * Reading a memory is a step in exploring the graph, not a detour from it.
 * A modal would black out the picture that provides the context for what is
 * being read, and every close would cost the reader their place.
 *
 * ── Why every node kind gets a body ────────────────────────────────────
 * A node that can be clicked and answers nothing teaches the reader to stop
 * clicking. The agent and the origin nodes have less to say than a memory,
 * so they say less — but they say something true.
 *
 * ── No dead buttons ────────────────────────────────────────────────────
 * Every action here is wired to a capability that exists. The link to the
 * source conversation appears only when there is a conversation to open;
 * when there is not, the panel says so in words instead of offering a
 * button that would fail.
 */
export function MemoryInspector({
  node,
  agentId,
  actions,
  busy,
  onClose,
}: {
  node: BrainNode;
  agentId: string;
  actions: MemoryActions;
  busy: boolean;
  onClose: () => void;
}) {
  const t = useT();
  return (
    <aside className="flex min-h-0 flex-col rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)">
      <header className="flex shrink-0 items-start gap-2 border-b border-(--color-border) px-3 py-2.5">
        <span
          className={cn(
            "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg",
            node.kind === "memory"
              ? "bg-(--color-brand-50) text-(--color-brand-700)"
              : "bg-(--color-muted) text-(--color-muted-foreground)",
          )}
        >
          {node.kind === "agent" ? (
            <Bot className="size-3.5" />
          ) : node.kind === "origin" ? (
            <MessagesSquare className="size-3.5" />
          ) : (
            <Brain className="size-3.5" />
          )}
        </span>
        <p className="min-w-0 flex-1 pt-1 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {node.kind === "agent" ? "Agente" : node.kind === "origin" ? "Origem" : "Memória"}
        </p>
        <button
          type="button"
          onClick={onClose}
          aria-label={t.app.modules.agents.memory.inspector.close}
          className="rounded-lg p-1 text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
        >
          <X className="size-3.5" />
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {node.kind === "agent" ? (
          <>
            <p className="text-[13px] font-medium text-(--color-foreground)">{node.label}</p>
            <p className="mt-1.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {node.memories === 0
                ? "Este agente ainda não lembra de nada."
                : `${node.memories} ${node.memories === 1 ? "memória" : "memórias"}, agrupadas pela origem de cada uma.`}
            </p>
          </>
        ) : node.kind === "origin" ? (
          <>
            <p className="text-[13px] font-medium text-(--color-foreground)">{node.label}</p>
            <p className="mt-1.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {node.memories} {node.memories === 1 ? "memória veio" : "memórias vieram"} daqui.
            </p>
            {node.origin === "thread" && node.conversationId ? (
              <Button size="sm" variant="outline" className="mt-3 w-full" asChild>
                <Link to={`/app/modules/agents/${agentId}/c/${node.conversationId}`}>
                  <MessagesSquare />
                  {t.app.modules.agents.memory.inspector.openConversation}
                </Link>
              </Button>
            ) : node.origin === "lost" ? (
              <p className="mt-3 flex items-start gap-1.5 rounded-xl border border-dashed border-(--color-border) px-3 py-2 text-[11px] leading-relaxed text-(--color-muted-foreground)">
                <Unlink className="mt-0.5 size-3 shrink-0" />
                {t.app.modules.agents.memory.inspector.originDeleted}
              </p>
            ) : null}
          </>
        ) : (
          <>
            <p className="whitespace-pre-wrap break-words text-[13px] leading-relaxed text-(--color-foreground)">
              {node.memory.content}
            </p>

            <div className="mt-3 space-y-1.5 border-t border-(--color-border) pt-3">
              <MemoryProvenance memory={node.memory} agentId={agentId} className="w-full" />
              <p className="text-[11px] text-(--color-muted-foreground)">
                Criada em {memoryDate(node.memory.created_at)}
                {node.memory.updated_at !== node.memory.created_at
                  ? ` · editada em ${memoryDate(node.memory.updated_at)}`
                  : ""}
              </p>
              {/* The state, said in full. On the canvas a dimmed node means
                  only "not being sent"; this is where the two reasons for
                  that are told apart. */}
              <p className="text-[11px] text-(--color-muted-foreground)">
                {!node.memory.enabled
                  ? "Desligada: não entra no contexto até ser religada."
                  : node.memory.in_context
                    ? "Está no contexto do próximo turno."
                    : "Ligada, mas fora do contexto: o orçamento de caracteres não alcançou esta."}
              </p>
            </div>

            <div className="mt-3 grid grid-cols-2 gap-1.5">
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => actions.startEdit(node.memory)}
              >
                <Pencil />
                {t.app.modules.agents.memory.inspector.edit}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => actions.toggleEnabled(node.memory)}
              >
                <Power />
                {node.memory.enabled ? "Desligar" : "Ligar"}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => actions.togglePinned(node.memory)}
              >
                {node.memory.pinned ? <PinOff /> : <Pin />}
                {node.memory.pinned ? "Desafixar" : "Fixar"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => actions.startDelete(node.memory)}
                className="text-(--color-destructive) hover:bg-(--color-destructive)/10"
              >
                <Trash2 />
                {t.app.modules.agents.memory.inspector.delete}
              </Button>
            </div>
          </>
        )}
      </div>
    </aside>
  );
}
