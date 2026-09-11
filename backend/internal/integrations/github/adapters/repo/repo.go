// Package repo is the Postgres implementation of the integration's storage
// ports.
//
// Every query is workspace-first. That is not a convention: an id arriving
// from a URL or from a model must never select a row it does not own, and
// the way to guarantee it is for the workspace predicate to be in the SQL
// rather than in a check the caller is trusted to have made.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

// Repos bundles the implementations so the module wires one value.
type Repos struct {
	Connections  ports.ConnectionRepo
	Repositories ports.RepositoryRepo
}

func New(pool *pgxpool.Pool) *Repos {
	txm := postgres.NewTxManager(pool)
	return &Repos{
		Connections:  &ConnectionRepo{pool: pool, txm: txm},
		Repositories: &RepositoryRepo{pool: pool, txm: txm},
	}
}

/* ── connections ─────────────────────────────────────────────────────── */

type ConnectionRepo struct {
	pool *pgxpool.Pool
	txm  *postgres.TxManager
}

const connectionCols = `id, workspace_id, auth_kind, token_cipher, token_hint,
	                    account_login, account_id, account_type, account_name,
	                    account_avatar_url, api_base_url, last_verified_at,
	                    created_at, updated_at, deleted_at`

func scanConnection(row pgx.Row) (*domain.Connection, error) {
	var c domain.Connection
	if err := row.Scan(&c.ID, &c.WorkspaceID, &c.AuthKind, &c.TokenCipher, &c.TokenHint,
		&c.AccountLogin, &c.AccountID, &c.AccountType, &c.AccountName,
		&c.AccountAvatarURL, &c.APIBaseURL, &c.LastVerifiedAt,
		&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// Upsert writes the workspace's single connection.
//
// ── Why it is a delete-then-insert and not an ON CONFLICT ──────────────
// The uniqueness that matters is partial (`WHERE deleted_at IS NULL`), and
// a partial unique index is not a conflict target ON CONFLICT can name
// portably. More importantly, replacing a connection must drop the
// authorized repositories that hung off the previous one: they were
// decisions about a credential that no longer exists, and carrying them
// over would silently re-authorize repositories against a different
// account. The FK cascade does that, and only a real delete triggers it.
// The delete and the insert are one transaction. A crash between them
// would otherwise leave a workspace disconnected by a request whose whole
// purpose was to connect it.
func (r *ConnectionRepo) Upsert(ctx context.Context, c *domain.Connection) error {
	return r.txm.WithinTx(ctx, func(ctx context.Context) error {
		conn := postgres.Conn(ctx, r.pool)
		// Hard delete, not a soft one: two soft-deleted rows are fine, but
		// the cascade to repositories only fires on a real DELETE, and
		// leaving the old row's repositories behind is the failure this
		// comment exists for.
		if _, err := conn.Exec(ctx,
			`DELETE FROM github.connections WHERE workspace_id = $1`, c.WorkspaceID); err != nil {
			return fmt.Errorf("replace github connection: %w", err)
		}
		q := `INSERT INTO github.connections
		      (id, workspace_id, auth_kind, token_cipher, token_hint,
		       account_login, account_id, account_type, account_name,
		       account_avatar_url, api_base_url, last_verified_at)
		      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		      RETURNING ` + connectionCols
		got, err := scanConnection(conn.QueryRow(ctx, q,
			c.ID, c.WorkspaceID, c.AuthKind, c.TokenCipher, c.TokenHint,
			c.AccountLogin, c.AccountID, c.AccountType, c.AccountName,
			c.AccountAvatarURL, c.APIBaseURL, c.LastVerifiedAt))
		if err != nil {
			return fmt.Errorf("insert github connection: %w", err)
		}
		*c = *got
		return nil
	})
}

func (r *ConnectionRepo) FindByWorkspace(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, error) {
	q := `SELECT ` + connectionCols + ` FROM github.connections
	      WHERE workspace_id = $1 AND deleted_at IS NULL`
	c, err := scanConnection(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A typed refusal rather than (nil, nil). "No GitHub here" is an
			// answer four layers branch on, and a nil that every caller must
			// remember to check is the version of it that eventually gets
			// dereferenced.
			return nil, domain.NotConnected()
		}
		return nil, fmt.Errorf("find github connection: %w", err)
	}
	return c, nil
}

func (r *ConnectionRepo) TouchVerified(ctx context.Context, workspaceID, id uuid.UUID, at time.Time) error {
	q := `UPDATE github.connections SET last_verified_at = $3, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	if _, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID, at); err != nil {
		return fmt.Errorf("touch github connection: %w", err)
	}
	return nil
}

// Disconnect removes the connection and, through the cascade, every
// repository authorization that depended on it.
//
// A hard delete rather than the soft one the column allows, and the choice
// is deliberate: the sealed token has to stop existing, and a soft-deleted
// row keeps it in the table where a future query without the `deleted_at`
// predicate would find it. The column stays because the read path filters
// on it and a schema that cannot express "gone" is one where a bug is
// unrecoverable — but disconnect means gone.
func (r *ConnectionRepo) Disconnect(ctx context.Context, workspaceID uuid.UUID) error {
	q := `DELETE FROM github.connections WHERE workspace_id = $1`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, workspaceID)
	if err != nil {
		return fmt.Errorf("disconnect github: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotConnected()
	}
	return nil
}

/* ── repositories ────────────────────────────────────────────────────── */

type RepositoryRepo struct {
	pool *pgxpool.Pool
	txm  *postgres.TxManager
}

const repositoryCols = `id, workspace_id, connection_id, github_id, owner, name,
	                    full_name, private, default_branch, html_url, description,
	                    owner_type, authorized_at, updated_at`

func scanRepository(row pgx.Row) (*domain.Repository, error) {
	var r domain.Repository
	if err := row.Scan(&r.ID, &r.WorkspaceID, &r.ConnectionID, &r.GitHubID, &r.Owner, &r.Name,
		&r.FullName, &r.Private, &r.DefaultBranch, &r.HTMLURL, &r.Description,
		&r.OwnerType, &r.AuthorizedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// ReplaceAll sets the authorized set to exactly `repos`.
//
// ── Why the whole set and not a diff ───────────────────────────────────
// Because the operator edits a set. Sending an add and a revoke as two
// requests creates a window in which the stored set is neither the old one
// nor the new one, and a tool call landing in that window reads an
// authorization nobody chose. One statement pair, one transaction, one
// answer.
//
// The delete comes first and is unconditional within the connection, so a
// repository that was authorized and is no longer in the list is revoked by
// the same call that adds the new ones — which is the property test
// "revoked repository is denied" actually depends on.
// The whole replacement is one transaction. Without it, a failure partway
// through the inserts would leave the workspace with FEWER authorizations
// than either the old set or the new one — a state nobody chose, produced
// by a request that reported an error the operator would probably retry.
func (r *RepositoryRepo) ReplaceAll(ctx context.Context, workspaceID, connectionID uuid.UUID, repos []domain.Repository) error {
	return r.txm.WithinTx(ctx, func(ctx context.Context) error {
		conn := postgres.Conn(ctx, r.pool)
		if _, err := conn.Exec(ctx,
			`DELETE FROM github.repositories WHERE workspace_id = $1 AND connection_id = $2`,
			workspaceID, connectionID); err != nil {
			return fmt.Errorf("clear github repositories: %w", err)
		}
		q := `INSERT INTO github.repositories
		      (id, workspace_id, connection_id, github_id, owner, name, full_name,
		       private, default_branch, html_url, description, owner_type)
		      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
		for i := range repos {
			x := &repos[i]
			if _, err := conn.Exec(ctx, q, uuid.New(), workspaceID, connectionID,
				x.GitHubID, x.Owner, x.Name, x.FullName, x.Private,
				x.DefaultBranch, x.HTMLURL, x.Description, x.OwnerType); err != nil {
				return fmt.Errorf("authorize github repository: %w", err)
			}
		}
		return nil
	})
}

