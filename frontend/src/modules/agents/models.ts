/**
 * Model identity — which family a model id belongs to, and how it reads.
 *
 * The gateway hands us free-form model ids (`claude-opus-5`,
 * `vertex_ai/gemini-2.5-pro`, `openrouter/openai/gpt-5`), so the family is
 * matched by pattern over a normalized id, not by an enum we would have to
 * keep in sync with LiteLLM. An unknown id is a normal outcome, not an
 * error: it falls back to a neutral badge and still shows its full name.
 *
 * The glyphs themselves live in `components/ModelGlyphs`.
 */

import { createElement } from "react";
import {
  Cross,
  Grid,
  Knot,
  Loops,
  Monogram,
  Spark,
  Sunburst,
  type GlyphProps,
} from "@/modules/agents/components/ModelGlyphs";

export type ModelBrand = {
  key: string;
  /** Human name of the family, for tooltips. */
  label: string;
  /** Brand hue — picked mid-range so it holds up on both themes. */
  color: string;
  Glyph: React.ComponentType<GlyphProps>;
};

/** Binds a letter to the monogram glyph. Built once, at module load. */
const mono = (letter: string) => {
  const Glyph = (props: GlyphProps) => createElement(Monogram, { ...props, letter });
  Glyph.displayName = `Monogram(${letter})`;
  return Glyph;
};

const BRANDS: Array<{ test: RegExp; brand: ModelBrand }> = [
  { test: /claude|anthropic/, brand: { key: "anthropic", label: "Anthropic", color: "#D97757", Glyph: Sunburst } },
  { test: /gpt|openai|^o[134]\b|davinci/, brand: { key: "openai", label: "OpenAI", color: "#10A37F", Glyph: Knot } },
  { test: /gemini|palm|gemma|google/, brand: { key: "google", label: "Google", color: "#4285F4", Glyph: Spark } },
  { test: /llama|meta/, brand: { key: "meta", label: "Meta", color: "#0866FF", Glyph: Loops } },
  { test: /mistral|mixtral|magistral|codestral|devstral|ministral/, brand: { key: "mistral", label: "Mistral", color: "#FF7000", Glyph: Grid } },
  { test: /grok|xai|x-ai/, brand: { key: "xai", label: "xAI", color: "#8B8D98", Glyph: Cross } },
  { test: /deepseek/, brand: { key: "deepseek", label: "DeepSeek", color: "#4D6BFE", Glyph: mono("D") } },
  { test: /qwen|qwq/, brand: { key: "qwen", label: "Qwen", color: "#7C6BF0", Glyph: mono("Q") } },
  { test: /kimi|moonshot/, brand: { key: "moonshot", label: "Moonshot", color: "#12B5A6", Glyph: mono("K") } },
  { test: /command|cohere/, brand: { key: "cohere", label: "Cohere", color: "#4E9E86", Glyph: mono("C") } },
  { test: /nova|titan|amazon|bedrock/, brand: { key: "amazon", label: "Amazon", color: "#E28B3C", Glyph: mono("A") } },
];

const UNKNOWN: ModelBrand = {
  key: "unknown",
  label: "Modelo",
  color: "#8B8D98",
  Glyph: mono("?"),
};

/**
 * Resolves a model id to its family. Gateway prefixes (`vertex_ai/`,
 * `bedrock/`, `openrouter/openai/`) are part of the string we match against,
 * which is why the patterns look for the family name anywhere in it.
 */
export function modelBrand(model: string | null | undefined): ModelBrand {
  if (!model) return UNKNOWN;
  const id = model.toLowerCase();
  return BRANDS.find((b) => b.test.test(id))?.brand ?? UNKNOWN;
}

/**
 * Strips the gateway prefix for display: `vertex_ai/gemini-2.5-pro` reads as
 * `gemini-2.5-pro`. The full id stays available as the element's title.
 */
export function shortModelName(model: string): string {
  const parts = model.split("/");
  return parts[parts.length - 1] || model;
}
