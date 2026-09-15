package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── reads ───────────────────────────────────────────────────────────── */

// ListRooms returns matching rooms and the unbounded total.
//
// ── Why the total comes back alongside ─────────────────────────────────
// Because every caller needs to know whether it is looking at everything.
// A model that received a truncated list and believed it was complete
// would answer "você tem quatro salas" with confidence.
//
// The total obeys the SAME sensitivity rule as the list, which the
// repository guarantees by sharing one predicate. A count that included
// withheld rows would report a number larger than the list and announce
// the existence of the row being withheld.
func (s *Service) ListRooms(ctx context.Context, workspaceID uuid.UUID, f ports.RoomFilter) ([]*domain.Room, int64, error) {
	items, err := s.rooms.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.rooms.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetRoom(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Room, error) {
	return s.rooms.FindByID(ctx, workspaceID, id)
}

/* ── writes ──────────────────────────────────────────────────────────── */

// CreateRoomInput is one new room, as any caller states it.
//
// ── Why there is no Status ─────────────────────────────────────────────
// A room created already archived is a room nobody asked for. Archiving
// is something that happens to a room that existed and stopped being
// used, and offering it at creation would be offering a state with no
// history behind it. New rooms are active; `palace.room.update` archives
// them.
type CreateRoomInput struct {
	Name        string
	Description string
	// Sensitivity is optional. Nil means DefaultSensitivity, which is
	// `normal`: the operator says when something is more than ordinary,
	// and defaulting to private would label everything private, which has
	// the same effect as labelling nothing.
	Sensitivity *domain.Sensitivity
}

func (s *Service) CreateRoom(ctx context.Context, workspaceID uuid.UUID, in CreateRoomInput) (*domain.Room, error) {
	sensitivity := domain.DefaultSensitivity
	if in.Sensitivity != nil {
		sensitivity = *in.Sensitivity
	}

	room := &domain.Room{
		WorkspaceID: workspaceID,
		Name:        domain.NormalizeName(in.Name),
		Description: in.Description,
		Status:      domain.DefaultLifecycle,
		Sensitivity: sensitivity,
	}
	if err := room.Validate(); err != nil {
		return nil, err
	}
	if err := s.rooms.Create(ctx, workspaceID, room); err != nil {
		return nil, err
	}
	return room, nil
}

// RoomUpdateResult is a completed edit: the room as it now is, plus what
// actually moved.
type RoomUpdateResult struct {
	Room *domain.Room
	domain.RoomChangeResult
}

// UpdateRoom applies a partial change to one room.
//
// ── Why it reads before it writes ──────────────────────────────────────
// Three reasons, and all three are load-bearing. The domain needs the
// current values to report what CHANGED rather than what was sent. An
// update naming a field it is not touching must leave the stored value
// alone, which means the stored value has to be in hand. And a room this
// workspace cannot see must fail HERE, as a not-found indistinguishable
// from a fabricated id, rather than as an UPDATE that silently affects
// zero rows and reports success.
//
// ── Why an empty change is refused ─────────────────────────────────────
// Because it is always a mistake at the point it is made: a caller that
// meant to change nothing had no reason to call, and a model that
// produced it misunderstood the contract. Accepting it would move
// `updated_at`, which reorders every listing: an edit that did nothing,
// announcing itself as the most recent work.
func (s *Service) UpdateRoom(ctx context.Context, workspaceID, id uuid.UUID, c domain.RoomChange) (*RoomUpdateResult, error) {
	if c.Empty() {
		return nil, domain.Invalid(
			"an update must change at least one of name, description, status or sensitivity")
	}

	room, err := s.rooms.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	changed := room.Apply(c)
	if err := room.Validate(); err != nil {
		return nil, err
	}

	// A change that moved nothing is not written. The row is already in
	// the requested state, and touching `updated_at` would claim activity
	// that did not happen. The caller is told, and says so.
	if changed.Unchanged() {
		return &RoomUpdateResult{Room: room, RoomChangeResult: changed}, nil
	}

	if err := s.rooms.Update(ctx, workspaceID, room); err != nil {
		return nil, err
	}
	return &RoomUpdateResult{Room: room, RoomChangeResult: changed}, nil
}
