package app

// Reading surfaces: the rules the operator's own projections impose on
// top of the Core.
//
// ══════════════════════════════════════════════════════════════════════
//
//	VISUAL CONTAINMENT INHERITS VISIBILITY, NOT SENSITIVITY
//
// ══════════════════════════════════════════════════════════════════════
//
// If a Room cannot appear on a surface, what is contained in it cannot
// appear on that surface either. An artifact drawn inside a room the
// viewer is not allowed to see would announce that room by existing, and
// on a spatial projection it would do so with a picture.
//
// ── What this is NOT ───────────────────────────────────────────────────
// It is not a relabelling. `Artifact.sensitivity` is never written by any
// of this, and moving the same row into a visible room makes it appear
// again with no edit to the row at all. The rule is about what a
// PROJECTION may render, not about what something IS.
//
// It is also not a change to the Core. `FindByID` remains ungated, for
// the reason ports.Sensitive gives: a caller holding an id already knows
// the row exists. The tools are unaffected, and they prove it: they never
// call anything in this file, and `Visibility{}` is only restrictive when
// a caller opts in.
//
// ── Why these live in app and not in the HTTP handler ──────────────────
// Because the rule is CROSS-ENTITY. Whether an artifact may be shown
// depends on a room, and whether a memory may be shown depends on an
// artifact which in turn depends on a room. A handler doing that would be
// a second place that holds repositories, which is the arrangement this
// module exists without. `app` is where a rule that needs two
// repositories belongs, and it is already where every other one lives.
//
// ── Why the refusal is NotFound and not a new kind ─────────────────────
// Because the surface must not be able to distinguish "withheld from you"
// from "never existed". The messages below are byte-identical to the ones
// the repositories produce for a missing row, on purpose: a different
// wording, a different kind or a different code would each be a way to
// confirm that a row exists inside a room somebody is not being shown.
//
// ── What is deliberately NOT here yet ──────────────────────────────────
// The LISTING side of the same rule. A listing needs the predicate inside
// the SQL, because a filter applied after the fact cannot make `Count`
// agree with `List`, and a total that counts what the list withholds
// announces exactly what the withholding is for. That is a separate
// slice. Until it lands, no listing that would need it is mounted: see
// the HTTP adapter, which mounts only the listings it can answer
// correctly.

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// VisibleToSurface reports whether a level may be rendered by a surface
// under these rules.
//
// ── Why this is one exported function and not a comparison per handler ─
// Because it is the ONE place the level rule is decided for a projection,
// exactly as `Sensitivity.HiddenFromBroadListing` is the one place it is
// decided for a listing. Three spellings of `!= highly_sensitive`
// scattered across handlers is how one of them ends up missing the day a
// fourth level exists.
//
// It fails closed on a level it does not recognise: an unknown value is
// withheld rather than shown, which is the only direction that costs
// nothing when it is wrong.
func VisibleToSurface(level domain.Sensitivity, v ports.Visibility) bool {
	if !level.Valid() {
		return false
	}
	if v.IncludeHighlySensitive {
		return true
	}
	return !level.HiddenFromBroadListing()
}

/* ── the three reads a surface may perform by id ─────────────────────── */

// RoomForSurface returns one room if this surface may render it.
//
// Only the level rule applies: a room is not contained in anything, so
// `InheritRoomVisibility` has nothing to act on here and is ignored.
func (s *Service) RoomForSurface(ctx context.Context, workspaceID, id uuid.UUID, v ports.Visibility) (*domain.Room, error) {
	room, err := s.rooms.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !VisibleToSurface(room.Sensitivity, v) {
		return nil, roomNotFound(id)
	}
	return room, nil
}

