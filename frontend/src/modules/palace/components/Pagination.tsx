/**
 * Offset pagination over a Palace listing.
 *
 * ── Why numbered pages and not infinite scroll ─────────────────────────
 * Because the backend guarantees a `total` computed under the same
 * predicate as the rows, and infinite scroll throws that away: it shows a
 * growing list with no statement of how much there is. For a surface whose
 * job is "I know what I am looking for", knowing whether you are seeing
 * everything is most of the value.
 *
 * ── What the total means, exactly ──────────────────────────────────────
 * How many rows the SAME filter matches for THIS reader. It is not a count
 * of what exists: rows the surface withholds are in neither the list nor
 * the total, and there is nothing here that would reveal the difference.
 */

import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";

export function Pagination({
  total,
  limit,
  offset,
  onOffsetChange,
}: {
  total: number;
  limit: number;
  offset: number;
  onOffsetChange: (offset: number) => void;
}) {
  const t = useT();
  const fmt = useFormat();

  const pageSize = limit > 0 ? limit : 1;
  const page = Math.floor(offset / pageSize) + 1;
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const first = total === 0 ? 0 : offset + 1;
  const last = Math.min(offset + pageSize, total);

  const atStart = offset <= 0;
  const atEnd = offset + pageSize >= total;

  return (
    <nav
      aria-label={t.app.library.pagination.label}
      className="flex flex-wrap items-center justify-between gap-3 border-t border-(--color-border) px-4 py-3"
    >
      <p aria-live="polite" className="text-xs text-(--color-muted-foreground)">
        {t.app.library.pagination.range
          .replace("{first}", fmt.number(first))
          .replace("{last}", fmt.number(last))
          .replace("{total}", fmt.number(total))}
      </p>

      <div className="flex items-center gap-2">
        <span className="text-xs text-(--color-muted-foreground)">
          {t.app.library.pagination.page
            .replace("{page}", fmt.number(page))
            .replace("{pages}", fmt.number(pages))}
        </span>
        <Button
          variant="outline"
          size="sm"
          disabled={atStart}
          onClick={() => onOffsetChange(Math.max(0, offset - pageSize))}
        >
          <ChevronLeft aria-hidden="true" />
          {t.app.library.pagination.previous}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={atEnd}
          onClick={() => onOffsetChange(offset + pageSize)}
        >
          {t.app.library.pagination.next}
          <ChevronRight aria-hidden="true" />
        </Button>
      </div>
    </nav>
  );
}
