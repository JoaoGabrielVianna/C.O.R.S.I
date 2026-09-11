package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/platform/postgres"
)

// ClockRepo answers "what time is it" with the DATABASE's clock.
//
// ── Why a repository, for something a process can read locally ─────────
// Because the finance contract classifies money against `now()` in SQL:
// REALIZED is `status = 'paid' AND occurred_at <= now()`, and that `now()`
// is the Postgres server clock — see docs/totals-contract.md and
// TransactionRepo.Totals. Anything that stamps a transaction from a
// DIFFERENT clock is a second authority, and the two only have to disagree
// by a fraction of a second for the contract to answer wrongly.
//
// The failure is not hypothetical and it is not small. A payment recorded
// as happening NOW, stamped from an API process whose clock runs a few
// hundred milliseconds ahead of the database, lands with `occurred_at >
// now()`. The row is then PROJECTED rather than REALIZED, and the user who
// records a purchase and immediately asks what they spent today is told
// zero. Both numbers are internally consistent, nothing errors, and the
// only evidence is a total that is quietly wrong.
//
// One clock, and it is the one the classification uses.
type ClockRepo struct {
	pool *pgxpool.Pool
}

func NewClockRepo(pool *pgxpool.Pool) *ClockRepo {
	return &ClockRepo{pool: pool}
}

// Now returns the database's current transaction timestamp.
//
// `now()` and not `clock_timestamp()`, deliberately: it is the same
// function the totals query calls, so inside a single transaction the
// stamp and the classification are not merely close, they are identical.
func (r *ClockRepo) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `SELECT now()`).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("read database clock: %w", err)
	}
	return t, nil
}
