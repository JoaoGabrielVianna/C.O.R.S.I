import { createElement, type SVGProps } from "react";
import { resolveIcon } from "./format";

/**
 * CategoryIcon — renders the lucide glyph for an icon key.
 *
 * Uses `React.createElement` rather than `const Icon = resolveIcon(name); <Icon … />`
 * so we don't construct a component reference inside JSX (which trips the
 * `react-hooks/static-components` lint rule). `resolveIcon` always returns the
 * same component reference for a given `name`.
 */
export function CategoryIcon({ name, ...props }: { name: string } & SVGProps<SVGSVGElement>) {
  return createElement(resolveIcon(name), props);
}
