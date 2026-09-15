package repo

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/corsi/backend/internal/palace/domain"
)

// ══════════════════════════════════════════════════════════════════════
//
//	A POSTGRES ERROR DOES NOT LEAVE THIS PACKAGE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What Postgres actually hands back ──────────────────────────────────
// When a CHECK constraint fires on a memory, the server answers with two
// separate strings, and the difference between them is the whole point:
//
//	Message  new row for relation "memories" violates check constraint
//	         "memories_importance_check"
//	Detail   Failing row contains (76435068-…, aaaa…, fact, <THE WHOLE
//	         CONTENT OF THE MEMORY>, <THE SUMMARY>, 99, medium, …)
//
// ── What was measured, rather than assumed ─────────────────────────────
// pgx v5.7.2 against Postgres 17, with a row whose content was a canary:
//
//	err.Error()                      no leak. PgError.Error() renders
//	                                 severity, Message and SQLSTATE, and
//	                                 never touches Detail
//	fmt.Sprintf("%+v", err)          no leak, same reason
//	slog TextHandler and JSONHandler no leak; both route an error value
//	                                 through Error()
//	json.Marshal(pgErr)              LEAKS. Every exported field,
//	                                 including Detail, including the row
//
// So the ordinary Go reflex, `fmt.Errorf("palace: insert memory: %w",
// err)`, does not print the row today. What it does is leave the
// *pgconn.PgError REACHABLE: any caller above can recover it with
// errors.As, which is exactly what error-handling code does, and from
// there the row is one json.Marshal away. A structured error reporter, an
// HTTP envelope that serialises the cause, a debug endpoint: each is one
// line, and each would publish somebody's memory.
//
// This is therefore a guard against a vector that is one line away, not a
// fix for a bug happening now. That distinction is stated plainly so
// nobody later reads the header, checks `err.Error()`, finds it clean and
// concludes the guard was superstition.
//
// ── Why it matters here and not everywhere ─────────────────────────────
// Because of where a Palace error ends up. It reaches the model as the
// body of a `tool` message and is written to
// `chat.tool_calls.error_message`, which `ToolDefinition.Confidential`
// does NOT redact: that flag drops `arguments` and `result` and leaves
// the error text alone. A context whose whole content is the operator's
// private record cannot have an unreviewed path from a constraint
// violation to that column.
//
// ── Why the domain validating first is not enough ──────────────────────
// The domain does validate before every write, so a CHECK should never
// fire. "Should never" is the assumption this codebase distrusts
// everywhere else: the constraints exist precisely as a backstop for the
// case where validation did not run or did not cover something, and a
// backstop whose failure mode is a data leak is worse than no backstop.
//
// ── What is kept ───────────────────────────────────────────────────────
// The SQLSTATE code and the constraint name. Both are vocabulary we
// chose, both are exactly what a person debugging needs, and neither can
// contain a row. The PgError is NOT wrapped, because wrapping is the
// reachability this exists to remove.

// safeDBError turns any error out of pgx into one that cannot carry a
// row.
//
// op names the operation in our own words ("insert memory"). It is a
// literal at every call site and never interpolates an entity.
func safeDBError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName != "" {
			// The constraint name is the single most useful fact here: it
			// says which rule was broken, and it is a name we wrote.
			return fmt.Errorf("palace: %s: database rejected the row (sqlstate %s, constraint %s)",
				op, pgErr.Code, pgErr.ConstraintName)
		}
		return fmt.Errorf("palace: %s: database rejected the statement (sqlstate %s)",
			op, pgErr.Code)
	}
	// Not a Postgres error: a context deadline, a dead pool, a scan type
	// mismatch. None of those carry a row, the text is worth keeping, and
	// `errors.Is(err, context.DeadlineExceeded)` has to keep working.
	return fmt.Errorf("palace: %s: %w", op, err)
}

// isNotFound reports whether err is this context's not-found.
//
// ── Why a repository needs to ask ──────────────────────────────────────
// Because one repository operation is built out of two others:
// StartOpen reads before it writes, and "there is no open session" is the
// signal to go ahead and open one. Everywhere else a not-found travels
// straight up to the caller; here it is a branch, and reading it with
// errors.As at the call site would spread the shape of domain.Error
// across a package whose job is SQL.
func isNotFound(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.KindNotFound
}
