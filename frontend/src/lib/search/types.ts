import type { ComponentType, SVGProps } from "react";

/**
 * SearchItem — generic shape for the future global search index.
 *
 * Today the workspace only registers `Command` items (in `lib/command`), but
 * every Command extends this interface so the palette renderer is uniform.
 * When modules ship, they will register additional `SearchItem`s of category
 * "job", "expense", "note", etc. — they appear in the palette automatically.
 *
 * Architecture only — no backend, no indexer, no fuzzy lib. The palette does
 * a normalized substring match across `title` + `keywords` for v1.
 */
export type SearchCategory =
  | "Navigation"
  | "Modules"
  | "Settings"
  | "Actions"
  | "Account"
  /* future categories — registered by modules */
  | "Jobs"
  | "Companies"
  | "Expenses"
  | "Notes";

export type SearchItem = {
  id: string;
  title: string;
  category: SearchCategory;
  /** Optional subtitle rendered beneath the title in the result row. */
  subtitle?: string;
  icon?: ComponentType<SVGProps<SVGSVGElement>>;
  /** Free-text tokens that participate in matching. */
  keywords?: readonly string[];
  /** Higher = ranked first when scores are tied. Default 0. */
  weight?: number;
  /** Optional human-readable shortcut hint (e.g. "⌘D"). */
  shortcut?: string;
};
