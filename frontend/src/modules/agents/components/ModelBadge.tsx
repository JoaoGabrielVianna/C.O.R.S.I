import { cn } from "@/lib/utils";
import { ModelIcon } from "@/modules/agents/components/ModelIcon";
import { modelBrand, shortModelName } from "@/modules/agents/models";

/**
 * How a model reads wherever it appears.
 *
 * ── Provider, Model, Agent are three things ────────────────────────────
 *
 *     Provider  →  Model  →  Agent
 *
 * The provider is the endpoint the call is billed through. The model is
 * what answers. The agent is a configuration that uses one of them. An
 * agent is never "a Claude": it is an agent currently pointed at a model
 * whose family happens to be Anthropic, and pointing it somewhere else
 * tomorrow does not make it a different agent.
 *
 * So the family glyph is decoration and the model id is the identity: the
 * text is always rendered beside the icon, never replaced by it. Family
 * matching lives in `models.ts` and is pattern-based over free-form gateway
 * ids, which is why an unrecognised model is an ordinary outcome — it falls
 * back to a neutral badge and still shows its full name. Nothing here
 * assumes a fixed set of vendors.
 */
export function ModelBadge({
  model,
  provider,
  className,
  size = "sm",
}: {
  model: string | null | undefined;
  /** The provider's name, when the surface has room to say where it runs. */
  provider?: string | null;
  className?: string;
  size?: "sm" | "md";
}) {
  const brand = modelBrand(model);
  const known = Boolean(model);
  const text = model ? shortModelName(model) : "modelo não definido";

  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-lg border border-(--color-border)",
        "bg-(--color-card) px-2 py-0.5",
        size === "sm" ? "text-[10.5px]" : "text-[11.5px]",
        className,
      )}
      title={
        [model ?? "modelo não definido", brand.label, provider ? `via ${provider}` : null]
          .filter(Boolean)
          .join(" · ")
      }
    >
      <ModelIcon model={model} className={size === "sm" ? "size-3.5" : "size-4"} />
      <span
        className={cn(
          "truncate font-mono",
          known ? "text-(--color-foreground)/80" : "text-(--color-muted-foreground) italic",
        )}
      >
        {text}
      </span>
      {provider ? (
        <>
          <span aria-hidden className="text-(--color-muted-foreground)">
            ·
          </span>
          <span className="truncate text-(--color-muted-foreground)">{provider}</span>
        </>
      ) : null}
    </span>
  );
}
