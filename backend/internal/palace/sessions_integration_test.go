//go:build integration

// Palace, slice S5: the working context.
//
// The harness lives in palace_integration_test.go; this file is the same
// package and uses it.
package palace

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

func (e *env) startSession(ws uuid.UUID) *domain.Session {
	e.t.Helper()
	res, err := e.svc.StartSession(e.ctx(), ws)
	if err != nil {
		e.t.Fatalf("start session: %v", err)
	}
	return res.Session
}

// openSessionRows counts the open sessions a workspace has, read straight
// from the table so the assertion does not depend on the code under test.
func (e *env) openSessionRows(ws uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx(), `
		SELECT count(*) FROM palace.sessions
		WHERE workspace_id = $1 AND status = 'open'`, ws).Scan(&n); err != nil {
		e.t.Fatalf("count open sessions: %v", err)
	}
	return n
}

/* ══════════════════════════════════════════════════════════════════════
   Start
   ══════════════════════════════════════════════════════════════════════ */

func TestStartingOpensASessionWhenThereIsNone(t *testing.T) {
	e := newEnv(t)

	res, err := e.svc.StartSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.AlreadyOpen {
		t.Error("the first start reported that one was already open")
	}
	if !res.Session.Open() {
		t.Errorf("the new session is %q", res.Session.Status)
	}
	if res.Session.ClosedAt != nil {
		t.Error("a fresh session carries a closing time")
	}
	// A session starts focused on nothing: the first thing a person does
	// is decide what they are working on.
	if res.Session.ActiveRoomID != nil || res.Session.ActiveArtifactID != nil {
		t.Error("a fresh session arrived already focused")
	}
	if res.Session.Summary != "" {
		t.Errorf("a fresh session carries a summary: %q", res.Session.Summary)
	}
	if !res.Session.StartedAt.Equal(res.Session.LastActivityAt) {
		t.Error("a fresh session's two clocks disagree")
	}
	if n := e.openSessionRows(e.mine); n != 1 {
		t.Errorf("%d open sessions in the table, want 1", n)
	}
}