// ArtifactForSurface returns one artifact if this surface may render it.
//
// Two rules, in this order: the artifact's own level, then the visibility
// of the room that contains it. An unfiled artifact is contained in
// nothing and passes the second by having nothing to fail.
func (s *Service) ArtifactForSurface(ctx context.Context, workspaceID, id uuid.UUID, v ports.Visibility) (*domain.Artifact, error) {
	artifact, err := s.artifacts.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !VisibleToSurface(artifact.Sensitivity, v) {
		return nil, artifactNotFound(id)
	}
	visible, err := s.roomVisible(ctx, workspaceID, artifact.RoomID, v)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, artifactNotFound(id)
	}
	return artifact, nil
}

// MemoryForSurface returns one memory if this surface may render it.
//
// ── Why the artifact is checked too, and not only the room ─────────────
// Because a memory can be filed in a VISIBLE room while being about an
// artifact that lives in a withheld one, and the domain allows exactly
// that: `room_id` and `artifact_id` are independent optional references.
// A memory whose summary describes a withheld artifact is the same
// disclosure as the artifact itself, with a different author. The check
// is therefore transitive, and it reuses the artifact rule rather than
// restating it.
func (s *Service) MemoryForSurface(ctx context.Context, workspaceID, id uuid.UUID, v ports.Visibility) (*domain.Memory, error) {
	memory, err := s.memories.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !VisibleToSurface(memory.Sensitivity, v) {
		return nil, memoryNotFound(id)
	}
	roomOK, err := s.roomVisible(ctx, workspaceID, memory.RoomID, v)
	if err != nil {
		return nil, err
	}
	if !roomOK {
		return nil, memoryNotFound(id)
	}
	artifactOK, err := s.artifactVisible(ctx, workspaceID, memory.ArtifactID, v)
	if err != nil {
		return nil, err
	}
	if !artifactOK {
		return nil, memoryNotFound(id)
	}
	return memory, nil
}

/* ── the containment checks ──────────────────────────────────────────── */

// roomVisible answers whether an optional room reference points at
// something this surface may render.
//
// A nil reference is VISIBLE: nothing contains the caller, so there is
// nothing to inherit. That is the unfiled case, and it is an ordinary
// state rather than a degraded one.
//
// A room that cannot be read at all is NOT visible. That covers a row
// this workspace does not have and a row that was soft-deleted, and it
// matches what the listing predicate will do when it arrives: it requires
// a room that is live AND showable, not merely one that was referenced.
func (s *Service) roomVisible(ctx context.Context, workspaceID uuid.UUID, roomID *uuid.UUID, v ports.Visibility) (bool, error) {
	if roomID == nil || !v.InheritRoomVisibility {
		return true, nil
	}
	room, err := s.rooms.FindByID(ctx, workspaceID, *roomID)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return VisibleToSurface(room.Sensitivity, v), nil
}

// artifactVisible answers the same question for an optional artifact
// reference, and it is the transitive half of the rule: an artifact is
// visible only if its own level passes AND its room does.
func (s *Service) artifactVisible(ctx context.Context, workspaceID uuid.UUID, artifactID *uuid.UUID, v ports.Visibility) (bool, error) {
	if artifactID == nil || !v.InheritRoomVisibility {
		return true, nil
	}
	artifact, err := s.artifacts.FindByID(ctx, workspaceID, *artifactID)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if !VisibleToSurface(artifact.Sensitivity, v) {
		return false, nil
	}
	return s.roomVisible(ctx, workspaceID, artifact.RoomID, v)
}

/* ── the refusals ────────────────────────────────────────────────────── */

// The three messages below are the repositories' own, repeated verbatim.
//
// That duplication is the point and must survive a refactor: a surface
// refusal has to be indistinguishable from a missing row, and the moment
// one of these gains a word the other does not, the difference is a probe
// for the existence of content inside a withheld room.

func roomNotFound(id uuid.UUID) error { return domain.NotFound("room %s was not found", id) }

func artifactNotFound(id uuid.UUID) error {
	return domain.NotFound("artifact %s was not found", id)
}

func memoryNotFound(id uuid.UUID) error { return domain.NotFound("memory %s was not found", id) }
