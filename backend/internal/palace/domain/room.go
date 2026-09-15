package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Rooms: the thematic areas of the palace.

const (
	// MaxRoomName bounds the handle. 200 is what every other short name
	// in this system carries.
	MaxRoomName = 200
	// MaxRoomDescription bounds the one paragraph a room may carry about
	// itself. Two thousand is a description, not a document; a room that
	// wants to say more is describing an artifact.
	MaxRoomDescription = 2000
)

// Room is one area of the palace: a context a person works inside.
//
// ── What a room is NOT ─────────────────────────────────────────────────
// It is not a folder tree, not a tag, and not a permission. It holds no
// state that evolves, which is exactly what an Artifact is for, and it
// grants nothing: a room being private does not make what is inside it
// private, because each thing inside carries its own level.
//
// ── Why a room carries a sensitivity of its own ────────────────────────
// Because a room's NAME can reveal the thing it contains before anything
// inside it is read. "Terapia", "Saída da empresa", "Diagnóstico" are
// each a disclosure on their own, and a listing of rooms that showed them
// would leak the fact without ever returning a single artifact.
type Room struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID

	Name        string
	Description string

	Status      Lifecycle
	Sensitivity Sensitivity

	CreatedAt time.Time
	UpdatedAt time.Time
}

// NormalizeName trims a name. It does not change case: the stored casing
// is what was written, and a name is a name.
func NormalizeName(raw string) string { return strings.TrimSpace(raw) }

// Validate checks a room that is about to be written.
//
// It validates SHAPE and not the story. The only things refused are
// values that would make the record unreadable, unowned or unbounded.
func (r *Room) Validate() error {
	if r.WorkspaceID == uuid.Nil {
		// A row with no workspace belongs to nobody, and every read in
		// this context filters on the workspace. It would be written and
		// never seen again.
		return Invalid("a workspace is required")
	}
	name := NormalizeName(r.Name)
	if name == "" {
		// A room nobody can name is one nobody can ask for again, and
		// asking for it again by name is most of what this context is for.
		return Invalid("a name is required")
	}
	if len([]rune(name)) > MaxRoomName {
		return Invalid("name is longer than %d characters", MaxRoomName)
	}
	if len([]rune(r.Description)) > MaxRoomDescription {
		return Invalid("description is longer than %d characters", MaxRoomDescription)
	}
	if !r.Status.Valid() {
		return Invalid("unknown status %s; the statuses are %s",
			quoteToken(string(r.Status)), strings.Join(LifecycleNames(), ", "))
	}
	if !r.Sensitivity.Valid() {
		return Invalid("unknown sensitivity %s; the levels are %s",
			quoteToken(string(r.Sensitivity)), strings.Join(SensitivityNames(), ", "))
	}
	return nil
}

/* ── the change ──────────────────────────────────────────────────────── */

// RoomChange is a partial edit of a room: each field is nil when the
// caller is not touching it.
//
// ── Why nil-means-unchanged, and not a full replacement ────────────────
// Because every caller that edits a room edits ONE aspect of it.
// "Arquiva a sala de carreira" moves the status and must not touch the
// description. A full-replacement contract would make the caller resend
// the fields it is not changing, and the day it resends a stale copy,
// because it read the room three turns ago, the edit silently reverts
// work.
type RoomChange struct {
	Name        *string
	Description *string
	Status      *Lifecycle
	Sensitivity *Sensitivity
}

// Empty reports whether this change would change nothing at all.
func (c RoomChange) Empty() bool {
	return c.Name == nil && c.Description == nil &&
		c.Status == nil && c.Sensitivity == nil
}

// RoomChangeResult is what a completed edit reports back.
//
// ── Why it reports rather than just doing ──────────────────────────────
// Because the caller that asked for the edit is the only party that can
// still see both sides of it, and a confirmation built from that is
// checkable: "sensitivity: normal → private" can be verified by a reader,
// "done" cannot.
//
// PreviousSensitivity is carried for the same reason PreviousStatus is,
// and it is the more important of the two: a change in how exposed
// something is deserves to be stated in the words of both ends.
type RoomChangeResult struct {
	PreviousStatus      Lifecycle
	PreviousSensitivity Sensitivity

	NameChanged        bool
	DescriptionChanged bool
	StatusChanged      bool
	SensitivityChanged bool
}

// Unchanged is true when every field the caller sent already held the
// value it sent. Saying so is what stops a model reporting work it did
// not do.
func (r RoomChangeResult) Unchanged() bool {
	return !r.NameChanged && !r.DescriptionChanged &&
		!r.StatusChanged && !r.SensitivityChanged
}

// Apply mutates the room and reports what actually moved.
func (r *Room) Apply(c RoomChange) RoomChangeResult {
	res := RoomChangeResult{
		PreviousStatus:      r.Status,
		PreviousSensitivity: r.Sensitivity,
	}
	if c.Name != nil && NormalizeName(*c.Name) != r.Name {
		r.Name = NormalizeName(*c.Name)
		res.NameChanged = true
	}
	if c.Description != nil && *c.Description != r.Description {
		r.Description = *c.Description
		res.DescriptionChanged = true
	}
	if c.Status != nil && *c.Status != r.Status {
		r.Status = *c.Status
		res.StatusChanged = true
	}
	if c.Sensitivity != nil && *c.Sensitivity != r.Sensitivity {
		r.Sensitivity = *c.Sensitivity
		res.SensitivityChanged = true
	}
	return res
}