func TestASecondStartFindsTheSameSessionAndDoesNotCountAsActivity(t *testing.T) {
	// Discovering that a session exists is not working in it. Moving the
	// clock here would make "quando eu mexi nisso pela última vez" answer
	// with the moment somebody asked rather than the moment something
	// changed.
	e := newEnv(t)
	first := e.startSession(e.mine)

	second, err := e.svc.StartSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if !second.AlreadyOpen {
		t.Error("the second start reported that it created a session")
	}
	if second.Session.ID != first.ID {
		t.Errorf("the second start returned a different session: %s vs %s",
			second.Session.ID, first.ID)
	}
	if !second.Session.LastActivityAt.Equal(first.LastActivityAt) {
		t.Errorf("an idempotent start moved last_activity_at: %v to %v",
			first.LastActivityAt, second.Session.LastActivityAt)
	}
	if n := e.openSessionRows(e.mine); n != 1 {
		t.Errorf("%d open sessions in the table, want 1", n)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE RACE
//
// ══════════════════════════════════════════════════════════════════════
//
// Callers starting at the same instant. The partial unique index means
// exactly one row can be open, so all but one must lose, and what they
// are told is the whole question: an answer, or a 23505 leaking a
// concurrency detail into a use case that has a perfectly good response.
func TestConcurrentStartsProduceOneSessionAndNoUniqueViolation(t *testing.T) {
	e := newEnv(t)

	const callers = 8
	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		mu      sync.Mutex
		ids     = map[uuid.UUID]int{}
		creates int
		errs    []error
	)
	start.Add(1)
	done.Add(callers)

	for i := 0; i < callers; i++ {
		go func() {
			defer done.Done()
			// Every goroutine waits on the same barrier, so they hit
			// Postgres together rather than in whatever order the
			// scheduler happened to start them.
			start.Wait()

			res, err := e.svc.StartSession(e.ctx(), e.mine)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids[res.Session.ID]++
			if !res.AlreadyOpen {
				creates++
			}
		}()
	}
	start.Done()
	done.Wait()

	for _, err := range errs {
		t.Errorf("a concurrent start failed: %v", err)
		if strings.Contains(err.Error(), "23505") ||
			strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			t.Error("a unique violation reached the caller")
		}
	}
	if len(ids) != 1 {
		t.Errorf("%d distinct sessions were handed out, want 1: %v", len(ids), ids)
	}
	if creates != 1 {
		t.Errorf("%d callers were told they created the session, want exactly 1", creates)
	}
	if n := e.openSessionRows(e.mine); n != 1 {
		t.Errorf("%d open sessions in the table, want 1", n)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE INDEX IS THE GUARANTEE, NOT THE SERVICE
//
// ══════════════════════════════════════════════════════════════════════
//
// Everything above goes through the application layer. This goes around
// it, so the assertion is about the database: a second open session
// cannot exist even if some future caller reaches the table directly.
func TestPostgresItselfRefusesASecondOpenSession(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)

	_, err := e.pool.Exec(e.ctx(),
		`INSERT INTO palace.sessions (workspace_id) VALUES ($1)`, e.mine)
	if err == nil {
		t.Fatal("Postgres accepted a second open session")
	}
	if !strings.Contains(err.Error(), "sessions_one_open_idx") {
		t.Errorf("it was refused for the wrong reason: %v", err)
	}

	// Another workspace is unaffected: the index is per workspace.
	if _, err := e.pool.Exec(e.ctx(),
		`INSERT INTO palace.sessions (workspace_id) VALUES ($1)`, e.theirs); err != nil {
		t.Errorf("a neighbour could not open their own session: %v", err)
	}
}

func TestEachWorkspaceHasItsOwnSession(t *testing.T) {
	e := newEnv(t)
	mine := e.startSession(e.mine)
	theirs := e.startSession(e.theirs)

	if mine.ID == theirs.ID {
		t.Fatal("two workspaces were handed the same session")
	}

	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != mine.ID {
		t.Error("the read returned another workspace's session")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Get
   ══════════════════════════════════════════════════════════════════════ */

func TestGetReturnsNotFoundWhenNothingIsOpen(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	assertNotFound(t, "reading with no session open", err)
}

func TestANeighboursSessionIsUnreachable(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.theirs)

	// No session of mine exists, and theirs must not stand in for it.
	_, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	assertNotFound(t, "reading a neighbour's session", err)

	// Nor can it be focused or closed from here.
	_, err = e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Summary: strPtr("invadindo")})
	assertNotFound(t, "focusing a neighbour's session", err)

	res, err := e.svc.CloseSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.Closed {
		t.Error("a neighbour's session was closed from another workspace")
	}

	// Theirs is untouched.
	still, err := e.svc.GetOpenSession(e.ctx(), e.theirs)
	if err != nil {
		t.Fatalf("their session: %v", err)
	}
	if !still.Open() || still.Summary != "" {
		t.Error("the neighbour's session was modified")
	}
}

func TestReadingDoesNotCountAsActivity(t *testing.T) {
	e := newEnv(t)
	session := e.startSession(e.mine)

	for i := 0; i < 3; i++ {
		if _, err := e.svc.GetOpenSession(e.ctx(), e.mine); err != nil {
			t.Fatalf("get: %v", err)
		}
	}

	after, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !after.LastActivityAt.Equal(session.LastActivityAt) {
		t.Errorf("reading moved last_activity_at: %v to %v",
			session.LastActivityAt, after.LastActivityAt)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Focus
   ══════════════════════════════════════════════════════════════════════ */

func TestFocusResolvesTheRoomAndTheArtifactInTheWorkspace(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactProject, "Palace", domain.SensitivityNormal, nil)

	res, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{
		Room:     domain.SetRef(room.ID),
		Artifact: domain.SetRef(artifact.ID),
	})
	if err != nil {
		t.Fatalf("focus: %v", err)
	}
	if !res.RoomChanged || !res.ArtifactChanged {
		t.Errorf("the focus was misreported: %+v", res.SessionFocusResult)
	}

	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActiveRoomID == nil || *got.ActiveRoomID != room.ID {
		t.Errorf("active room = %v, want %s", got.ActiveRoomID, room.ID)
	}
	if got.ActiveArtifactID == nil || *got.ActiveArtifactID != artifact.ID {
		t.Errorf("active artifact = %v, want %s", got.ActiveArtifactID, artifact.ID)
	}
}

