// Package repo is the Postgres implementation of the Meta Threads storage
// port.
//
// One table, one row per workspace, and no post is ever written — see the
// note at the top of ports.go on why there is no PostRepo here.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/integrations/metathreads/domain"
	"github.com/corsi/backend/internal/integrations/metathreads/ports"
)

type Repos struct {
	Connections ports.ConnectionRepo
}

func New(pool *pgxpool.Pool) *Repos {
	return &Repos{Connections: &ConnectionRepo{pool: pool}}
}

type ConnectionRepo struct{ pool *pgxpool.Pool }

const connectionCols = `id, workspace_id, token_cipher, token_hint, token_expires_at,
	scopes, account_id, username, display_name, profile_picture_url,
	api_base_url, last_verified_at, created_at, updated_at`

func scanConnection(row pgx.Row) (*domain.Connection, error) {
	var c domain.Connection
	var scopes []string
	if err := row.Scan(&c.ID, &c.WorkspaceID, &c.TokenCipher, &c.TokenHint, &c.TokenExpiresAt,
		&scopes, &c.AccountID, &c.Username, &c.DisplayName, &c.ProfilePictureURL,
		&c.APIBaseURL, &c.LastVerifiedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Scopes = make([]domain.Scope, 0, len(scopes))
	for _, s := range scopes {
		c.Scopes = append(c.Scopes, domain.Scope(s))
	}
	return &c, nil
}

// Upsert writes the workspace's single connection.
//
// ── Why the soft-deleted row is revived rather than left behind ────────
// The unique index is partial (`WHERE deleted_at IS NULL`), so a
// disconnected row does not block a new one — but leaving it would
// accumulate one dead row per reconnect, each holding a sealed credential
// nothing can reach and nothing purges. Reconnecting the same workspace
// takes over the existing row, whichever state it was in.
func (r *ConnectionRepo) Upsert(ctx context.Context, c *domain.Connection) error {
	scopes := make([]string, 0, len(c.Scopes))
	for _, s := range c.Scopes {
		scopes = append(scopes, string(s))
	}
	base := c.APIBaseURL
	if base == "" {
		base = "https://graph.threads.net"
	}

	err := r.pool.QueryRow(ctx, `
		INSERT INTO meta_threads.connections (
			workspace_id, token_cipher, token_hint, token_expires_at, scopes,
			account_id, username, display_name, profile_picture_url, api_base_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (workspace_id) WHERE deleted_at IS NULL
		DO UPDATE SET
			token_cipher = EXCLUDED.token_cipher,
			token_hint = EXCLUDED.token_hint,
			token_expires_at = EXCLUDED.token_expires_at,
			scopes = EXCLUDED.scopes,
			account_id = EXCLUDED.account_id,
			username = EXCLUDED.username,
			display_name = EXCLUDED.display_name,
			profile_picture_url = EXCLUDED.profile_picture_url,
			api_base_url = EXCLUDED.api_base_url,
			updated_at = now()
		RETURNING id, created_at, updated_at`,
		c.WorkspaceID, c.TokenCipher, c.TokenHint, c.TokenExpiresAt, scopes,
		c.AccountID, c.Username, c.DisplayName, c.ProfilePictureURL, base,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("meta_threads: upsert connection: %w", err)
	}
	c.APIBaseURL = base
	return nil
}

func (r *ConnectionRepo) FindByWorkspace(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, error) {
	c, err := scanConnection(r.pool.QueryRow(ctx, `
		SELECT `+connectionCols+`
		FROM meta_threads.connections
		WHERE workspace_id = $1 AND deleted_at IS NULL`, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotConnected()
	}
	if err != nil {
		return nil, fmt.Errorf("meta_threads: read connection: %w", err)
	}
	return c, nil
}

func (r *ConnectionRepo) TouchVerified(ctx context.Context, workspaceID, id uuid.UUID, at time.Time) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE meta_threads.connections SET last_verified_at = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id, at); err != nil {
		return fmt.Errorf("meta_threads: touch connection: %w", err)
	}
	return nil
}

// ReplaceToken writes a refreshed credential, leaving identity alone.
func (r *ConnectionRepo) ReplaceToken(ctx context.Context, workspaceID, id uuid.UUID, cipher []byte, hint string, expiresAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE meta_threads.connections
		SET token_cipher = $3, token_hint = $4, token_expires_at = $5, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id, cipher, hint, expiresAt)
	if err != nil {
		return fmt.Errorf("meta_threads: replace token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotConnected()
	}
	return nil
}

// Disconnect soft-deletes AND destroys the stored credential.
//
// ── Why the cipher is overwritten and not merely orphaned ──────────────
// Because a sealed token on a row nobody reads is still a sealed token: it
// survives in backups, it is decryptable with the current SECRETS_KEY, and
// nothing about `deleted_at` makes it less valid at Meta. Disconnecting is
// the operator saying "stop holding this", and the honest implementation of
// that is not to hold it. The row stays so the fact of the disconnection
// stays.
func (r *ConnectionRepo) Disconnect(ctx context.Context, workspaceID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE meta_threads.connections
		SET deleted_at = now(), updated_at = now(),
		    token_cipher = ''::bytea, token_hint = '…'
		WHERE workspace_id = $1 AND deleted_at IS NULL`, workspaceID)
	if err != nil {
		return fmt.Errorf("meta_threads: disconnect: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotConnected()
	}
	return nil
}

// DisconnectByAccountID removes every live connection for one Meta account.
//
// The same soft delete plus cipher wipe Disconnect performs, keyed on the
// account id instead of the workspace. One statement, exact match, no
// caller-supplied predicate — see the note on the port.
func (r *ConnectionRepo) DisconnectByAccountID(ctx context.Context, accountID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE meta_threads.connections
		SET deleted_at = now(), updated_at = now(),
		    token_cipher = ''::bytea, token_hint = '…'
		WHERE account_id = $1 AND deleted_at IS NULL`, accountID)
	if err != nil {
		return 0, fmt.Errorf("meta_threads: disconnect by account: %w", err)
	}
	return tag.RowsAffected(), nil
}
