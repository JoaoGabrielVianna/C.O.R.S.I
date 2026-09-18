/**
 * The Rooms tab.
 *
 * A room the surface withholds is not here, is not in the total, and is
 * not hinted at. That is the backend's guarantee; this file only renders
 * what arrived.
 */

import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge, StatusBadge } from "@/modules/palace/components/Badges";
import {
  LibraryEmpty,
  LibraryError,
  LibraryRefreshing,
  LibrarySkeleton,
} from "@/modules/palace/components/LibraryStates";
import { Pagination } from "@/modules/palace/components/Pagination";
import { usePalaceRooms } from "@/modules/palace/hooks/usePalace";
import {
  PAGE_SIZE,
  resetPage,
  type LibraryParams,
} from "@/modules/palace/hooks/useLibraryParams";

import { FilterBar, Select } from "./Filters";
import { Cell, LibraryRow, LibraryTable, LinkCell } from "./Table";

const COLUMNS = 4;

export function RoomsTable({
  params,
  setParams,
}: {
  params: LibraryParams;
  setParams: (next: Partial<LibraryParams>) => void;
}) {
  const t = useT();
  const fmt = useFormat();

  const query = usePalaceRooms({
    status: params.status,
    search: params.q || undefined,
    limit: PAGE_SIZE,
    offset: params.offset,
  });

  const filtered = Boolean(params.q) || params.status !== "active";

  return (
    <>
      <FilterBar
        search={params.q}
        searchPlaceholder={t.app.library.filters.searchRooms}
        onSearchChange={(q) => setParams(resetPage({ q }))}
        onClear={() => setParams(resetPage({ q: "", status: "active" }))}
        showClear={filtered}
      >
        <Select
          id="palace-rooms-status"
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
        caption={t.app.library.a11y.rooms}
        headers={[
          t.app.library.columns.name,
          t.app.library.columns.description,
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
              filtered
                ? params.q
                  ? t.app.library.states.noMatchTitle
                  : t.app.library.states.emptyArchivedTitle
                : t.app.library.states.emptyRoomsTitle
            }
            body={
              filtered
                ? params.q
                  ? t.app.library.states.noMatchBody
                  : t.app.library.states.emptyArchivedBody
                : t.app.library.states.emptyRoomsBody
            }
          />
        ) : null}

        {query.isSuccess && query.data.items.length > 0 ? (
          <tbody>
            {query.data.items.map((room) => (
              <LibraryRow key={room.room_id}>
                <LinkCell
                  to={`/app/modules/palace/library/rooms/${room.room_id}`}
                  label={t.app.library.a11y.openRoom.replace("{name}", room.name)}
                >
                  {room.name}
                </LinkCell>
                <Cell className="max-w-prose text-(--color-muted-foreground)">
                  {room.description}
                </Cell>
                <Cell>
                  <span className="flex flex-wrap gap-1.5">
                    <StatusBadge status={room.status} />
                    <SensitivityBadge sensitivity={room.sensitivity} />
                  </span>
                </Cell>
                <Cell className="whitespace-nowrap text-(--color-muted-foreground)">
                  {fmt.date(room.updated_at, "short")}
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