func TestFocusRefusesANeighboursRoomOrArtifact(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)
	theirRoom := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	theirArtifact := e.artifact(e.theirs, domain.ArtifactNote, "a nota deles", domain.SensitivityNormal, nil)
	fabricated := uuid.New()

	_, errRoom := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Room: domain.SetRef(theirRoom.ID)})
	_, errFakeRoom := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Room: domain.SetRef(fabricated)})
	assertNotFound(t, "focusing a neighbour's room", errRoom)
	assertNotFound(t, "focusing a fabricated room", errFakeRoom)
	assertSameAnswer(t, "room", errRoom, errFakeRoom, theirRoom.ID, fabricated)

	_, errArtifact := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Artifact: domain.SetRef(theirArtifact.ID)})
	_, errFakeArtifact := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Artifact: domain.SetRef(fabricated)})
	assertNotFound(t, "focusing a neighbour's artifact", errArtifact)
	assertNotFound(t, "focusing a fabricated artifact", errFakeArtifact)
	assertSameAnswer(t, "artifact", errArtifact, errFakeArtifact, theirArtifact.ID, fabricated)

	// And the session was left alone by all four refusals.
	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActiveRoomID != nil || got.ActiveArtifactID != nil {
		t.Error("a refused focus changed the session anyway")
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	SETTING THE ARTIFACT DOES NOT SET THE ROOM
//
// ══════════════════════════════════════════════════════════════════════
//
// A session records where somebody is WORKING, not what owns what. A
// person can be inside an artifact while thinking about a different
// area, and a system that quietly moved the room under them would be
// answering a question they did not ask.
func TestFocusingAnArtifactDoesNotPropagateItsRoom(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)

	career := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	health := e.room(e.mine, "Saúde", domain.SensitivityNormal)
	// The artifact is filed in Carreira, which is exactly the fact that a
	// propagating implementation would use.
	artifact := e.artifact(e.mine, domain.ArtifactProject, "Palace", domain.SensitivityNormal, &career.ID)

	// No room focused at all: the artifact must not supply one.
	if _, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Artifact: domain.SetRef(artifact.ID)}); err != nil {
		t.Fatalf("focus: %v", err)
	}
	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActiveRoomID != nil {
		t.Errorf("focusing an artifact set the room to %v", got.ActiveRoomID)
	}

	// A DIFFERENT room focused: the artifact must not overwrite it.
	if _, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Room: domain.SetRef(health.ID)}); err != nil {
		t.Fatalf("focus room: %v", err)
	}
	if _, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Artifact: domain.SetRef(artifact.ID)}); err != nil {
		t.Fatalf("refocus artifact: %v", err)
	}
	got, err = e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActiveRoomID == nil || *got.ActiveRoomID != health.ID {
		t.Errorf("the artifact's room overwrote the focused one: %v", got.ActiveRoomID)
	}
}

