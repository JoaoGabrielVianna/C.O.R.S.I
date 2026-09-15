package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sessions: the short-term working context.
//
// ══════════════════════════════════════════════════════════════════════
//
//	ONE OPEN SESSION PER WORKSPACE IS A v1 RESTRICTION, NOT A LAW
//
// ══════════════════════════════════════════════════════════════════════
//
// The schema enforces it with a partial unique index, and that is what
// makes "the current working context" a question with one answer: there
// is no tie to break, so `palace.session.get` is deterministic. It is the
// right answer for v1, where the only way in is a conversation with an
// agent through one interface.
//
// It is explicitly NOT a permanent property of the concept. Concurrent
// channels are a real direction, Web and Telegram at the same time being
// the obvious pair, and they need independent sessions: two channels
// sharing one focus would have each one silently redirecting the other.
// Relaxing this means dropping the index and giving a Session a channel
// identity. Both are additive, neither is designed here, and nothing in
// this package or above it may be built on "there is exactly one session"
// as though it were a truth about the domain.
//
// ── What a Session is NOT ──────────────────────────────────────────────
// It is not a conversation, and it does not hold history. What was said
// lives in the conversation that said it; what was learned lives in a
// Memory. A session holds only what is in FOCUS and, once it is over, one
// paragraph about what the stretch was for. If deleting a session would
// lose information, something was stored in the wrong place.

/* ── the status ──────────────────────────────────────────────────────── */

// SessionStatus is whether this working stretch is still going.
type SessionStatus string

const (
	SessionOpen   SessionStatus = "open"
	SessionClosed SessionStatus = "closed"
)

var SessionStatuses = []SessionStatus{SessionOpen, SessionClosed}

func (s SessionStatus) Valid() bool {
	return s == SessionOpen || s == SessionClosed
}

func (s SessionStatus) String() string { return string(s) }

func SessionStatusNames() []string { return names(SessionStatuses) }

func ParseSessionStatus(raw string) (SessionStatus, error) {
	s := SessionStatus(fold(raw))
	if !s.Valid() {
		return "", Invalid("unknown session status %s; the statuses are %s",
			quoteToken(raw), strings.Join(SessionStatusNames(), ", "))
	}
	return s, nil
}

/* ── the entity ──────────────────────────────────────────────────────── */

// MaxSessionSummary bounds the paragraph a closed session carries. Two
// thousand characters is an account of a working stretch; more than that
// is a Memory, or an Artifact, and both are better places for it.
const MaxSessionSummary = 2000

// Session is one stretch of work and what it is pointed at.
type Session struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID

	Status SessionStatus

	// What the session is pointed at right now. Both optional: a session
	// can be open with nothing focused, which is exactly what starting one
	// produces, because the first thing a person does is decide what they
	// are working on.
	ActiveRoomID     *uuid.UUID
	ActiveArtifactID *uuid.UUID

	// Summary is what this stretch was about. Optional, and usually
	// written at the end.
	Summary string

	StartedAt time.Time
	// LastActivityAt is what "still going" is measured from. Stamped by
	// whoever applies a change, from the clock the database keeps, never
	// from a process clock.
	LastActivityAt time.Time
	ClosedAt       *time.Time
}

// Open is a named question rather than a comparison at each call site.
func (s *Session) Open() bool { return s.Status == SessionOpen }

func (s *Session) Validate() error {
	if s.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if !s.Status.Valid() {
		return Invalid("unknown session status %s; the statuses are %s",
			quoteToken(string(s.Status)), strings.Join(SessionStatusNames(), ", "))
	}
	if len([]rune(s.Summary)) > MaxSessionSummary {
		return Invalid("summary is longer than %d characters", MaxSessionSummary)
	}
	if s.StartedAt.IsZero() {
		return Invalid("started_at is required")
	}
	if s.LastActivityAt.IsZero() {
		return Invalid("last_activity_at is required")
	}
	// The status and the timestamp move together or not at all, mirroring
	// the CHECK. Without it a row could be closed with no closed_at, and
	// every "how long did I work" answer would silently be nothing.
	if s.Status == SessionOpen && s.ClosedAt != nil {
		return Invalid("an open session cannot have a closing time")
	}
	if s.Status == SessionClosed && s.ClosedAt == nil {
		return Invalid("a closed session must have a closing time")
	}
	if s.ActiveRoomID != nil && *s.ActiveRoomID == uuid.Nil {
		return Invalid("active_room_id is present but empty; omit it instead of sending an empty id")
	}
	if s.ActiveArtifactID != nil && *s.ActiveArtifactID == uuid.Nil {
		return Invalid("active_artifact_id is present but empty; omit it instead of sending an empty id")
	}
	return nil
}

/* ── the focus change ────────────────────────────────────────────────── */

// SessionFocus is a partial edit of what a session is pointed at.
//
// The two references use OptionalRef because "stop working on that
// artifact" and "do not touch the artifact" are different requests, and a
// plain pointer can only express one of them.
type SessionFocus struct {
	Room     OptionalRef
	Artifact OptionalRef
	Summary  *string
}

func (f SessionFocus) Empty() bool {
	return !f.Room.Touched() && !f.Artifact.Touched() && f.Summary == nil
}

type SessionFocusResult struct {
	RoomChanged     bool
	ArtifactChanged bool
	SummaryChanged  bool
}

func (r SessionFocusResult) Unchanged() bool {
	return !r.RoomChanged && !r.ArtifactChanged && !r.SummaryChanged
}

// Apply moves the focus and reports what actually moved.
//
// ── Why the clock is a parameter ───────────────────────────────────────
// Because `last_activity_at` is what every "what am I working on"
// question is answered from, and this package must not be the second
// authority on what time it is. The caller passes the instant it read
// from the database, exactly as Finance requires of anything that stamps
// a moment.
//
// ── Why an unchanged focus does not touch the clock ────────────────────
// Because a request that moved nothing is not activity. Stamping it would
// make a no-op the most recent thing that happened, which is the same
// dishonesty as an edit that reports success without changing a field.
func (s *Session) Apply(f SessionFocus, now time.Time) SessionFocusResult {
	var res SessionFocusResult
	s.ActiveRoomID, res.RoomChanged = f.Room.Resolve(s.ActiveRoomID)
	s.ActiveArtifactID, res.ArtifactChanged = f.Artifact.Resolve(s.ActiveArtifactID)
	if f.Summary != nil && *f.Summary != s.Summary {
		s.Summary = *f.Summary
		res.SummaryChanged = true
	}
	if !res.Unchanged() {
		s.LastActivityAt = now
	}
	return res
}

// Close ends the session and reports whether it was open to begin with.
//
// ── Why closing twice is a result and not an error ─────────────────────
// Because the request has already been satisfied: the session is closed,
// which is what the caller wanted. Reporting a failure would make a model
// apologise for a state that is correct, and worse, retry. Returning
// false lets the caller say "it was already closed", which is the true
// sentence and a different one from "I closed it".
func (s *Session) Close(now time.Time) bool {
	if !s.Open() {
		return false
	}
	s.Status = SessionClosed
	s.LastActivityAt = now
	closed := now
	s.ClosedAt = &closed
	return true
}
