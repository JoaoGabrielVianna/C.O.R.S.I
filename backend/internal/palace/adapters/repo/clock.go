package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/platform/postgres"
)

// ClockRepo answers "what time is it" with the DATABASE's clock.
//
// ── Why Palace has one at all ──────────────────────────────────────────
// The argument is Finance's and Palace inherits it rather than
// re-deriving it: a context that stamps moments from the API process
// introduces a second authority, and two clocks only have to disagree by
// a fraction of a second for a record written a moment ago to sort into
// the future. Nothing errors when they do.
//
// Palace's version of that failure is a session whose `last_activity_at`
// precedes its own `started_at`, and a memory whose `occurred_at` is
// tomorrow because a tool resolved "hoje" locally.
//
// ── What uses it in this slice: nothing, and that is honest ────────────
// Room and Memory carry two system-set timestamps, `created_at` and
// `updated_at`, and both are stamped inside the SQL that writes the row.
// That IS the database clock, and passing one in from here would be
// strictly worse: an extra round trip for a value the statement can read
// itself, with a window in between.
//
// The port and this adapter exist so the surfaces that genuinely need to
// stamp a moment find the authority already here. A tool resolving a
// relative date and a session recording activity both arrive in later
// slices, and neither should have to reach for time.Now because the one
// clock was not wired yet.
type ClockRepo struct {
	pool *pgxpool.Pool
}

func NewClockRepo(pool *pgxpool.Pool) *ClockRepo { return &ClockRepo{pool: pool} }

// Now returns the database's current transaction timestamp.
//
// `now()` and not `clock_timestamp()`, deliberately and for the reason
// Finance gives: it is the same function the row-writing statements call,
// so inside one transaction the stamp and the row are not merely close,
// they are identical.
func (r *ClockRepo) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `SELECT now()`).Scan(&t); err != nil {
		return time.Time{}, safeDBError("read the database clock", err)
	}
	return t, nil
}