func TestSetClearAndUnchangedMoveIndependently(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactProject, "Palace", domain.SensitivityNormal, nil)

	if _, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{
		Room: domain.SetRef(room.ID), Artifact: domain.SetRef(artifact.ID),
	}); err != nil {
		t.Fatalf("focus: %v", err)
	}

	// Clear the artifact, say nothing about the room.
	res, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Artifact: domain.ClearRef()})
	if err != nil {
		t.Fatalf("clear artifact: %v", err)
	}
	if !res.ArtifactChanged || res.RoomChanged {
		t.Errorf("clearing the artifact reported the wrong fields: %+v", res.SessionFocusResult)
	}

	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActiveArtifactID != nil {
		t.Errorf("the artifact was not cleared: %v", got.ActiveArtifactID)
	}
	if got.ActiveRoomID == nil || *got.ActiveRoomID != room.ID {
		t.Errorf("an untouched room moved: %v", got.ActiveRoomID)
	}

	// Clearing something already empty is not a change.
	res, err = e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Artifact: domain.ClearRef()})
	if err != nil {
		t.Fatalf("clear again: %v", err)
	}
	if !res.Unchanged() {
		t.Errorf("clearing an already-empty artifact reported a change: %+v", res.SessionFocusResult)
	}
}

func TestAnEmptyFocusIsRefused(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)

	_, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{})
	assertInvalid(t, "an empty focus", err)
}

func TestFocusingWithNoOpenSessionIsNotFound(t *testing.T) {
	e := newEnv(t)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)

	_, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Room: domain.SetRef(room.ID)})
	assertNotFound(t, "focusing with nothing open", err)
}

func TestFocusAllowsAnArchivedRoomOrArtifact(t *testing.T) {
	// Deliberately unlike a Relation endpoint. A relation is an assertion
	// about two things, and drawing a new one to something retired is
	// either a mistake or a sign it should be active again. Focus is
	// where the operator is LOOKING, and looking at something they
	// archived is ordinary: it is how they decide to bring it back.
	e := newEnv(t)
	e.startSession(e.mine)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactNote, "uma nota", domain.SensitivityNormal, nil)

	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateRoom(e.ctx(), e.mine, room.ID,
		domain.RoomChange{Status: &archived}); err != nil {
		t.Fatalf("archive room: %v", err)
	}
	if _, err := e.svc.UpdateArtifact(e.ctx(), e.mine, artifact.ID,
		domain.ArtifactChange{Status: &archived}); err != nil {
		t.Fatalf("archive artifact: %v", err)
	}

	if _, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{
		Room: domain.SetRef(room.ID), Artifact: domain.SetRef(artifact.ID),
	}); err != nil {
		t.Fatalf("focusing archived entities was refused: %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   The activity clock
   ══════════════════════════════════════════════════════════════════════ */

func TestARealChangeAdvancesTheActivityClock(t *testing.T) {
	e := newEnv(t)
	session := e.startSession(e.mine)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)

	res, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Room: domain.SetRef(room.ID)})
	if err != nil {
		t.Fatalf("focus: %v", err)
	}
	if !res.Session.LastActivityAt.After(session.LastActivityAt) {
		t.Errorf("last_activity_at did not advance: %v to %v",
			session.LastActivityAt, res.Session.LastActivityAt)
	}

	// And it is the DATABASE's clock, not this process's: the value sits
	// inside a window read from the same authority.
	now, err := e.svc.Now(e.ctx())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	if res.Session.LastActivityAt.After(now) {
		t.Errorf("last_activity_at %v is ahead of the database clock %v",
			res.Session.LastActivityAt, now)
	}

	// The stored row agrees with what was reported.
	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.LastActivityAt.Equal(res.Session.LastActivityAt) {
		t.Errorf("the row says %v, the result said %v",
			got.LastActivityAt, res.Session.LastActivityAt)
	}
}

