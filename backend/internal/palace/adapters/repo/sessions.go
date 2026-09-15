package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

type SessionRepo struct{ pool *pgxpool.Pool }

const sessionCols = `id, workspace_id, status, active_room_id, active_artifact_id,
	summary, started_at, last_activity_at, closed_at`

func scanSession(row pgx.Row) (*domain.Session, error) {
	var s domain.Session
	if err := row.Scan(&s.ID, &s.WorkspaceID, &s.Status, &s.ActiveRoomID,
		&s.ActiveArtifactID, &s.Summary, &s.StartedAt, &s.LastActivityAt,
		&s.ClosedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// FindOpen reads the workspace's open session.
//
// `status = 'open'` rather than an ordering over every session: the
// partial unique index guarantees there is at most one, so this is a
// lookup and not a "most recent" heuristic that would keep working, and
// keep being wrong, if the index were ever dropped.
func (r *SessionRepo) FindOpen(ctx context.Context, workspaceID uuid.UUID) (*domain.Session, error) {
	s, err := scanSession(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+sessionCols+`
		FROM palace.sessions
		WHERE workspace_id = $1 AND status = $2`,
		workspaceID, string(domain.SessionOpen)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("this workspace has no open session")
	}
	if err != nil {
		return nil, safeDBError("read open session", err)
	}
	return s, nil
}

// startAttempts bounds the read-insert-read loop below.
//
// Three. Each attempt loses only if another caller opened a session in
// the window between this one's read and its insert, and the winner's
// session is then visible to the next read. Two callers need two
// attempts; three need at most three. A workspace with enough concurrent
// starts to exhaust this is not racing, it is looping, and a bounded
// failure is better than spinning.
const startAttempts = 3

// StartOpen returns the workspace's open session, opening one if there is
// none.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE RACE, AND WHY NO SINGLE STATEMENT CLOSES IT
//
// ══════════════════════════════════════════════════════════════════════
//
// Two callers start at the same instant. The partial unique index means
// exactly one row can be open, so one of them has to lose, and what it
// is told is the whole question.
//
//	plain INSERT              the loser blocks, then gets 23505. A
//	                          database error, surfacing a concurrency
//	                          detail in a use case that has a perfectly
//	                          good answer: "one is already open".
//
//	ON CONFLICT DO NOTHING    no error, and no row either. Better, and
//	                          not enough.
//
//	that, plus a UNION ALL    still no row. Under READ COMMITTED the
//	SELECT of the existing    whole statement runs on the snapshot taken
//	row                       before the winner committed, so the
//	                          SELECT half cannot see what the INSERT
//	                          half just collided with.
//
// The last one is the trap worth naming, because it is the pattern that
// works elsewhere in this package (see RelationRepo.Create) and reads as
// though it should work here. It does not, and the difference is that a
// relation's conflict is with a row that was already committed, while
// this one is with a row committed microseconds ago by somebody still in
// flight.
//
// So the loop below issues a FRESH statement after a conflict. A new
// statement takes a new snapshot, which is the only thing that can see
// the winner's row.
func (r *SessionRepo) StartOpen(ctx context.Context, workspaceID uuid.UUID) (*domain.Session, bool, error) {
	for attempt := 1; attempt <= startAttempts; attempt++ {
		// Read first. The common case by far is that a session is already
		// open, and this answers it without attempting a write.
		//
		// It also means `last_activity_at` is not touched: finding out
		// that a session exists is not working in it.
		found, err := r.FindOpen(ctx, workspaceID)
		if err == nil {
			return found, false, nil
		}
		if !isNotFound(err) {
			return nil, false, err
		}

		opened, conflicted, err := r.insertOpen(ctx, workspaceID)
		if err != nil {
			return nil, false, err
		}
		if !conflicted {
			return opened, true, nil
		}
		// Somebody opened one between our read and our insert. Their row
		// is committed by now; the next iteration's read is a new
		// statement and will see it.
	}
	// Not a database error and not the caller's fault, so it is reported
	// in our own words rather than as a failure of the last statement.
	return nil, false, fmt.Errorf(
		"palace: start session: could not settle which session is open after %d attempts",
		startAttempts)
}

// insertOpen tries to open a session, reporting a conflict rather than
// raising one.
//
// `ON CONFLICT (workspace_id) WHERE status = 'open' DO NOTHING` names the
// PARTIAL index, predicate included, which is what lets Postgres infer
// `sessions_one_open_idx` rather than looking for a constraint over the
// whole table. Without the predicate the statement would not compile
// against this schema at all.
//
// Every column but the workspace is left to its default, so `status`,
// `started_at` and `last_activity_at` are written by the database:
// `now()` inside the statement that creates the row is the same clock
// every other timestamp here comes from, and strictly better than one
// passed in from a process that read it a round trip earlier.
func (r *SessionRepo) insertOpen(ctx context.Context, workspaceID uuid.UUID) (*domain.Session, bool, error) {
	if workspaceID == uuid.Nil {
		return nil, false, fmt.Errorf("palace: start session: no workspace was supplied")
	}
	s, err := scanSession(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO palace.sessions (workspace_id)
		VALUES ($1)
		ON CONFLICT (workspace_id) WHERE status = 'open' DO NOTHING
		RETURNING `+sessionCols, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		// DO NOTHING returned nothing: another caller holds the open
		// session. Not an error, and deliberately not reported as one.
		return nil, true, nil
	}
	if err != nil {
		return nil, false, safeDBError("start session", err)
	}
	return s, false, nil
}

// Update writes a session's focus, summary, status and clocks.
//
// ── Why the timestamps are written and not stamped in SQL ──────────────
// Unlike every other Update in this package, which lets the statement
// write `updated_at = now()`, this one writes the values the entity
// holds. The reason is that the DOMAIN decides whether the clock moves:
// a focus change that moved nothing must not advance
// `last_activity_at`, and only Session.Apply knows whether anything
// moved. A `now()` in the SET clause would advance it on every call,
// including the no-ops, which is exactly the dishonesty the rule exists
// to prevent.
//
// The value still comes from the database's clock: the service reads it
// through ports.Clock and hands it to the domain. One authority, one
// round trip, and the entity in memory agrees with the row on disk.
func (r *SessionRepo) Update(ctx context.Context, workspaceID uuid.UUID, s *domain.Session) error {
	if err := assertWorkspace("update session", workspaceID, s.WorkspaceID); err != nil {
		return err
	}
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		UPDATE palace.sessions
		SET status = $3, active_room_id = $4, active_artifact_id = $5,
		    summary = $6, last_activity_at = $7, closed_at = $8
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, s.ID, string(s.Status), s.ActiveRoomID, s.ActiveArtifactID,
		s.Summary, s.LastActivityAt, s.ClosedAt)
	if err != nil {
		return safeDBError("update session", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("session %s was not found", s.ID)
	}
	return nil
}
