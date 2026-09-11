import { Link } from "react-router-dom";
import { MessagesSquare, PenLine, Unlink } from "lucide-react";
import { cn } from "@/lib/utils";
import { provenanceOf, type ApiMemory } from "@/modules/agents/api/memories";
import { useT } from "@/lib/i18n";

/**
 * Where a memory came from, in one line.
 *
 * This is the answer to "why does the agent know this?", and it is the whole
 * reason provenance is stored at all. Three states, all normal:
 *
 *   manual   the user typed it, and there is nothing to link to
 *   thread   it came from a conversation that is still there — a link
 *   lost     it came from a conversation that is not — said plainly
 *
 * The third is not an error and must never read as one. A memory outliving
 * its thread is the rule the schema was built around: deleting a
 * conversation is not supposed to quietly delete what was learnt in it.
 */
export function MemoryProvenance({
  memory,
  agentId,
  className,
}: {
  memory: ApiMemory;
  agentId: string;
  className?: string;
}) {
  const t = useT();
  const p = provenanceOf(memory);
  const base = cn(
    "inline-flex min-w-0 items-center gap-1.5 text-[11px] text-(--color-muted-foreground)",
    className,
  );

  if (p.kind === "manual") {
    return (
      <span className={base}>
        <PenLine className="size-3 shrink-0" />
        {t.app.modules.agents.memory.provenance.manual}
      </span>
    );
  }

  if (p.kind === "lost") {
    return (
      <span className={base} title={t.app.modules.agents.memory.provenance.originGoneTitle}>
        <Unlink className="size-3 shrink-0" />
        {t.app.modules.agents.memory.provenance.originGone}
      </span>
    );
  }

  return (
    <Link
      to={`/app/modules/agents/${agentId}/c/${p.conversationId}`}
      className={cn(base, "rounded-md transition-colors hover:text-(--color-foreground)")}
    >
      <MessagesSquare className="size-3 shrink-0" />
      <span className="truncate">
        {t.app.modules.agents.memory.savedFrom.replace("{title}", p.title)}
      </span>
    </Link>
  );
}
