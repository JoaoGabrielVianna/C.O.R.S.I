/**
 * The room, as rows.
 *
 * ── What this is for ───────────────────────────────────────────────────
 * Two jobs, and they are the same job. It is what D5 hands over to when
 * the scene cannot offer a target big enough to press, and it is the
 * semantic equivalent a reader can always reach through "see as a list".
 *
 * ── Why it is not the full Library ─────────────────────────────────────
 * Because the question here is narrower: "what is in THIS room". Filters,
 * tabs and search belong to the Library proper, one link away, and
 * rebuilding them inside a room would be a second search surface with its
 * own idea of what a filter means.
 *
 * It reads the same endpoint the scene reads, with the same parameters, so
 * the two representations cannot show different rooms.
 */

import { Link } from "react-router-dom";

import { useFormat, useT } from "@/lib/i18n";
import { SensitivityBadge, StatusBadge } from "@/modules/palace/components/Badges";
import { useArtifactKindLabel } from "@/modules/palace/components/labels";
import { usePalaceArtifacts } from "@/modules/palace/hooks/usePalace";

export function RoomLibrary({ roomId }: { roomId: string | undefined }) {
  const t = useT();
  const fmt = useFormat();
  const kindLabel = useArtifactKindLabel();

  const query = usePalaceArtifacts({
    room_id: roomId,
    status: "active",
    limit: 100,
    offset: 0,
  });

  if (query.isPending) {
    return (
      <div aria-busy="true" aria-label={t.app.library.states.loading} className="space-y-2">
        {[0, 1, 2, 3, 4].map((i) => (
          <div key={i} aria-hidden="true" className="h-10 animate-pulse rounded-lg bg-(--color-muted)" />
        ))}
      </div>
    );
  }

  if (query.isError) {
    return <p className="text-sm text-(--color-muted-foreground)">{query.error.message}</p>;
  }

  if (query.data.items.length === 0) {
    return (
      <div className="py-10 text-center">
        <p className="text-sm font-medium">{t.app.palace.scene.emptyRoomTitle}</p>
        <p className="mt-1.5 text-sm text-(--color-muted-foreground)">
          {t.app.palace.scene.emptyRoomBody}
        </p>
      </div>
    );
  }

  return (
    <ul className="divide-y divide-(--color-border) rounded-xl border border-(--color-border)">
      {query.data.items.map((artifact) => (
        <li key={artifact.artifact_id}>
          <Link
            to={`/app/modules/palace/library/artifacts/${artifact.artifact_id}`}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-sm outline-none hover:bg-(--color-muted)/60 focus-visible:ring-2 focus-visible:ring-(--color-ring)/50"
          >
            <span className="font-medium">{artifact.title}</span>
            <span className="text-xs text-(--color-muted-foreground)">
              {kindLabel(artifact.kind)}
            </span>
            {artifact.item_count > 0 ? (
              <span className="text-xs tabular-nums text-(--color-muted-foreground)">
                {fmt.number(artifact.item_done_count)}
                {" / "}
                {fmt.number(artifact.item_count)}
              </span>
            ) : null}
            <StatusBadge status={artifact.status} />
            <SensitivityBadge sensitivity={artifact.sensitivity} />
          </Link>
        </li>
      ))}
    </ul>
  );
}