func TestAFocusThatMovesNothingDoesNotAdvanceTheClock(t *testing.T) {
	// A no-op that stamped activity would be the session announcing work
	// that did not happen.
	e := newEnv(t)
	e.startSession(e.mine)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)

	first, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Room: domain.SetRef(room.ID)})
	if err != nil {
		t.Fatalf("focus: %v", err)
	}
	settled := first.Session.LastActivityAt

	// Re-focusing on the same room, and re-sending the same summary, are
	// both requests that move nothing.
	for name, focus := range map[string]domain.SessionFocus{
		"same room":    {Room: domain.SetRef(room.ID)},
		"same summary": {Summary: strPtr("")},
	} {
		res, err := e.svc.FocusSession(e.ctx(), e.mine, focus)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !res.Unchanged() {
			t.Errorf("%s reported a change: %+v", name, res.SessionFocusResult)
		}
		if !res.Session.LastActivityAt.Equal(settled) {
			t.Errorf("%s advanced last_activity_at: %v to %v",
				name, settled, res.Session.LastActivityAt)
		}
	}

	// And the row was never written either.
	got, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.LastActivityAt.Equal(settled) {
		t.Errorf("the stored row moved on a no-op: %v to %v", settled, got.LastActivityAt)
	}
}

func TestWritingTheSummaryIsActivity(t *testing.T) {
	e := newEnv(t)
	session := e.startSession(e.mine)

	res, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Summary: strPtr("fechando o desenho do Palace")})
	if err != nil {
		t.Fatalf("focus: %v", err)
	}
	if !res.SummaryChanged {
		t.Error("the summary change went unreported")
	}
	if !res.Session.LastActivityAt.After(session.LastActivityAt) {
		t.Error("writing the summary did not advance the clock")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Close
   ══════════════════════════════════════════════════════════════════════ */

func TestClosingEndsTheSessionAndStampsTheClosingTime(t *testing.T) {
	e := newEnv(t)
	session := e.startSession(e.mine)

	res, err := e.svc.CloseSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !res.Closed {
		t.Fatalf("closing an open session reported %q", res.Reason)
	}
	if res.Session == nil || res.Session.ID != session.ID {
		t.Fatal("close returned a different session")
	}
	if res.Session.Open() {
		t.Errorf("the closed session still reports open: %q", res.Session.Status)
	}
	if res.Session.ClosedAt == nil {
		t.Fatal("closed_at was not stamped")
	}
	if !res.Session.LastActivityAt.Equal(*res.Session.ClosedAt) {
		t.Error("closing stamped the two clocks with different instants")
	}

	// From the database's clock, not this process's.
	now, err := e.svc.Now(e.ctx())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	if res.Session.ClosedAt.After(now) {
		t.Errorf("closed_at %v is ahead of the database clock %v", res.Session.ClosedAt, now)
	}

	if n := e.openSessionRows(e.mine); n != 0 {
		t.Errorf("%d open sessions after closing, want 0", n)
	}
	_, err = e.svc.GetOpenSession(e.ctx(), e.mine)
	assertNotFound(t, "reading after closing", err)
}

func TestClosingTwiceIsAnAnswerAndNotAFailure(t *testing.T) {
	// The caller wanted no open session, and there is none. Reporting a
	// failure would make a model apologise for a state that is correct,
	// and retry.
	e := newEnv(t)
	e.startSession(e.mine)

	if _, err := e.svc.CloseSession(e.ctx(), e.mine); err != nil {
		t.Fatalf("first close: %v", err)
	}

	res, err := e.svc.CloseSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("the second close failed technically: %v", err)
	}
	if res.Closed {
		t.Error("the second close reported that it closed something")
	}
	if res.Reason != app.CloseReasonNoOpenSession {
		t.Errorf("reason = %q, want %q", res.Reason, app.CloseReasonNoOpenSession)
	}
	if res.Session != nil {
		// No "most recent session" fallback: picking the last closed one
		// and re-closing it would move a timestamp on a record of work
		// that finished hours ago, on the strength of a guess.
		t.Error("the second close went looking for a session to name")
	}
}

