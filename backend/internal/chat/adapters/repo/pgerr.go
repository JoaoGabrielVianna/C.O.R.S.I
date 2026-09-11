package repo

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/corsi/backend/internal/chat/domain"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation
}

// mapPgError converts the two constraint violations this module can
// legitimately hit into domain errors, so the HTTP layer answers 409
// instead of 500. Anything else is a real fault and passes through.
func mapPgError(err error, conflictMsg, fkMsg string) error {
	switch {
	case isUniqueViolation(err):
		return domain.Conflict(conflictMsg)
	case isFKViolation(err):
		return domain.Invalid(fkMsg)
	default:
		return err
	}
}
