package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	sessionStart = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	sessionLater = time.Date(2026, 9, 13, 11, 30, 0, 0, time.UTC)
)

func openSession() *Session {
	return &Session{
		ID:             uuid.New(),
		WorkspaceID:    uuid.New(),
		Status:         SessionOpen,
		StartedAt:      sessionStart,
		LastActivityAt: sessionStart,
	}
}

func TestAnOpenSessionValidates(t *testing.T) {
	if err := openSession().Validate(); err != nil {
		t.Fatalf("a valid open session was refused: %v", err)
	}
}

func TestAStartedSessionFocusesOnNothing(t *testing.T) {
	// The first thing a person does is decide what they are working on, so
	// a session that demanded a room before it could start would be one
	// nobody opens.
	s := openSession()
	if s.ActiveRoomID != nil || s.ActiveArtifactID != nil {
		t.Error("a fresh session arrived already focused")
	}
	if !s.Open() {
		t.Error("a fresh session does not report open")
	}
}

func TestTheStatusAndTheClosingTimeMoveTogether(t *testing.T) {
	// Without this a row could be closed with no closed_at, and every "how
	// long did I work" answer would silently be nothing.
	s := openSession()
	s.ClosedAt = &sessionLater
	assertInvalid(t, s.Validate(), "open session cannot have a closing time")

	s = openSession()
	s.Status = SessionClosed
	assertInvalid(t, s.Validate(), "closed session must have a closing time")
}

func TestASessionNeedsItsTimestampsAndAWorkspace(t *testing.T) {
	s := openSession()
	s.WorkspaceID = uuid.Nil
	assertInvalid(t, s.Validate(), "workspace")

	s = openSession()
	s.StartedAt = time.Time{}
	assertInvalid(t, s.Validate(), "started_at")

	s = openSession()
	s.LastActivityAt = time.Time{}
	assertInvalid(t, s.Validate(), "last_activity_at")
}

func TestASessionSummaryIsBounded(t *testing.T) {
	// More than a paragraph about a stretch of work is a Memory, or an
	// Artifact, and both are better places for it.
	s := openSession()
	s.Summary = strings.Repeat("x", MaxSessionSummary+1)
	assertInvalid(t, s.Validate(), "summary")
}

func TestASessionRefusesAZeroFocus(t *testing.T) {
	zero := uuid.Nil

	s := openSession()
	s.ActiveRoomID = &zero
	assertInvalid(t, s.Validate(), "active_room_id")

	s = openSession()
	s.ActiveArtifactID = &zero
	assertInvalid(t, s.Validate(), "active_artifact_id")
}

/* ── focus ───────────────────────────────────────────────────────────── */

func TestAnEmptyFocusIsRecognisable(t *testing.T) {
	if !(SessionFocus{}).Empty() {
		t.Error("the zero SessionFocus does not report empty")
	}
	if (SessionFocus{Room: ClearRef()}).Empty() {
		t.Error("a focus that lets go of the room reports empty")
	}
}

func TestMovingTheFocusStampsActivity(t *testing.T) {
	s := openSession()
	room := uuid.New()

	res := s.Apply(SessionFocus{Room: SetRef(room)}, sessionLater)

	if !res.RoomChanged || s.ActiveRoomID == nil || *s.ActiveRoomID != room {
		t.Fatalf("the focus did not move: %v %+v", s.ActiveRoomID, res)
	}
	if !s.LastActivityAt.Equal(sessionLater) {
		t.Errorf("LastActivityAt = %v, want %v", s.LastActivityAt, sessionLater)
	}
}

func TestAFocusThatMovesNothingIsNotActivity(t *testing.T) {
	// Stamping a no-op would make it the most recent thing that happened,
	// which is the same dishonesty as an edit reporting success without
	// changing a field.
	s := openSession()
	room := uuid.New()
	s.ActiveRoomID = &room

	res := s.Apply(SessionFocus{Room: SetRef(room)}, sessionLater)

	if !res.Unchanged() {
		t.Errorf("re-focusing on the current room reported movement: %+v", res)
	}
	if !s.LastActivityAt.Equal(sessionStart) {
		t.Errorf("LastActivityAt moved to %v on a no-op", s.LastActivityAt)
	}
}

