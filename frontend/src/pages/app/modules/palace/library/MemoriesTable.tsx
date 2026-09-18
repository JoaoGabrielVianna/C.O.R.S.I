/**
 * The Memories tab.
 *
 * ── Why the row shows a summary and not the content ────────────────────
 * Because the operator wrote the summary to be exactly the one line a
 * listing shows. When there is none, the backend sends an excerpt and
 * says whether it cut; this renders whichever arrived and never composes
 * its own.
 *
 * ── Why importance is a floor and not a sort ───────────────────────────
 * The domain's own reasoning: ordering by importance buries the thing
 * written this morning under a four-year-old five. The listing stays in
 * recency order and the floor is how a reader narrows it.
 */

import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge, StatusBadge } from "@/modules/palace/components/Badges";
import { useConfidenceLabel, useMemoryKindLabel } from "@/modules/palace/components/labels";
import {
  LibraryEmpty,
  LibraryError,
  LibraryRefreshing,
  LibrarySkeleton,
} from "@/modules/palace/components/LibraryStates";
import { Pagination } from "@/modules/palace/components/Pagination";
import { usePalaceMemories, usePalaceRooms } from "@/modules/palace/hooks/usePalace";
import {
  PAGE_SIZE,
  UNFILED,
  resetPage,
  type LibraryParams,
} from "@/modules/palace/hooks/useLibraryParams";
import type { MemoryKind } from "@/modules/palace/api/types";

import { FilterBar, Select } from "./Filters";
import { Cell, LibraryRow, LibraryTable, LinkCell } from "./Table";

const COLUMNS = 6;
const KINDS: readonly MemoryKind[] = [
  "fact",
  "preference",
  "idea",
  "decision",
  "learning",
  "reflection",
];
const IMPORTANCE = [1, 2, 3, 4, 5] as const;