func TestClosingWithNothingEverOpenedIsTheSameAnswer(t *testing.T) {
	e := newEnv(t)

	res, err := e.svc.CloseSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.Closed || res.Reason != app.CloseReasonNoOpenSession {
		t.Errorf("closed=%v reason=%q, want false and %q",
			res.Closed, res.Reason, app.CloseReasonNoOpenSession)
	}
}

func TestTheSecondCloseDoesNotRewriteTheFirstOne(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)

	first, err := e.svc.CloseSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	closedAt := *first.Session.ClosedAt

	if _, err := e.svc.CloseSession(e.ctx(), e.mine); err != nil {
		t.Fatalf("second close: %v", err)
	}

	// Read straight from the table: the second call must not have moved
	// the timestamp on a record of work that already finished.
	var storedClosedAt time.Time
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT closed_at FROM palace.sessions WHERE id = $1`,
		first.Session.ID).Scan(&storedClosedAt); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !storedClosedAt.Equal(closedAt) {
		t.Errorf("closed_at was rewritten: %v, want %v", storedClosedAt, closedAt)
	}
}

func TestStartingAfterClosingOpensANewSession(t *testing.T) {
	e := newEnv(t)
	first := e.startSession(e.mine)
	if _, err := e.svc.CloseSession(e.ctx(), e.mine); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := e.svc.StartSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.AlreadyOpen {
		t.Error("starting after a close reported that one was already open")
	}
	if res.Session.ID == first.ID {
		t.Error("starting after a close reopened the closed session")
	}
	if n := e.openSessionRows(e.mine); n != 1 {
		t.Errorf("%d open sessions, want 1", n)
	}

	// The closed one is still on the record. Closing is not deleting.
	var rows int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.sessions WHERE workspace_id = $1`, e.mine).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 {
		t.Errorf("%d sessions on record, want 2", rows)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	CLOSING CONSOLIDATES NOTHING
//
// ══════════════════════════════════════════════════════════════════════
//
// Deciding that something said during a stretch of work deserves to
// become durable knowledge is a judgement. A system that made it
// automatically at the end of every session would fill the operator's
// palace with things they never chose to keep, each one indistinguishable
// afterwards from the ones they did.
func TestClosingCreatesNoMemorySourceArtifactOrRelation(t *testing.T) {
	e := newEnv(t)

	// A populated workspace, so "nothing changed" is a real claim rather
	// than a count of zero against zero.
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactProject, "Palace", domain.SensitivityNormal, &room.ID)
	memory := e.memory(e.mine, "uma decisão", domain.SensitivityNormal, &room.ID)
	source := e.source(e.mine, "uma transcrição", domain.SensitivityNormal)
	if _, err := e.svc.LinkSource(e.ctx(), e.mine, memory.ID, source.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	e.relate(e.mine, domain.EntityMemory, memory.ID,
		domain.RelationRelatedTo, domain.EntityArtifact, artifact.ID)

	e.startSession(e.mine)
	if _, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{
		Room:     domain.SetRef(room.ID),
		Artifact: domain.SetRef(artifact.ID),
		Summary:  strPtr("revisei o desenho e decidi manter a tabela"),
	}); err != nil {
		t.Fatalf("focus: %v", err)
	}

	before := e.countEverything()
	if _, err := e.svc.CloseSession(e.ctx(), e.mine); err != nil {
		t.Fatalf("close: %v", err)
	}
	after := e.countEverything()

	for table, n := range before {
		if after[table] != n {
			t.Errorf("closing changed %s: %d rows before, %d after", table, n, after[table])
		}
	}

	// And nothing was EDITED either: the summary did not migrate into the
	// memory, and no timestamp moved.
	gotMemory, err := e.svc.GetMemory(e.ctx(), e.mine, memory.ID)
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	if gotMemory.Content != "uma decisão" || !gotMemory.UpdatedAt.Equal(memory.UpdatedAt) {
		t.Error("closing edited the memory")
	}
	gotArtifact, err := e.svc.GetArtifact(e.ctx(), e.mine, artifact.ID)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !gotArtifact.UpdatedAt.Equal(artifact.UpdatedAt) {
		t.Error("closing edited the artifact")
	}
}

