package repo

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const pgUniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
