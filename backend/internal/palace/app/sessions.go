package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
)

// Sessions: the short-term working context.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A SESSION HOLDS FOCUS. IT DOES NOT HOLD HISTORY, AND IT CONSOLIDATES
//	NOTHING
//
// ══════════════════════════════════════════════════════════════════════
//
// What was said lives in the conversation that said it. What was learned
// lives in a Memory, written on purpose by somebody who decided it was
// worth keeping. A session holds what is in FOCUS and, once it is over,
// one paragraph about what the stretch was for.
//
// ── Closing consolidates nothing, and that is load-bearing ─────────────
// Close writes `status`, `closed_at` and `last_activity_at`. It does not
// create a Memory, a Source, an Artifact or a Relation, and it does not
// edit one. Deciding that something said during a stretch of work
// deserves to become durable knowledge is a judgement, and a system that
// made it automatically at the end of every session would fill the
// operator's palace with things they never chose to keep, each one
// indistinguishable afterwards from the ones they did.
//
// Memory Consolidation is a later, separate step, and when it arrives it
// will be something somebody asks for.

/* ── start ───────────────────────────────────────────────────────────── */

// StartSessionResult is what starting reports back.
type StartSessionResult struct {
	Session *domain.Session
	// AlreadyOpen is true when a session was already open and this call
	// simply found it.
	//
	// ── Why this is a result and not an error ──────────────────────────
	// Because the request has already been satisfied: the caller wanted
	// an open session and there is one. Refusing would make a model
	// apologise for a state that is correct, and retry. "Já havia uma
	// sessão aberta" is a different sentence from "abri uma", and this is
	// the field that lets a caller tell them apart.
	AlreadyOpen bool
}

