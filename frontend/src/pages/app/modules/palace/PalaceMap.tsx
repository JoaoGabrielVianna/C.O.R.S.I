/**
 * The Palace's entrance.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A SHELF OF DIORAMAS, NOT A FLOOR PLAN
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── Why not a single plan with the rooms placed on it ──────────────────
 * Because a plan positions rooms in a space, and a reader takes position
 * for meaning however carefully we avoid encoding one: "why is this room
 * next to that one?" has no answer, and the honest answer — "it is not
 * next to anything" — is not what the picture says. Semantic adjacency
 * is forbidden, so the entrance refuses to draw anything that looks like
 * it. A grid of vignettes keeps the isometric language and claims nothing.
 *
 * ── Why the vignette shows no artifacts ────────────────────────────────
 * A miniature with three desks in a room that holds fourteen invites
 * counting, and counting a picture is how somebody ends up wrong about
 * their own record. The vignette draws the ARCHITECTURE; the numbers are
 * text.
 *
 * ── What the numbers are ───────────────────────────────────────────────
 * Active artifacts and memories. Not `archived_count`: the map is the
 * active space, and archived content has its own address in the Library.
 */

import { Link } from "react-router-dom";

import { useFormat, useT } from "@/lib/i18n";
import { Button } from "@/components/ui/Button";
import { SensitivityBadge } from "@/modules/palace/components/Badges";
import { usePalaceOverview } from "@/modules/palace/hooks/usePalace";
import { RoomVignette, UnfiledTray } from "@/modules/palace/scene/Vignettes";
import { decorationFor } from "@/modules/palace/scene/decoration";

export function PalaceMap() {
  const t = useT();
  const fmt = useFormat();
  const query = usePalaceOverview();

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6">
      <header className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{t.app.palace.scene.mapTitle}</h1>
          <p className="mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
            {t.app.palace.scene.mapDescription}
          </p>
        </div>
        <Button variant="outline" size="sm" asChild>
          <Link to="/app/modules/palace/library">{t.app.library.title}</Link>
        </Button>
      </header>

      {query.isPending ? (
        <div
          aria-busy="true"
          aria-label={t.app.library.states.loading}
          className="h-40 animate-pulse rounded-2xl bg-(--color-muted)"
        />
      ) : null}

      {query.isError ? (
        <div className="rounded-2xl border border-(--color-border) p-8 text-center">
          <p className="text-sm font-medium">{t.app.library.states.errorTitle}</p>
          <p className="mt-1.5 text-sm text-(--color-muted-foreground)">
            {query.error.message}
          </p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => void query.refetch()}>
            {t.app.library.states.retry}
          </Button>
        </div>
      ) : null}

      {query.isSuccess ? (
        query.data.rooms.length === 0 && query.data.unfiled.artifact_count === 0 ? (
          <div className="rounded-2xl border border-(--color-border) p-14 text-center">
            <p className="text-sm font-medium">{t.app.palace.scene.mapEmptyTitle}</p>
            <p className="mx-auto mt-1.5 max-w-prose text-sm text-(--color-muted-foreground)">
              {t.app.palace.scene.mapEmptyBody}
            </p>
          </div>
        ) : (
          <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {query.data.rooms.map((room) => (
              <li key={room.room_id}>
                <Link
                  to={`/app/modules/palace/rooms/${room.room_id}`}
                  aria-label={t.app.palace.scene.openRoom.replace("{name}", room.name)}
                  className="group block overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft) outline-none transition-[box-shadow,transform] duration-200 [transition-timing-function:var(--ease-premium)] hover:-translate-y-px hover:shadow-(--shadow-lift) focus-visible:ring-2 focus-visible:ring-(--color-ring)"
                >
                  <RoomVignette decoration={decorationFor(room.room_id)} />
                  <div className="p-4">
                    <div className="flex items-start justify-between gap-2">
                      <h2 className="text-sm font-semibold">{room.name}</h2>
                      <SensitivityBadge sensitivity={room.sensitivity} />
                    </div>
                    <p className="mt-1 text-xs text-(--color-muted-foreground)">
                      {t.app.palace.scene.counts
                        .replace("{artifacts}", fmt.number(room.artifact_count))
                        .replace("{memories}", fmt.number(room.memory_count))}
                    </p>
                  </div>
                </Link>
              </li>
            ))}

            {/* Only when it holds something, and never with the silhouette
                of a room: it is a staging tray, not a place. */}
            {query.data.unfiled.artifact_count > 0 ? (
              <li>
                <Link
                  to="/app/modules/palace/library?tab=artifacts&room=none"
                  aria-label={t.app.palace.scene.openUnfiled}
                  className="group block overflow-hidden rounded-2xl border border-dashed border-(--color-border-strong) bg-(--color-muted)/40 outline-none transition-[box-shadow,transform] duration-200 [transition-timing-function:var(--ease-premium)] hover:-translate-y-px focus-visible:ring-2 focus-visible:ring-(--color-ring)"
                  data-testid="unfiled-tray"
                >
                  <UnfiledTray />
                  <div className="p-4">
                    <h2 className="text-sm font-semibold">{t.app.palace.scene.unfiledTitle}</h2>
                    <p className="mt-1 text-xs text-(--color-muted-foreground)">
                      {t.app.palace.scene.unfiledBody}
                    </p>
                  </div>
                </Link>
              </li>
            ) : null}
          </ul>
        )
      ) : null}
    </div>
  );
}
