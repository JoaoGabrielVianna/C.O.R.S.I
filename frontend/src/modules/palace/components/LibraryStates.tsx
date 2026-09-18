/**
 * The three states every Library view can be in, drawn once.
 *
 * ── Why the skeleton takes a row count from the caller ─────────────────
 * Because a skeleton is a shape, not a claim. It must not imply how many
 * rows are coming: the caller passes a fixed, constant number that is the
 * same whatever the data turns out to be, so a reader cannot count the
 * bars and conclude anything. The frozen spec says this in the negative
 * ("sem tiles falsos"), and this is the same rule for a list.
 *
 * ── Why empty has two shapes ───────────────────────────────────────────
 * "You have nothing here" and "nothing matched what you typed" are
 * different facts and they need different answers: the first is about the
 * Palace, the second is about the query. Collapsing them produces the
 * screen that tells somebody their knowledge base is empty because they
 * mistyped a word.
 *
 * ── What none of these may do ──────────────────────────────────────────
 * Invent data. No demo row, no sample room, no placeholder standing in for
 * something withheld. For this surface, content the backend did not return
 * does not exist, and an empty state that hinted otherwise would be the
 * side channel the whole design is built to close.
 */

import { AlertTriangle } from "lucide-react";

import { Button } from "@/components/ui/Button";
import { useT } from "@/lib/i18n";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";

/** A fixed number of bars. Never derived from anything. */
const SKELETON_ROWS = 5;

/**
 * "These rows are the previous answer, and a new one is on its way."
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   SHOWING OLD ROWS IS ONLY HONEST IF THE SURFACE SAYS THEY ARE OLD
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── Why the table is not marked busy ───────────────────────────────────
 * `aria-busy` on the table would tell a screen reader that the content it
 * is reading is unavailable, and it is not: those rows are real, they were
 * returned by the backend, and they are the best answer anybody has until
 * the next one arrives. What changed is the QUESTION, so the status lives
 * beside the table rather than on top of it, and the table keeps its rows,
 * its focus and its reading position.
 *
 * ── Why the region is always rendered ──────────────────────────────────
 * A live region has to exist before its content changes, or the change is
 * not announced. So the element is permanent and only its text moves
 * between the label and nothing, which is also what keeps it from
 * announcing on every render: identical text is not a change.
 *
 * `polite` because this interrupts nothing. Somebody who just typed a
 * letter does not need their reader to stop mid-sentence to hear that a
 * request is in flight.
 */
export function LibraryRefreshing({ refreshing }: { refreshing: boolean }) {
  const t = useT();
  return (
    <p
      role="status"
      aria-live="polite"
      data-refreshing={refreshing ? "true" : undefined}
      className={cn(
        "px-4 pb-2 text-xs text-(--color-muted-foreground) transition-opacity",
        refreshing ? "opacity-100" : "opacity-0",
      )}
    >
      {refreshing ? t.app.library.states.updating : ""}
    </p>
  );
}

export function LibrarySkeleton({ columns }: { columns: number }) {
  const t = useT();
  return (
    <tbody aria-busy="true" aria-label={t.app.library.states.loading}>
      {Array.from({ length: SKELETON_ROWS }, (_, row) => (
        <tr key={row} className="border-t border-(--color-border)">
          {Array.from({ length: columns }, (_, col) => (
            <td key={col} className="px-4 py-3.5">
              <div
                aria-hidden="true"
                className={cn(
                  "h-4 animate-pulse rounded bg-(--color-muted)",
                  col === 0 ? "w-3/4" : "w-1/2",
                )}
              />
            </td>
          ))}
        </tr>
      ))}
    </tbody>
  );
}

export function LibraryEmpty({
  columns,
  title,
  body,
}: {
  columns: number;
  title: string;
  body: string;
}) {
  return (
    <tbody>
      <tr className="border-t border-(--color-border)">
        <td colSpan={columns} className="px-4 py-14 text-center">
          <p className="text-sm font-medium text-(--color-foreground)">{title}</p>
          <p className="mx-auto mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
            {body}
          </p>
        </td>
      </tr>
    </tbody>
  );
}

/**
 * The error row.
 *
 * ── Why the ApiError message is shown as it arrives ────────────────────
 * Because the backend's refusals name a field, a limit or a closed
 * vocabulary and never quote stored content — that is a rule the domain
 * enforces and a test proves. Rewriting them here would replace something
 * specific and actionable with "something went wrong".
 *
 * A 404 says only that: not found. It is the same answer a fabricated id
 * gets, and this screen must not add a word suggesting anything else was
 * involved. There is no "you do not have access" copy in this codebase
 * because there is no such state to describe.
 */
export function LibraryError({
  columns,
  error,
  onRetry,
}: {
  columns: number;
  error: Error;
  onRetry: () => void;
}) {
  const t = useT();
  const detail = error instanceof ApiError ? error.message : t.app.library.states.errorFallback;

  return (
    <tbody>
      <tr className="border-t border-(--color-border)">
        <td colSpan={columns} className="px-4 py-14">
          <div className="mx-auto flex max-w-prose flex-col items-center gap-3 text-center">
            <AlertTriangle aria-hidden="true" className="size-5 text-(--color-destructive)" />
            <p className="text-sm font-medium text-(--color-foreground)">
              {t.app.library.states.errorTitle}
            </p>
            <p className="text-sm text-(--color-muted-foreground)">{detail}</p>
            <Button variant="outline" size="sm" onClick={onRetry}>
              {t.app.library.states.retry}
            </Button>
          </div>
        </td>
      </tr>
    </tbody>
  );
}
