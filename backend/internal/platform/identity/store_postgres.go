package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresStore is the Store backed by `identity.sessions`.
//
// The table has its own schema and its own migration timeline
// (`schema_migrations_identity`) for the reason every bounded context here
// has one: a shared timeline makes two independent deployables agree on a
// version number they have no reason to share.
type postgresStore struct{ pool *pgxpool.Pool }

// NewPostgresStore builds the production Store.
func NewPostgresStore(pool *pgxpool.Pool) Store { return &postgresStore{pool: pool} }

func (s *postgresStore) Create(ctx context.Context, tokenHash []byte, sess Session) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO identity.sessions (token_sha256, subject, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		tokenHash, sess.Subject, sess.IssuedAt, sess.ExpiresAt)
	return err
}

func (s *postgresStore) Lookup(ctx context.Context, tokenHash []byte, now time.Time) (Session, bool, error) {
	var sess Session
	err := s.pool.QueryRow(ctx,
		`SELECT subject, issued_at, expires_at
		   FROM identity.sessions
		  WHERE token_sha256 = $1 AND expires_at > $2`,
		tokenHash, now).Scan(&sess.Subject, &sess.IssuedAt, &sess.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return sess, true, nil
}

func (s *postgresStore) Delete(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM identity.sessions WHERE token_sha256 = $1`, tokenHash)
	return err
}

func (s *postgresStore) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM identity.sessions WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