func TestLettingGoOfTheArtifactKeepsTheRoom(t *testing.T) {
	// "sai desse documento" is not "sai dessa sala", and a focus edit that
	// conflated them would drop context the person still wanted.
	s := openSession()
	room, artifact := uuid.New(), uuid.New()
	s.ActiveRoomID, s.ActiveArtifactID = &room, &artifact

	res := s.Apply(SessionFocus{Artifact: ClearRef()}, sessionLater)

	if !res.ArtifactChanged || s.ActiveArtifactID != nil {
		t.Fatalf("the artifact was not released: %v", s.ActiveArtifactID)
	}
	if res.RoomChanged || s.ActiveRoomID == nil || *s.ActiveRoomID != room {
		t.Error("releasing the artifact also moved the room")
	}
}

func TestWritingTheSummaryIsAFocusChangeToo(t *testing.T) {
	s := openSession()
	summary := "Fechei o desenho do Palace e comecei as migrations"

	res := s.Apply(SessionFocus{Summary: &summary}, sessionLater)

	if !res.SummaryChanged || s.Summary != summary {
		t.Fatalf("the summary was not written: %q %+v", s.Summary, res)
	}
}

/* ── closing ─────────────────────────────────────────────────────────── */

func TestClosingASessionStampsBothClocks(t *testing.T) {
	s := openSession()

	if !s.Close(sessionLater) {
		t.Fatal("closing an open session reported that it was already closed")
	}
	if s.Open() {
		t.Error("a closed session still reports open")
	}
	if s.ClosedAt == nil || !s.ClosedAt.Equal(sessionLater) {
		t.Errorf("ClosedAt = %v, want %v", s.ClosedAt, sessionLater)
	}
	if !s.LastActivityAt.Equal(sessionLater) {
		t.Errorf("LastActivityAt = %v, want %v", s.LastActivityAt, sessionLater)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("a session closed by Close() does not validate: %v", err)
	}
}

func TestClosingTwiceIsAResultAndNotAFailure(t *testing.T) {
	// The request has already been satisfied. Reporting a failure would
	// make a model apologise for a state that is correct, and retry.
	s := openSession()
	s.Close(sessionLater)
	firstClose := *s.ClosedAt

	evenLater := sessionLater.Add(time.Hour)
	if s.Close(evenLater) {
		t.Error("closing an already-closed session reported that it did the work")
	}
	if !s.ClosedAt.Equal(firstClose) {
		t.Errorf("the second close rewrote ClosedAt to %v", s.ClosedAt)
	}
}

/* ── the v1 restriction is a restriction, not a law ──────────────────── */

func TestNothingInTheDomainAssumesASingleSession(t *testing.T) {
	// One open session per workspace is enforced by a partial unique index
	// in the schema, and it is a v1 restriction: concurrent channels are a
	// real direction and would need independent sessions. Nothing here may
	// be built on the assumption, so two open sessions in one workspace
	// must be well formed as far as this package is concerned.
	ws := uuid.New()
	a, b := openSession(), openSession()
	a.WorkspaceID, b.WorkspaceID = ws, ws

	if err := a.Validate(); err != nil {
		t.Fatalf("the first session was refused: %v", err)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("the domain refused a second open session; the single-session "+
			"rule belongs to the schema, not here: %v", err)
	}
}

func TestSessionStatusParsesItsTwoWords(t *testing.T) {
	for _, raw := range []string{"open", " OPEN ", "closed"} {
		if _, err := ParseSessionStatus(raw); err != nil {
			t.Errorf("ParseSessionStatus(%q) = %v", raw, err)
		}
	}
	if _, err := ParseSessionStatus("paused"); err == nil {
		t.Error("`paused` was accepted; there are two statuses")
	}
}
