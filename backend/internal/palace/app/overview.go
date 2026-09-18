package app

// The summaries a projection needs, computed the way a projection has to
// be able to trust.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A COUNT IS A FACT ABOUT WHAT THE CALLER COULD REACH, OR IT IS A LEAK
//
// ══════════════════════════════════════════════════════════════════════
//
// Every number produced here is computed under the SAME visibility rules
// as the listing it summarises. That is not a nicety: a room card reading
// "7 objetos" above a listing that can only ever return 6 has published
// the existence of the seventh, and it did so without returning one
// character of its content. Withholding text while leaking the count is
// withholding nothing that matters.
//
// The rules are not applied here. They are passed down into the filter and
// enforced in SQL, which is what lets the count and the listing be the
// same predicate rather than two predicates that agree for now.
//
// ── Why the reads are grouped and not looped ───────────────────────────
// A workspace with forty rooms would otherwise be forty round trips for
// one screen, each doing almost nothing. Every summary below is a fixed
// number of statements regardless of how many rooms, artifacts or entries
// exist. There is no cache, and there should not be one yet: the honest
// version of "this might get slow" is a measurement, not a layer.

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// overviewRoomRequest is what the overview asks for.
//
// The repository lowers any request above its own ceiling rather than
// refusing, so this is a way of saying "as many as you will give me". The
// total comes back alongside precisely so a caller can tell that it got
// fewer than exist, which is the one thing a map must not be silent about.
const overviewRoomRequest = 1000

// RoomOverview is one room on the map, with what it contains.
//
// The three counts are of ELIGIBLE rows only, under the caller's
// visibility. A room the surface will not show does not appear at all, and
// a row the surface will not show is in none of these numbers.
type RoomOverview struct {
	Room *domain.Room

	ArtifactCount int64
	MemoryCount   int64
	// ArchivedCount is what the room has retired. It is NOT a withheld
	// count: archiving is the operator's own organising act and the rows
	// stay reachable through an explicit status filter. The distinction
	// matters because the affordance that opens the archive has to know
	// whether there is anything behind it, and inventing that number would
	// be an empty drawer.
	ArchivedCount int64
}

// UnfiledOverview is what belongs to no room.
//
// ── Why it is not a RoomOverview with a nil Room ───────────────────────
// Because it is not a room, and a caller that had to special-case a nil
// field would eventually forget to. Unfiled has no name, no description
// and no sensitivity of its own; the only things it shares with a room are
// the two counts.
type UnfiledOverview struct {
	ArtifactCount int64
	ArchivedCount int64
}

// Overview is the whole map, in one answer.
type Overview struct {
	Rooms []RoomOverview
	// RoomTotal is how many eligible active rooms exist, ignoring the
	// window Rooms was cut to.
	//
	// ── Why a map carries a total at all ───────────────────────────────
	// Because the alternative is a map that is silently short. Every
	// listing in this context reports the unbounded total for the same
	// reason, and a summary is the surface where a missing row is hardest
	// to notice: there is no page two to be absent from.
	//
	// It counts only what the caller could reach, exactly like the counts
	// beside each room.
	RoomTotal int64

	Unfiled UnfiledOverview
}