// StartSession opens the workspace's working context, or finds the one
// that is already open.
//
// ── Idempotent, including under a race ─────────────────────────────────
// Two callers starting at the same instant get the SAME session, and
// exactly one of them is told it created it. The partial unique index is
// the guarantee and the repository is what turns losing the race into an
// answer instead of a unique violation. See SessionRepo.StartOpen, which
// explains why no single statement can do it.
//
// ── Finding one does not count as activity ─────────────────────────────
// `last_activity_at` is untouched when a session was already open.
// Discovering that a session exists is not working in it, and moving the
// clock would make "quando eu mexi nisso pela última vez" answer with the
// moment somebody asked rather than the moment something changed.
func (s *Service) StartSession(ctx context.Context, workspaceID uuid.UUID) (*StartSessionResult, error) {
	session, created, err := s.sessions.StartOpen(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return &StartSessionResult{Session: session, AlreadyOpen: !created}, nil
}

/* ── read ────────────────────────────────────────────────────────────── */

// GetOpenSession returns the workspace's open session.
//
// ── Why absence is not-found here and a RESULT in CloseSession ─────────
// Because they are different acts. This is a READ of something that is
// not there, which is what not-found means everywhere else in this
// context. Closing is a MUTATION whose goal state is already achieved,
// and telling a caller "it failed" when the thing they wanted is true
// would be wrong.
//
// Rendering the not-found as "nenhuma sessão aberta" rather than as a
// failure is the job of whatever surface asks: the tool layer already
// translates this context's not-found into something a model can act on.
//
// ── Reading never moves the clock ──────────────────────────────────────
// There is no write on this path at all.
func (s *Service) GetOpenSession(ctx context.Context, workspaceID uuid.UUID) (*domain.Session, error) {
	return s.sessions.FindOpen(ctx, workspaceID)
}

/* ── focus ───────────────────────────────────────────────────────────── */

// FocusResult is a completed change of working context.
type FocusResult struct {
	Session *domain.Session
	domain.SessionFocusResult
}

// FocusSession moves what the open session is pointed at.
//
// ── The order of the checks ────────────────────────────────────────────
//  1. An empty focus is refused, for the reason UpdateRoom states: a
//     caller that meant to change nothing had no reason to call.
//  2. The open session is found, or there is nothing to focus.
//  3. Each named reference is resolved IN THIS WORKSPACE, before the
//     change is applied, so a focus naming a room this workspace does not
//     have leaves the entity in memory untouched.
//  4. The domain applies it and decides whether anything moved.
//  5. Only a change that moved something is written, and only then does
//     the clock advance.
//
// ── Archived rooms and artifacts are allowed here ──────────────────────
// Deliberately, and unlike a Relation endpoint. A relation is an
// assertion about two things and drawing a new one to something retired
// is either a mistake or a sign it should be active again. Focus is
// where the operator is looking, and looking at something they archived
// is ordinary: it is how they decide to bring it back. So this path uses
// the existence checks, which ignore lifecycle, exactly as filing a
// memory in a room does.
//
// ── No implicit propagation between the two references ─────────────────
// Setting the active artifact does NOT set the active room, even when
// the artifact is filed in one. A session records where somebody is
// working, not what owns what: a person can be inside an artifact while
// thinking about a different area, and a system that quietly moved the
// room under them would be answering a question they did not ask. The
// two fields move only when they are named.
func (s *Service) FocusSession(ctx context.Context, workspaceID uuid.UUID, f domain.SessionFocus) (*FocusResult, error) {
	if f.Empty() {
		return nil, domain.Invalid(
			"a focus change must set or clear at least one of the room, the artifact or the summary")
	}

	session, err := s.sessions.FindOpen(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	if roomID, ok := f.Room.ID(); ok {
		if err := s.requireRoom(ctx, workspaceID, &roomID); err != nil {
			return nil, err
		}
	}
	if artifactID, ok := f.Artifact.ID(); ok {
		if err := s.requireArtifact(ctx, workspaceID, &artifactID); err != nil {
			return nil, err
		}
	}

	// The database's clock, read once, handed to the domain. The domain
	// stamps it only if something moved: see Session.Apply.
	now, err := s.clock.Now(ctx)
	if err != nil {
		return nil, err
	}

	changed := session.Apply(f, now)
	if err := session.Validate(); err != nil {
		return nil, err
	}

	// A change that moved nothing is not written, so the clock stays
	// where it was. A no-op that advanced `last_activity_at` would be the
	// session announcing work that did not happen, which is the same
	// dishonesty every other Unchanged in this context exists to prevent.
	if changed.Unchanged() {
		return &FocusResult{Session: session, SessionFocusResult: changed}, nil
	}

	if err := s.sessions.Update(ctx, workspaceID, session); err != nil {
		return nil, err
	}
	return &FocusResult{Session: session, SessionFocusResult: changed}, nil
}

/* ── close ───────────────────────────────────────────────────────────── */

// CloseReason says why a close did not close anything.
//
// ── Why this lives here and not in the domain ──────────────────────────
// Because it describes the outcome of a USE CASE, not a property of an
// entity, and the domain never sees the case it names: when there is no
// open session there is no Session to ask. A vocabulary in the domain
// for a situation the domain cannot observe would be a word with no
// owner.
type CloseReason string

const (
	// CloseReasonNoOpenSession: there was nothing to close.
	CloseReasonNoOpenSession CloseReason = "no_open_session"
)

func (r CloseReason) String() string { return string(r) }

// CloseSessionResult is what closing reports back.
type CloseSessionResult struct {
	// Session is the session that was closed, and nil when there was
	// none. A caller that checks Closed first never has to test it.
	Session *domain.Session
	Closed  bool
	// Reason is set only when Closed is false.
	Reason CloseReason
}

// CloseSession ends the workspace's working context.
//
// ══════════════════════════════════════════════════════════════════════
//
//	CLOSING TWICE IS AN ANSWER, NOT A FAILURE
//
// ══════════════════════════════════════════════════════════════════════
//
// The second call reports `Closed: false, Reason: no_open_session`. It
// is not an error, because nothing went wrong: the caller wanted no open
// session and there is none.
//
// ── And it does not go looking for one to close ────────────────────────
// There is no "most recent session" fallback. Picking the last closed
// one and re-closing it would move a timestamp on a record of work that
// finished hours ago, on the strength of a guess about what the caller
// meant. The honest answer is that there was nothing open.
//
// ── What it does NOT do ────────────────────────────────────────────────
// It writes three fields on one row and touches nothing else. No Memory,
// no Source, no Artifact, no Relation is created or edited. Consolidation
// is a later, separate, asked-for step; see the note at the top of this
// file.
func (s *Service) CloseSession(ctx context.Context, workspaceID uuid.UUID) (*CloseSessionResult, error) {
	session, err := s.sessions.FindOpen(ctx, workspaceID)
	if err != nil {
		if isNotFound(err) {
			return &CloseSessionResult{Closed: false, Reason: CloseReasonNoOpenSession}, nil
		}
		return nil, err
	}

	now, err := s.clock.Now(ctx)
	if err != nil {
		return nil, err
	}

	// The domain reports whether it actually closed. It cannot be false
	// here, because the session came from FindOpen, and the check stays
	// anyway: an invariant that is only true because of where the value
	// came from is one that breaks when somebody changes where it comes
	// from.
	if !session.Close(now) {
		return &CloseSessionResult{Closed: false, Reason: CloseReasonNoOpenSession}, nil
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	if err := s.sessions.Update(ctx, workspaceID, session); err != nil {
		return nil, err
	}
	return &CloseSessionResult{Session: session, Closed: true}, nil
}
