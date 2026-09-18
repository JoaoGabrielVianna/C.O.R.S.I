/**
 * What an artifact says and what it contains, drawn once.
 *
 * ── Why this was extracted ─────────────────────────────────────────────
 * The Library's detail page and the room's inspector show the same two
 * things. Two components would be two places to decide that an entry with
 * no text renders as a blank row, or that a done entry is struck through,
 * and they would answer differently within a month. One component means
 * the room and the list cannot disagree about the operator's own work.
 *
 * It is deliberately presentational and neutral: it takes a loaded
 * artifact and renders it. No fetching, no domain rule, no navigation, no
 * writes. The two callers own their own shells, their own headers and
 * their own pagination, because those differ and should.
 */

import { useFormat, useT } from "@/lib/i18n";

import type { ArtifactDetail } from "../api/types";

export function ArtifactBody({ artifact }: { artifact: ArtifactDetail }) {
  const t = useT();
  return (
    <section className="mb-8">
      <h3 className="mb-2 text-sm font-semibold text-(--color-foreground)">
        {t.app.library.detail.body}
      </h3>
      {artifact.body ? (
        <p className="max-w-prose text-sm whitespace-pre-wrap text-(--color-foreground)">
          {artifact.body}
        </p>
      ) : (
        <p className="text-sm text-(--color-muted-foreground)">
          {t.app.library.detail.noBody}
        </p>
      )}
    </section>
  );
}

export function ArtifactItems({ artifact }: { artifact: ArtifactDetail }) {
  const t = useT();
  const fmt = useFormat();
  const doneCount = artifact.items.filter((item) => item.done).length;

  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold text-(--color-foreground)">
        {t.app.library.detail.items}
      </h3>

      {artifact.item_total === 0 ? (
        <p className="text-sm text-(--color-muted-foreground)">
          {t.app.library.detail.noItems}
        </p>
      ) : (
        <>
          <p className="mb-3 text-xs text-(--color-muted-foreground)">
            {t.app.library.detail.itemsProgress
              .replace("{done}", fmt.number(doneCount))
              .replace("{total}", fmt.number(artifact.items.length))}
          </p>
          {/*
            A list of statements, not a form. These entries are read only
            on both surfaces: a checkbox a reader can click but not save is
            a control that lies about what it does, so the state is shown
            with a marker and the box is absent.
          */}
          <ul className="divide-y divide-(--color-border) rounded-xl border border-(--color-border)">
            {artifact.items.map((item) => (
              <li key={item.item_id} className="flex gap-3 px-4 py-2.5 text-sm">
                <span
                  aria-hidden="true"
                  className={
                    item.done ? "text-(--color-brand-600)" : "text-(--color-muted-foreground)"
                  }
                >
                  {item.done ? "✓" : "·"}
                </span>
                <span
                  className={
                    item.done
                      ? "text-(--color-muted-foreground) line-through"
                      : "text-(--color-foreground)"
                  }
                >
                  {item.text}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

export function ArtifactContent({ artifact }: { artifact: ArtifactDetail }) {
  return (
    <>
      <ArtifactBody artifact={artifact} />
      <ArtifactItems artifact={artifact} />
    </>
  );
}
