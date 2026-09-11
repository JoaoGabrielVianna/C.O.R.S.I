import { modelBrand } from "@/modules/agents/models";

/**
 * The model's family glyph, tinted with the family's hue.
 *
 * Decorative by design: every caller renders the model id as text beside it,
 * so identity never rests on the icon or its color alone. The brand table
 * and the matching rules live in `@/modules/agents/models`.
 */
export function ModelIcon({
  model,
  className = "size-4",
}: {
  model: string | null | undefined;
  className?: string;
}) {
  const brand = modelBrand(model);
  return (
    <span
      title={brand.label}
      style={{ color: brand.color }}
      className={`inline-flex shrink-0 ${className}`}
    >
      <brand.Glyph className="size-full" />
    </span>
  );
}
