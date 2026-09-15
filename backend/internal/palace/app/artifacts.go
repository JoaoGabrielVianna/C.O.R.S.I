package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── reads ───────────────────────────────────────────────────────────── */

// ListArtifacts returns matching artifacts and the unbounded total, which
// obeys the same sensitivity rule as the list. See ListRooms.
func (s *Service) ListArtifacts(ctx context.Context, workspaceID uuid.UUID, f ports.ArtifactFilter) ([]*domain.Artifact, int64, error) {
	items, err := s.artifacts.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.artifacts.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetArtifact(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Artifact, error) {
	return s.artifacts.FindByID(ctx, workspaceID, id)
}

/* ── writes ──────────────────────────────────────────────────────────── */

// CreateArtifactInput is one new artifact, as any caller states it.
//
// ── Why Kind is required and has no default ────────────────────────────
// Because it decides what the object IS and it can never be changed
// afterwards. A default would be the system guessing, once, at the one
// property nobody can correct: a thought captured as a `note` that should
// have been a `list` has to be recreated, and the guess would make that
// the common case rather than the mistake.
//
// ── Why there is no Status ─────────────────────────────────────────────
// An artifact created already archived is one nobody asked for. Same
// reasoning as CreateRoomInput.
type CreateArtifactInput struct {
	Kind  domain.ArtifactKind
	Title string
	Body  string
	// RoomID optionally files this artifact. Resolved in the caller's
	// workspace before anything is written. Nil is an ordinary state: the
	// thing exists before anybody decides where it belongs.
	RoomID *uuid.UUID
	// Sensitivity is optional. Nil means DefaultSensitivity.
	Sensitivity *domain.Sensitivity
}

func (s *Service) CreateArtifact(ctx context.Context, workspaceID uuid.UUID, in CreateArtifactInput) (*domain.Artifact, error) {
	sensitivity := domain.DefaultSensitivity
	if in.Sensitivity != nil {
		sensitivity = *in.Sensitivity
	}

	a := &domain.Artifact{
		WorkspaceID: workspaceID,
		RoomID:      in.RoomID,
		Kind:        in.Kind,
		Title:       domain.NormalizeName(in.Title),
		Body:        in.Body,
		Status:      domain.DefaultLifecycle,
		Sensitivity: sensitivity,
	}
	// Shape first, then the reference: a malformed artifact naming a room
	// this workspace does not have should be told what is wrong with the
	// artifact, and asking the database about a room for a row that could
	// never be written is a query nobody needed.
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := s.requireRoom(ctx, workspaceID, in.RoomID); err != nil {
		return nil, err
	}
	if err := s.artifacts.Create(ctx, workspaceID, a); err != nil {
		return nil, err
	}
	return a, nil
}

// ArtifactUpdateResult is a completed edit: the artifact as it now is,
// plus what actually moved.
type ArtifactUpdateResult struct {
	Artifact *domain.Artifact
	domain.ArtifactChangeResult
}

// UpdateArtifact applies a partial change to one artifact.
//
// Reads before it writes, and refuses an empty change, for the reasons
// UpdateRoom states in full.
//
// ── The kind is not reachable from here ────────────────────────────────
// Not by a check in this function: ArtifactChange has no Kind field and
// the repository's UPDATE has no `kind` in its SET clause. Two layers
// would both have to grow one before a kind could move, which is what
// makes "immutable after creation" a property of the code rather than a
// rule somebody remembers.
func (s *Service) UpdateArtifact(ctx context.Context, workspaceID, id uuid.UUID, c domain.ArtifactChange) (*ArtifactUpdateResult, error) {
	if c.Empty() {
		return nil, domain.Invalid(
			"an update must change at least one of title, body, status, sensitivity or room")
	}

	a, err := s.artifacts.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	// Resolved BEFORE the change is applied, so an edit naming a room
	// this workspace does not have leaves the entity in memory untouched.
	if roomID, ok := c.Room.ID(); ok {
		if err := s.requireRoom(ctx, workspaceID, &roomID); err != nil {
			return nil, err
		}
	}

	changed := a.Apply(c)
	if err := a.Validate(); err != nil {
		return nil, err
	}

	if changed.Unchanged() {
		return &ArtifactUpdateResult{Artifact: a, ArtifactChangeResult: changed}, nil
	}

	if err := s.artifacts.Update(ctx, workspaceID, a); err != nil {
		return nil, err
	}
	return &ArtifactUpdateResult{Artifact: a, ArtifactChangeResult: changed}, nil
}

/* ── reference resolution ────────────────────────────────────────────── */

// requireArtifact confirms that this workspace has this artifact.
//
// The same rule, and the same single answer, as requireRoom: an artifact
// that exists in another workspace and one that never existed are one
// response, because any difference between them is a way to confirm the
// existence of somebody else's record.
func (s *Service) requireArtifact(ctx context.Context, workspaceID uuid.UUID, artifactID *uuid.UUID) error {
	if artifactID == nil {
		return nil
	}
	found, err := s.artifacts.Exists(ctx, workspaceID, *artifactID)
	if err != nil {
		return err
	}
	if !found {
		return domain.NotFound("artifact %s was not found", *artifactID)
	}
	return nil
}