func (r *RepositoryRepo) ListByConnection(ctx context.Context, workspaceID, connectionID uuid.UUID) ([]domain.Repository, error) {
	q := `SELECT ` + repositoryCols + ` FROM github.repositories
	      WHERE workspace_id = $1 AND connection_id = $2
	      ORDER BY lower(full_name) ASC`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("list github repositories: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Repository, 0)
	for rows.Next() {
		got, err := scanRepository(rows)
		if err != nil {
			return nil, fmt.Errorf("scan github repository: %w", err)
		}
		out = append(out, *got)
	}
	return out, rows.Err()
}

// FindByFullName is THE authorization check.
//
// Case-folded on both sides, because GitHub treats these names
// case-insensitively and a model will not reproduce capitalisation. The
// index is on `lower(full_name)`, so this is the read it was built for.
//
// Not found and not authorized are the same answer, deliberately: a caller
// who may not read a repository must not be able to learn from the error
// whether it exists.
func (r *RepositoryRepo) FindByFullName(ctx context.Context, workspaceID, connectionID uuid.UUID, fullName string) (*domain.Repository, error) {
	q := `SELECT ` + repositoryCols + ` FROM github.repositories
	      WHERE workspace_id = $1 AND connection_id = $2 AND lower(full_name) = $3`
	got, err := scanRepository(postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		workspaceID, connectionID, domain.NormalizeFullName(fullName)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.RepositoryNotAuthorized(fullName)
		}
		return nil, fmt.Errorf("find github repository: %w", err)
	}
	return got, nil
}

func (r *RepositoryRepo) CountByConnection(ctx context.Context, workspaceID, connectionID uuid.UUID) (int64, error) {
	q := `SELECT count(*) FROM github.repositories
	      WHERE workspace_id = $1 AND connection_id = $2`
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, workspaceID, connectionID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count github repositories: %w", err)
	}
	return n, nil
}
