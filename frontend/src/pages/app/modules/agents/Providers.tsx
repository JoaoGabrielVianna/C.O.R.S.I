import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { PageHeader } from "@/components/workspace";
import { useT } from "@/lib/i18n";
import { ModelCostsPanel } from "@/modules/agents/components/ModelCostsPanel";
import { ProvidersPanel } from "@/modules/agents/components/ProvidersPanel";

/**
 * Provedores: the endpoints and credentials the agents speak through, and
 * what each key has actually been billed.
 *
 * ── Why it moved out of the agents screen ──────────────────────────────
 * A provider is not an agent's setting; it is shared infrastructure that
 * several agents point at. Sitting it above the agent table said otherwise,
 * and put a credential form, an agent registry and a cost chart on one
 * screen. Now it is a destination of its own, reached from the Home.
 *
 * ── The label ──────────────────────────────────────────────────────────
 * It used to be called "Tokens". On the same screen, "tokens" also meant
 * the unit models are billed in and the ceiling on a reply. Three meanings
 * of one word was a vocabulary collision, not a naming preference.
 *
 * ── The debt, stated ───────────────────────────────────────────────────
 * Conceptually a provider belongs to Integrations, not to Agents. It is not
 * being moved: the target architecture says to record the violation and
 * leave it until a second backend integration exists, and moving it now
 * would cross a module boundary and deliver nothing. Registered as
 * known debt.
 */
export function ProvidersPage() {
  const t = useT();
  return (
    <div className="mx-auto w-full max-w-[1200px] space-y-5">
      <Link
        to="/app/modules/agents"
        className="inline-flex items-center gap-1 font-mono text-[10.5px] uppercase tracking-[0.18em] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
      >
        <ArrowLeft className="size-3" />
        {t.app.modules.agents.home.title}
      </Link>

      <PageHeader
        className="shrink-0"
        title={t.app.modules.agents.providers.title}
        description={t.app.modules.agents.providers.description}
      />

      <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(300px,340px)]">
        {/* The summary leads on a stacked layout and sticks on a wide one:
            narrow windows glance at it, wide ones scan the table beside it. */}
        <div className="min-w-0 xl:sticky xl:top-18 xl:order-2">
          <ModelCostsPanel />
        </div>
        <div className="min-w-0 xl:order-1">
          <ProvidersPanel />
        </div>
      </div>
    </div>
  );
}
