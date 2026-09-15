package domain

import (
	"time"

	"github.com/google/uuid"
)

// Optional edits of nullable fields.
//
// ── The problem these two types exist to make unrepresentable ──────────
// A partial edit of a nullable field has THREE states, not two:
//
//	untouched   the caller did not mention it; keep what is stored
//	set         point it at this value
//	cleared     detach it; the stored value becomes NULL
//
// A `*uuid.UUID` expresses two of them. The usual fixes are both bad. A
// `**uuid.UUID` is correct and reads like a puzzle at every call site. A
// pointer plus a `DetachRoom bool` beside it is readable and admits a
// fourth, meaningless state (both set) which somebody then has to
// remember to reject, in a check that is easy to write once and easy to
// forget when the next nullable field arrives.
//
// So the three states are a type with unexported fields and three
// constructors. The zero value is `untouched`, which is what a field
// nobody mentioned should mean, and the contradictory state cannot be
// built.
//
// ── Why this is not a speculative abstraction ──────────────────────────
// Six fields in this package need it today: an artifact's room, a
// memory's room, a memory's artifact, a memory's occurred_at, and a
// session's active room and active artifact.

/* ── a nullable reference ────────────────────────────────────────────── */

// OptionalRef is a partial edit of a nullable entity reference.
type OptionalRef struct {
	touched bool
	clear   bool
	id      uuid.UUID
}

// KeepRef leaves the stored reference alone. It is the zero value, so a
// Change built without mentioning the field already means this.
func KeepRef() OptionalRef { return OptionalRef{} }

// SetRef points the reference at id.
func SetRef(id uuid.UUID) OptionalRef {
	return OptionalRef{touched: true, id: id}
}

// ClearRef detaches the reference.
func ClearRef() OptionalRef {
	return OptionalRef{touched: true, clear: true}
}

// Touched reports whether the caller said anything about this field.
func (o OptionalRef) Touched() bool { return o.touched }

// Cleared reports whether the caller asked to detach.
func (o OptionalRef) Cleared() bool { return o.touched && o.clear }

// ID returns the requested target, and whether one was requested. It is
// what an application service calls to know which row it must resolve in
// the caller's workspace before the edit is allowed to proceed.
func (o OptionalRef) ID() (uuid.UUID, bool) {
	if !o.touched || o.clear {
		return uuid.Nil, false
	}
	return o.id, true
}

// Resolve applies this edit to the stored value and reports whether
// anything moved.
//
// Comparing before assigning is what makes "this edit changed nothing" a
// fact the caller can report, rather than a claim it has to make on
// faith.
func (o OptionalRef) Resolve(current *uuid.UUID) (*uuid.UUID, bool) {
	if !o.touched {
		return current, false
	}
	if o.clear {
		return nil, current != nil
	}
	if current != nil && *current == o.id {
		return current, false
	}
	next := o.id
	return &next, true
}

/* ── a nullable instant ──────────────────────────────────────────────── */

// OptionalTime is a partial edit of a nullable timestamp, with the same
// three states and the same reasoning as OptionalRef.
type OptionalTime struct {
	touched bool
	clear   bool
	at      time.Time
}

// KeepTime leaves the stored instant alone. It is the zero value.
func KeepTime() OptionalTime { return OptionalTime{} }

// SetTime points the instant at t.
func SetTime(t time.Time) OptionalTime {
	return OptionalTime{touched: true, at: t}
}

// ClearTime removes the instant.
func ClearTime() OptionalTime {
	return OptionalTime{touched: true, clear: true}
}

func (o OptionalTime) Touched() bool { return o.touched }

func (o OptionalTime) Cleared() bool { return o.touched && o.clear }

// At returns the requested instant, and whether one was requested.
func (o OptionalTime) At() (time.Time, bool) {
	if !o.touched || o.clear {
		return time.Time{}, false
	}
	return o.at, true
}

// Resolve applies this edit to the stored value and reports whether
// anything moved.
//
// Equality is `Time.Equal` rather than `==`: two instants that name the
// same moment in different locations are the same instant, and `==`
// compares the monotonic reading and the location too. Using it would
// report a change every time a value made a round trip through Postgres,
// which stamps everything UTC.
func (o OptionalTime) Resolve(current *time.Time) (*time.Time, bool) {
	if !o.touched {
		return current, false
	}
	if o.clear {
		return nil, current != nil
	}
	if current != nil && current.Equal(o.at) {
		return current, false
	}
	next := o.at
	return &next, true
}
