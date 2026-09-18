/**
 * The table shell every Library view shares.
 *
 * ── Why a real `<table>` ───────────────────────────────────────────────
 * Because this is tabular data and the browser already knows how to
 * announce it: a screen reader reads "column 3 of 6, Room" without anyone
 * writing an aria attribute. A grid of divs would need `role="table"`,
 * `role="row"`, `role="cell"` and a maintainer who remembers to keep them
 * in step, which is three ways to be wrong in exchange for nothing.
 *
 * ── Why the row is a link and not an onClick ───────────────────────────
 * A clickable row that is not a link cannot be opened in a new tab,
 * cannot be focused by Tab, and does not tell a screen reader that it goes
 * anywhere. The link lives in the first cell and stretches over the row
 * with a pseudo-element, so the whole row is clickable while the
 * accessibility tree still sees one link with one name.
 */

import type { ReactNode } from "react";
import { Link } from "react-router-dom";

import { cn } from "@/lib/utils";

export function LibraryTable({
  caption,
  headers,
  children,
}: {
  caption: string;
  headers: ReactNode[];
  children: ReactNode;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr>
            {headers.map((header, i) => (
              <th
                key={i}
                scope="col"
                className="px-4 pb-2.5 text-xs font-medium tracking-wide text-(--color-muted-foreground) uppercase"
              >
                {header}
              </th>
            ))}
          </tr>
        </thead>
        {children}
      </table>
    </div>
  );
}

export function LibraryRow({ children }: { children: ReactNode }) {
  return (
    <tr className="group relative border-t border-(--color-border) transition-colors hover:bg-(--color-muted)/60 focus-within:bg-(--color-muted)/60">
      {children}
    </tr>
  );
}

export function Cell({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <td className={cn("px-4 py-3.5 align-top", className)}>{children}</td>;
}

/**
 * The primary cell: the one that carries the row's link.
 *
 * `after:absolute after:inset-0` is what makes the rest of the row
 * clickable without adding a second interactive element. The focus ring is
 * drawn on the row rather than the text so a keyboard reader sees the same
 * target a mouse does.
 */
export function LinkCell({
  to,
  label,
  children,
}: {
  to: string;
  label: string;
  children: ReactNode;
}) {
  return (
    <td className="px-4 py-3.5 align-top">
      <Link
        to={to}
        aria-label={label}
        className={cn(
          "font-medium text-(--color-foreground) outline-none",
          "after:absolute after:inset-0 after:content-['']",
          "group-focus-within:underline hover:underline",
          "focus-visible:after:ring-2 focus-visible:after:ring-(--color-ring)/60 focus-visible:after:rounded-lg",
        )}
      >
        {children}
      </Link>
    </td>
  );
}