// countEverything reads the row count of every Palace table for one
// workspace, straight from the database.
func (e *env) countEverything() map[string]int {
	e.t.Helper()
	out := map[string]int{}
	for _, table := range []string{
		"rooms", "artifacts", "artifact_items", "sources",
		"memories", "memory_sources", "relations",
	} {
		var n int
		if err := e.pool.QueryRow(e.ctx(),
			fmt.Sprintf(`SELECT count(*) FROM palace.%s WHERE workspace_id = $1`, table),
			e.mine).Scan(&n); err != nil {
			e.t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

/* ══════════════════════════════════════════════════════════════════════
   Nothing leaks
   ══════════════════════════════════════════════════════════════════════ */

func TestNoSessionFailurePathCarriesTheSummary(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)

	// The summary IS the working context, and it is the one free-text
	// field a session carries.
	if _, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Summary: strPtr(canary)}); err != nil {
		t.Fatalf("focus: %v", err)
	}

	cases := map[string]func() error{
		"summary too long": func() error {
			_, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{
				Summary: strPtr(canary + strings.Repeat("x", domain.MaxSessionSummary)),
			})
			return err
		},
		"empty focus": func() error {
			_, err := e.svc.FocusSession(e.ctx(), e.mine, domain.SessionFocus{})
			return err
		},
		"foreign room": func() error {
			theirs := e.room(e.theirs, "deles", domain.SensitivityNormal)
			_, err := e.svc.FocusSession(e.ctx(), e.mine,
				domain.SessionFocus{Room: domain.SetRef(theirs.ID)})
			return err
		},
		"no open session": func() error {
			_, err := e.svc.FocusSession(e.ctx(), e.theirs,
				domain.SessionFocus{Summary: strPtr(canary)})
			return err
		},
	}

	for name, run := range cases {
		err := run()
		if err == nil {
			t.Errorf("%s: want a failure", name)
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: the failure quoted the summary: %v", name, err)
		}
	}

	if strings.Contains(e.logs.String(), canary) {
		t.Errorf("the service logged the summary: %s", e.logs.String())
	}
}

func TestASessionLoadedFromPostgresStillRedactsItself(t *testing.T) {
	e := newEnv(t)
	e.startSession(e.mine)
	if _, err := e.svc.FocusSession(e.ctx(), e.mine,
		domain.SessionFocus{Summary: strPtr(canary)}); err != nil {
		t.Fatalf("focus: %v", err)
	}

	loaded, err := e.svc.GetOpenSession(e.ctx(), e.mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	raw, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), canary) {
		t.Errorf("a session loaded from Postgres serialised its summary: %s", raw)
	}
	if strings.Contains(fmt.Sprintf("%v", loaded), canary) {
		t.Errorf("a session loaded from Postgres formatted its summary")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   The v1 restriction is a restriction
   ══════════════════════════════════════════════════════════════════════ */

func TestSessionCarriesNoChannelIdentity(t *testing.T) {
	// One open session per workspace works because there is one way in.
	// Concurrent channels are a real direction and would need independent
	// sessions, which means dropping the index AND giving a session a
	// channel identity. Neither is designed here, and this test exists so
	// that adding half of it is a deliberate act: a channel column with
	// the index still in place would be a schema that promises
	// per-channel sessions and delivers one.
	e := newEnv(t)
	e.startSession(e.mine)

	rows, err := e.pool.Query(e.ctx(), `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'palace' AND table_name = 'sessions'`)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, forbidden := range []string{"channel", "conversation", "telegram", "device"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("palace.sessions carries %q; the one-open-per-workspace index "+
					"must be dropped in the same change that adds a channel identity", name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read columns: %v", err)
	}
}

/* ── shared helper ───────────────────────────────────────────────────── */

func strPtr(s string) *string { return &s }
