package repo

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()
	unique := &pgconn.PgError{Code: pgUniqueViolation}
	other := &pgconn.PgError{Code: "12345"}
	plain := errors.New("boom")

	if !isUniqueViolation(unique) {
		t.Fatal("23505 should be unique violation")
	}
	if isUniqueViolation(other) {
		t.Fatal("other pg codes must not be unique violation")
	}
	if isUniqueViolation(plain) {
		t.Fatal("non-pg errors must not be unique violation")
	}
}

func TestIsFKViolation(t *testing.T) {
	t.Parallel()
	fk := &pgconn.PgError{Code: pgForeignKeyViolation}
	if !isFKViolation(fk) {
		t.Fatal("23503 should be FK violation")
	}
	if isFKViolation(errors.New("boom")) {
		t.Fatal("plain errors must not be FK violation")
	}
}
