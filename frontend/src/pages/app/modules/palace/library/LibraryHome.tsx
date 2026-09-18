/**
 * The Library: the objective projection of the Semantic Palace.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   "I KNOW WHAT I AM LOOKING FOR"
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * This surface is deliberately NOT spatial and should not grow an
 * identity. The spatial Palace answers a different question — "I want to
 * see what is there" — and the two are worth keeping apart: a Library that
 * tried to be evocative would be slower at the one thing it is for, and a
 * Palace that tried to be a table would have no reason to exist.
 *
 * Both read the same application service through the same routes. That is
 * the claim this slice exists to make true, and it is why nothing here
 * keeps state the backend does not have.
 */

import { useT } from "@/lib/i18n";
import {
  LIBRARY_TABS,
  resetPage,
  useLibraryParams,
  type LibraryTab,
} from "@/modules/palace/hooks/useLibraryParams";
import { cn } from "@/lib/utils";

import { ArtifactsTable } from "./ArtifactsTable";
import { MemoriesTable } from "./MemoriesTable";
import { RoomsTable } from "./RoomsTable";

export function LibraryHome() {
  const t = useT();
  const { params, setParams } = useLibraryParams();

  const tabLabel: Record<LibraryTab, string> = {
    rooms: t.app.library.tabs.rooms,
    artifacts: t.app.library.tabs.artifacts,
    memories: t.app.library.tabs.memories,
  };

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6">
      <header className="mb-6">
        <p className="text-xs font-medium tracking-wide text-(--color-muted-foreground) uppercase">
          {t.app.palace.title}
        </p>
        <h1 className="mt-1 text-2xl font-semibold text-(--color-foreground)">
          {t.app.library.title}
        </h1>
        <p className="mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
          {t.app.library.description}
        </p>
      </header>

      {/*
        Native links rather than a JS tab widget. Each tab is an address:
        it can be bookmarked, opened in a new tab and reached with the back
        button, and `aria-current` tells a screen reader which one is on
        without any roving tabindex to maintain.

        Switching tab clears the filters that belong to the other ones,
        because a `kind` from Artifacts means nothing on Memories and
        leaving it in the URL would filter a list by a word it does not
        have.
      */}
      <nav aria-label={t.app.library.title} className="mb-4 flex gap-1 border-b border-(--color-border)">
        {LIBRARY_TABS.map((tab) => {
          const active = params.tab === tab;
          return (
            <button
              key={tab}
              type="button"
              aria-current={active ? "page" : undefined}
              onClick={() =>
                setParams(
                  resetPage({
                    tab,
                    artifactKind: undefined,
                    memoryKind: undefined,
                    room: undefined,
                    artifact: undefined,
                    minImportance: 0,
                  }),
                )
              }
              className={cn(
                "-mb-px border-b-2 px-3.5 py-2.5 text-sm font-medium transition-colors",
                "outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50 focus-visible:rounded-t-lg",
                active
                  ? "border-(--color-accent) text-(--color-foreground)"
                  : "border-transparent text-(--color-muted-foreground) hover:text-(--color-foreground)",
              )}
            >
              {tabLabel[tab]}
            </button>
          );
        })}
      </nav>

      <section className="rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
        {params.tab === "rooms" ? <RoomsTable params={params} setParams={setParams} /> : null}
        {params.tab === "artifacts" ? (
          <ArtifactsTable params={params} setParams={setParams} />
        ) : null}
        {params.tab === "memories" ? (
          <MemoriesTable params={params} setParams={setParams} />
        ) : null}
      </section>
    </div>
  );
}