export function MemoriesTable({
  params,
  setParams,
}: {
  params: LibraryParams;
  setParams: (next: Partial<LibraryParams>) => void;
}) {
  const t = useT();
  const fmt = useFormat();
  const kindLabel = useMemoryKindLabel();
  const confidenceLabel = useConfidenceLabel();

  const query = usePalaceMemories({
    status: params.status,
    search: params.q || undefined,
    kind: params.memoryKind,
    room_id: params.room,
    artifact_id: params.artifact,
    min_importance: params.minImportance > 0 ? params.minImportance : undefined,
    limit: PAGE_SIZE,
    offset: params.offset,
  });

  const rooms = usePalaceRooms({ status: "active", limit: 100, offset: 0 });

  const filtered =
    Boolean(params.q) ||
    params.status !== "active" ||
    Boolean(params.memoryKind) ||
    Boolean(params.room) ||
    Boolean(params.artifact) ||
    params.minImportance > 0;

  return (
    <>
      <FilterBar
        search={params.q}
        searchPlaceholder={t.app.library.filters.searchMemories}
        onSearchChange={(q) => setParams(resetPage({ q }))}
        onClear={() =>
          setParams(
            resetPage({
              q: "",
              status: "active",
              memoryKind: undefined,
              room: undefined,
              artifact: undefined,
              minImportance: 0,
            }),
          )
        }
        showClear={filtered}
      >
        <Select
          id="palace-memories-kind"
          label={t.app.library.filters.kind}
          value={params.memoryKind ?? ""}
          onChange={(kind) =>
            setParams(resetPage({ memoryKind: kind ? (kind as MemoryKind) : undefined }))
          }
        >
          <option value="">{t.app.library.filters.anyKind}</option>
          {KINDS.map((kind) => (
            <option key={kind} value={kind}>
              {kindLabel(kind)}
            </option>
          ))}
        </Select>

        <Select
          id="palace-memories-room"
          label={t.app.library.filters.room}
          value={params.room ?? ""}
          onChange={(room) => setParams(resetPage({ room: room || undefined }))}
        >
          <option value="">{t.app.library.filters.anyRoom}</option>
          <option value={UNFILED}>{t.app.palace.unfiled}</option>
          {(rooms.data?.items ?? []).map((room) => (
            <option key={room.room_id} value={room.room_id}>
              {room.name}
            </option>
          ))}
        </Select>

        <Select
          id="palace-memories-importance"
          label={t.app.library.filters.importance}
          value={params.minImportance > 0 ? String(params.minImportance) : ""}
          onChange={(value) =>
            setParams(resetPage({ minImportance: value ? Number.parseInt(value, 10) : 0 }))
          }
        >
          <option value="">{t.app.library.filters.anyImportance}</option>
          {IMPORTANCE.map((level) => (
            <option key={level} value={level}>
              {fmt.number(level)}
            </option>
          ))}
        </Select>

        <Select
          id="palace-memories-status"
          label={t.app.library.filters.status}
          value={params.status}
          onChange={(status) =>
            setParams(resetPage({ status: status === "archived" ? "archived" : "active" }))
          }
        >
          <option value="active">{t.app.palace.status.active}</option>
          <option value="archived">{t.app.palace.status.archived}</option>
        </Select>
      </FilterBar>

      {/* A refetch for a new question keeps the previous rows on screen
          (see `usePalace`), so the surface has to say which they are. It
          is a sibling of the table, never an attribute on it: the rows are
          real and stay readable, focusable and navigable while this is
          up. */}
      <LibraryRefreshing refreshing={query.isFetching && !query.isPending} />

      <LibraryTable
        caption={t.app.library.a11y.memories}
        headers={[
          t.app.library.columns.summary,
          t.app.library.columns.kind,
          t.app.library.columns.importance,
          t.app.library.columns.confidence,
          t.app.library.columns.status,
          t.app.library.columns.updated,
        ]}
      >
        {query.isPending ? <LibrarySkeleton columns={COLUMNS} /> : null}

        {query.isError ? (
          <LibraryError
            columns={COLUMNS}
            error={query.error}
            onRetry={() => void query.refetch()}
          />
        ) : null}

        {query.isSuccess && query.data.items.length === 0 ? (
          <LibraryEmpty
            columns={COLUMNS}
            title={
              params.q
                ? t.app.library.states.noMatchTitle
                : filtered
                  ? t.app.library.states.emptyArchivedTitle
                  : t.app.library.states.emptyMemoriesTitle
            }
            body={
              params.q
                ? t.app.library.states.noMatchBody
                : filtered
                  ? t.app.library.states.emptyArchivedBody
                  : t.app.library.states.emptyMemoriesBody
            }
          />
        ) : null}

        {query.isSuccess && query.data.items.length > 0 ? (
          <tbody>
            {query.data.items.map((memory) => (
              <LibraryRow key={memory.memory_id}>
                <LinkCell
                  to={`/app/modules/palace/library/memories/${memory.memory_id}`}
                  label={t.app.library.a11y.openMemory}
                >
                  {memory.summary || memory.content_excerpt}
                </LinkCell>
                <Cell className="whitespace-nowrap text-(--color-muted-foreground)">
                  {kindLabel(memory.kind)}
                </Cell>
                <Cell className="tabular-nums text-(--color-muted-foreground)">
                  {fmt.number(memory.importance)}
                </Cell>
                <Cell className="whitespace-nowrap text-(--color-muted-foreground)">
                  {confidenceLabel(memory.confidence)}
                </Cell>
                <Cell>
                  <span className="flex flex-wrap gap-1.5">
                    <StatusBadge status={memory.status} />
                    <SensitivityBadge sensitivity={memory.sensitivity} />
                  </span>
                </Cell>
                <Cell className="whitespace-nowrap text-(--color-muted-foreground)">
                  {fmt.date(memory.updated_at, "short")}
                </Cell>
              </LibraryRow>
            ))}
          </tbody>
        ) : null}
      </LibraryTable>

      {query.isSuccess ? (
        <Pagination
          total={query.data.total}
          limit={query.data.limit}
          offset={query.data.offset}
          onOffsetChange={(offset) => setParams({ offset })}
        />
      ) : null}
    </>
  );
}
