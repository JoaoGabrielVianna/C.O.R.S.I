/**
 * The Artifacts tab.
 *
 * ── The room filter has three states, not two ──────────────────────────
 * A room, `none`, or nothing. "Filed nowhere" is a real and ordinary
 * state — the thing exists before anybody decides where it belongs — so it
 * is a first-class option rather than something a reader has to guess at.
 * It is not the same as "no filter", and the control says so.
 *
 * The list of rooms in that control is itself a Palace listing, so a room
 * the surface withholds is not an option in it. Nothing here had to know
 * that.
 */

import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge, StatusBadge } from "@/modules/palace/components/Badges";
import { useArtifactKindLabel } from "@/modules/palace/components/labels";
import {
  LibraryEmpty,
  LibraryError,
  LibraryRefreshing,
  LibrarySkeleton,
} from "@/modules/palace/components/LibraryStates";
import { Pagination } from "@/modules/palace/components/Pagination";
import { usePalaceArtifacts, usePalaceRooms } from "@/modules/palace/hooks/usePalace";
import {
  PAGE_SIZE,
  UNFILED,
  resetPage,
  type LibraryParams,
} from "@/modules/palace/hooks/useLibraryParams";
import type { ArtifactKind } from "@/modules/palace/api/types";

import { FilterBar, Select } from "./Filters";
import { Cell, LibraryRow, LibraryTable, LinkCell } from "./Table";

const COLUMNS = 6;
const KINDS: readonly ArtifactKind[] = ["project", "list", "plan", "note"];

export function ArtifactsTable({
  params,
  setParams,
}: {
  params: LibraryParams;
  setParams: (next: Partial<LibraryParams>) => void;
}) {
  const t = useT();
  const fmt = useFormat();
  const kindLabel = useArtifactKindLabel();

  const query = usePalaceArtifacts({
    status: params.status,
    search: params.q || undefined,
    kind: params.artifactKind,
    room_id: params.room,
    limit: PAGE_SIZE,
    offset: params.offset,
  });

  // The room control's options. Active rooms only: filtering by an
  // archived room is a question nobody asked on this screen, and the
  // listing already has a status filter of its own.
  const rooms = usePalaceRooms({ status: "active", limit: 100, offset: 0 });
  const roomName = new Map((rooms.data?.items ?? []).map((r) => [r.room_id, r.name]));

  const filtered =
    Boolean(params.q) ||
    params.status !== "active" ||
    Boolean(params.artifactKind) ||
    Boolean(params.room);

  return (
    <>
      <FilterBar
        search={params.q}
        searchPlaceholder={t.app.library.filters.searchArtifacts}
        onSearchChange={(q) => setParams(resetPage({ q }))}
        onClear={() =>
          setParams(
            resetPage({ q: "", status: "active", artifactKind: undefined, room: undefined }),
          )
        }
        showClear={filtered}
      >
        <Select
          id="palace-artifacts-kind"
          label={t.app.library.filters.kind}
          value={params.artifactKind ?? ""}
          onChange={(kind) =>
            setParams(
              resetPage({ artifactKind: kind ? (kind as ArtifactKind) : undefined }),
            )
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
          id="palace-artifacts-room"
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
          id="palace-artifacts-status"
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
        caption={t.app.library.a11y.artifacts}
        headers={[
          t.app.library.columns.title,
          t.app.library.columns.kind,
          t.app.library.columns.room,
          t.app.library.columns.items,
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
                  : t.app.library.states.emptyArtifactsTitle
            }
            body={
              params.q
                ? t.app.library.states.noMatchBody
                : filtered
                  ? t.app.library.states.emptyArchivedBody
                  : t.app.library.states.emptyArtifactsBody
            }
          />
        ) : null}

        {query.isSuccess && query.data.items.length > 0 ? (
          <tbody>
            {query.data.items.map((artifact) => (
              <LibraryRow key={artifact.artifact_id}>
                <LinkCell
                  to={`/app/modules/palace/library/artifacts/${artifact.artifact_id}`}
                  label={t.app.library.a11y.openArtifact.replace("{title}", artifact.title)}
                >
                  {artifact.title}
                </LinkCell>
                <Cell className="whitespace-nowrap text-(--color-muted-foreground)">
                  {kindLabel(artifact.kind)}
                </Cell>
                <Cell className="text-(--color-muted-foreground)">
                  {artifact.room_id
                    ? (roomName.get(artifact.room_id) ?? "")
                    : t.app.palace.unfiled}
                </Cell>
                <Cell className="whitespace-nowrap tabular-nums text-(--color-muted-foreground)">
                  {artifact.item_count > 0 ? (
                    <span
                      aria-label={t.app.library.a11y.items
                        .replace("{done}", fmt.number(artifact.item_done_count))
                        .replace("{total}", fmt.number(artifact.item_count))}
                    >
                      {fmt.number(artifact.item_done_count)}
                      {" / "}
                      {fmt.number(artifact.item_count)}
                    </span>
                  ) : null}
                </Cell>
                <Cell>
                  <span className="flex flex-wrap gap-1.5">
                    <StatusBadge status={artifact.status} />
                    <SensitivityBadge sensitivity={artifact.sensitivity} />
                  </span>
                </Cell>
                <Cell className="whitespace-nowrap text-(--color-muted-foreground)">
                  {fmt.date(artifact.updated_at, "short")}
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