// Overview builds the map of eligible active rooms and what they hold.
//
// Five statements, whatever the size of the workspace: the rooms, their
// total, and three grouped counts.
func (s *Service) Overview(ctx context.Context, workspaceID uuid.UUID, v ports.Visibility) (*Overview, error) {
	active := domain.LifecycleActive
	archived := domain.LifecycleArchived

	roomFilter := ports.RoomFilter{
		Status:    &active,
		Sensitive: ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		Page:      ports.Page{Limit: overviewRoomRequest},
	}
	rooms, err := s.rooms.List(ctx, workspaceID, roomFilter)
	if err != nil {
		return nil, err
	}
	roomTotal, err := s.rooms.Count(ctx, workspaceID, roomFilter)
	if err != nil {
		return nil, err
	}

	artifacts, err := s.artifacts.CountByRoom(ctx, workspaceID, ports.ArtifactFilter{
		Status:                &active,
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	if err != nil {
		return nil, err
	}
	archivedArtifacts, err := s.artifacts.CountByRoom(ctx, workspaceID, ports.ArtifactFilter{
		Status:                &archived,
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	if err != nil {
		return nil, err
	}
	memories, err := s.memories.CountByRoom(ctx, workspaceID, ports.MemoryFilter{
		Status:                &active,
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	if err != nil {
		return nil, err
	}

	byRoom := indexCounts(artifacts)
	archivedByRoom := indexCounts(archivedArtifacts)
	memoriesByRoom := indexCounts(memories)

	out := &Overview{
		Rooms:     make([]RoomOverview, 0, len(rooms)),
		RoomTotal: roomTotal,
		Unfiled: UnfiledOverview{
			// The NULL group of the same grouped reads. It costs no extra
			// statement, and it is exact: `room_id IS NULL` is not a room
			// that was withheld, it is a row that was filed nowhere.
			ArtifactCount: byRoom[uuid.Nil],
			ArchivedCount: archivedByRoom[uuid.Nil],
		},
	}
	for _, room := range rooms {
		out.Rooms = append(out.Rooms, RoomOverview{
			Room:          room,
			ArtifactCount: byRoom[room.ID],
			MemoryCount:   memoriesByRoom[room.ID],
			ArchivedCount: archivedByRoom[room.ID],
		})
	}
	return out, nil
}

// indexCounts turns a grouped read into a lookup.
//
// The NULL group lands under uuid.Nil, which is safe because a real room
// can never carry that id: the domain refuses a zero id at validation, and
// the column is generated. A missing key reads as zero, which is what a
// room with nothing in it means.
func indexCounts(counts []ports.RoomCount) map[uuid.UUID]int64 {
	out := make(map[uuid.UUID]int64, len(counts))
	for _, c := range counts {
		id := uuid.Nil
		if c.RoomID != nil {
			id = *c.RoomID
		}
		out[id] = c.Count
	}
	return out
}

/* ── one room ────────────────────────────────────────────────────────── */

// RoomTallies is what a single room contains, for the room's own read.
type RoomTallies struct {
	ArtifactCount int64
	MemoryCount   int64
	ArchivedCount int64
}

// TalliesForRoom counts what one room holds, under the caller's
// visibility.
//
// Three statements. It does NOT resolve the room: the caller has already
// done that through the surface, and doing it again here would be a second
// opinion about whether the room may be seen.
func (s *Service) TalliesForRoom(ctx context.Context, workspaceID, roomID uuid.UUID, v ports.Visibility) (*RoomTallies, error) {
	active := domain.LifecycleActive
	archived := domain.LifecycleArchived
	sensitive := ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive}

	artifacts, err := s.artifacts.Count(ctx, workspaceID, ports.ArtifactFilter{
		Status:                &active,
		RoomID:                &roomID,
		Sensitive:             sensitive,
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	if err != nil {
		return nil, err
	}
	archivedCount, err := s.artifacts.Count(ctx, workspaceID, ports.ArtifactFilter{
		Status:                &archived,
		RoomID:                &roomID,
		Sensitive:             sensitive,
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	if err != nil {
		return nil, err
	}
	memories, err := s.memories.Count(ctx, workspaceID, ports.MemoryFilter{
		Status:                &active,
		RoomID:                &roomID,
		Sensitive:             sensitive,
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	if err != nil {
		return nil, err
	}

	return &RoomTallies{
		ArtifactCount: artifacts,
		MemoryCount:   memories,
		ArchivedCount: archivedCount,
	}, nil
}

/* ── a page of artifacts ─────────────────────────────────────────────── */

// TalliesForArtifacts counts the entries of a whole page in one statement.
//
// ── Why the caller passes ids instead of this reading the page ─────────
// Because the page was already produced under a filter this function has
// no business reproducing. Taking the ids means this cannot disagree with
// what was listed: it can only describe it.
func (s *Service) TalliesForArtifacts(ctx context.Context, workspaceID uuid.UUID, artifactIDs []uuid.UUID) (map[uuid.UUID]ports.ItemTally, error) {
	return s.items.CountByArtifacts(ctx, workspaceID, artifactIDs)
}
