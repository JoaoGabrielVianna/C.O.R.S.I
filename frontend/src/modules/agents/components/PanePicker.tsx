/*
 * FROZEN — part of Comparison Mode, which is out of scope for Agents v1.0.0
 * (owner decision D8).
 *
 * Not deleted: D8 says the existing code is preserved rather than removed,
 * and removal would need its own decision. Nothing imports it, and the new
 * information architecture has no multi-pane surface for it to attach to.
 */
import { Bot, MessageSquarePlus, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import type { ApiAgent } from "@/modules/agents/api/agents";
import type { ApiConversation } from "@/modules/agents/api/conversations";

/**
 * What a freshly opened column shows before it has a thread.
 *
 * Deliberately not a dropdown: the choice is the entire content of the
 * column at this point, so it gets the whole space. Threads already open in
 * another column are marked rather than hidden — reading the same
 * conversation in two columns is odd but not wrong, and silently removing
 * options is more confusing than labelling them.
 */

interface Props {
  conversations: ApiConversation[];
  agentsById: Map<string, ApiAgent>;
  /** Threads already visible in another column. */
  openConversationIds: Set<string>;
  onPick: (conversationId: string) => void;
  onNew: () => void;
  onClose?: () => void;
}

export function PanePicker({
  conversations,
  agentsById,
  openConversationIds,
  onPick,
  onNew,
  onClose,
}: Props) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-2 border-b border-(--color-border) px-1 pb-2.5">
        <p className="flex-1 text-[13px] font-medium text-(--color-foreground)">Nova coluna</p>
        {onClose ? (
          <button
            type="button"
            onClick={onClose}
            aria-label="Fechar esta coluna"
            title="Fechar esta coluna"
            className="rounded-lg p-1.5 text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
          >
            <X className="size-4" />
          </button>
        ) : null}
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto py-3">
        <Button size="sm" onClick={onNew} className="mb-3 w-full justify-start">
          <MessageSquarePlus />
          Começar uma conversa
        </Button>

        {conversations.length === 0 ? (
          <p className="px-1 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            Nenhuma conversa ainda. Comece uma acima.
          </p>
        ) : (
          <>
            <p className="mb-1 px-1 font-mono text-[9.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
              ou abra uma existente
            </p>
            <div className="space-y-0.5">
              {conversations.map((c) => {
                const agent = agentsById.get(c.agent_id);
                const alreadyOpen = openConversationIds.has(c.id);
                return (
                  <button
                    key={c.id}
                    type="button"
                    onClick={() => onPick(c.id)}
                    className={cn(
                      "flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left",
                      "transition-colors hover:bg-(--color-muted)",
                    )}
                  >
                    <div className="flex size-6 shrink-0 items-center justify-center rounded-md bg-(--color-muted) text-(--color-muted-foreground)">
                      <Bot className="size-3" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-[12.5px] text-(--color-foreground)">
                        {c.title || "Sem título"}
                      </p>
                      <p className="truncate text-[10.5px] text-(--color-muted-foreground)">
                        {agent?.name ?? "agente removido"}
                      </p>
                    </div>
                    {alreadyOpen ? (
                      <span className="shrink-0 font-mono text-[9.5px] uppercase tracking-wider text-(--color-muted-foreground)">
                        aberta
                      </span>
                    ) : null}
                  </button>
                );
              })}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
