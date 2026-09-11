import { cn } from "@/lib/utils";

/**
 * Table primitives for the Agents settings screen.
 *
 * Tokens and agents are records with the same shape of question — name,
 * binding, knobs, cost — so they read best as tables, aligned column to
 * column, rather than as stacks of cards that repeat their own labels.
 *
 * Wide content scrolls inside `TableScroll`, never on the page.
 */

export function TableScroll({ children }: { children: React.ReactNode }) {
  return <div className="w-full overflow-x-auto">{children}</div>;
}

export function Th({
  children,
  className,
  numeric,
}: {
  children?: React.ReactNode;
  className?: string;
  numeric?: boolean;
}) {
  return (
    <th
      scope="col"
      className={cn(
        "px-3 py-2 font-mono text-[10px] font-medium uppercase tracking-[0.12em]",
        "text-(--color-muted-foreground)",
        numeric ? "text-right" : "text-left",
        className,
      )}
    >
      {children}
    </th>
  );
}

export function Td({
  children,
  className,
  numeric,
  colSpan,
}: {
  children?: React.ReactNode;
  className?: string;
  numeric?: boolean;
  colSpan?: number;
}) {
  return (
    <td
      colSpan={colSpan}
      className={cn("px-3 py-2.5 align-middle", numeric ? "text-right" : "text-left", className)}
    >
      {children}
    </td>
  );
}

/** A row that carries an expanded editor or result under its record. */
export function ExpandedRow({
  colSpan,
  children,
}: {
  colSpan: number;
  children: React.ReactNode;
}) {
  return (
    <tr className="bg-(--color-muted)/40">
      <td colSpan={colSpan} className="px-3 pb-3 pt-0">
        {children}
      </td>
    </tr>
  );
}
