// Package repo is the Postgres implementation of the Telegram
// integration's storage ports.
//
// ── The scoping rule, and where it differs from every other module ─────
// Elsewhere in this codebase the rule is "every query is workspace-first",
// because an id arriving from a URL or from a model must never select a
// row it does not own.
//
// Here the lookup that STARTS a request cannot be workspace-first, and
// saying so plainly is better than pretending otherwise: an inbound
// Telegram update carries a chat id and nothing else, and the binding is
// what TURNS that into a workspace. So `FindByChat` is keyed on the
// Telegram chat id alone.
//
// What makes that safe is that the row is the authority. It was written by
// an operator confirming a pairing code, its workspace_id is never
// supplied by a request, and every query after it is scoped by the
// workspace that row produced. The one lookup that is not workspace-first
// is the one that decides what the workspace is.
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

// Repos bundles the implementations so the module wires one value.
type Repos struct {
	Bindings   ports.BindingRepo
	ChatAgents ports.ChatAgentRepo
	Pairings   ports.PairingRepo
	Cursor     ports.CursorRepo
}

func New(pool *pgxpool.Pool) *Repos {
	txm := postgres.NewTxManager(pool)
	return &Repos{
		Bindings:   &BindingRepo{pool: pool},
		ChatAgents: &ChatAgentRepo{pool: pool},
		Pairings:   &PairingRepo{pool: pool, txm: txm},
		Cursor:     &CursorRepo{pool: pool},
	}
}

/* ── bindings ────────────────────────────────────────────────────────── */

type BindingRepo struct{ pool *pgxpool.Pool }

const bindingCols = `id, workspace_id, telegram_user_id, telegram_chat_id,
	                 active_agent_id, created_at, updated_at, revoked_at`

func scanBinding(row pgx.Row) (*domain.Binding, error) {
	var b domain.Binding
	if err := row.Scan(&b.ID, &b.WorkspaceID, &b.TelegramUserID, &b.TelegramChatID,
		&b.ActiveAgentID, &b.CreatedAt, &b.UpdatedAt, &b.RevokedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

// FindByChat returns the LIVE binding for a Telegram chat, or nil.
//
// Nil rather than a not-found error, because "this chat has no binding" is
// the ordinary state of every chat in the world and is not an exception.
// The caller decides what it means — /start issues a code, a message is
// refused — and a repository that decided for them would be making a
// product choice in a SQL file.
func (r *BindingRepo) FindByChat(ctx context.Context, telegramChatID int64) (*domain.Binding, error) {
	b, err := scanBinding(r.pool.QueryRow(ctx,
		`SELECT `+bindingCols+` FROM telegram.bindings
		  WHERE telegram_chat_id = $1 AND revoked_at IS NULL`, telegramChatID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (r *BindingRepo) Create(ctx context.Context, b *domain.Binding) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO telegram.bindings (id, workspace_id, telegram_user_id, telegram_chat_id, active_agent_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		b.ID, b.WorkspaceID, b.TelegramUserID, b.TelegramChatID, b.ActiveAgentID)
	if isUniqueViolation(err) {
		// The partial unique indexes: one live binding per chat, one per
		// Telegram user. Hitting either means somebody paired between the
		// check and the insert, which is a conflict rather than a fault.
		return domain.Conflict("this telegram identity is already bound to a workspace")
	}
	return err
}

func (r *BindingRepo) SetActiveAgent(ctx context.Context, id, agentID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE telegram.bindings SET active_agent_id = $2, updated_at = now()
		  WHERE id = $1 AND revoked_at IS NULL`, id, agentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Revoked between resolving the binding and selecting an agent.
		return domain.NotPaired("this binding is no longer live")
	}
	return nil
}

func (r *BindingRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE telegram.bindings SET revoked_at = now(), updated_at = now()
		  WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("binding")
	}
	return nil
}

/* ── chat agents ─────────────────────────────────────────────────────── */

type ChatAgentRepo struct{ pool *pgxpool.Pool }

func (r *ChatAgentRepo) Find(ctx context.Context, bindingID, agentID uuid.UUID) (*domain.ChatAgent, error) {
	var ca domain.ChatAgent
	err := r.pool.QueryRow(ctx,
		`SELECT id, workspace_id, binding_id, agent_id, conversation_id, created_at, last_used_at
		   FROM telegram.chat_agents WHERE binding_id = $1 AND agent_id = $2`,
		bindingID, agentID).
		Scan(&ca.ID, &ca.WorkspaceID, &ca.BindingID, &ca.AgentID, &ca.ConversationID,
			&ca.CreatedAt, &ca.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ca, nil
}

// Upsert writes the (binding, agent) → conversation mapping.
//
// ── Why the conflict UPDATES the conversation ──────────────────────────
// Because the only caller that reaches here with an existing row is the
// one that just discovered the stored conversation no longer exists — an
// operator deleted the thread in the web client. Pointing the mapping at
// the replacement is the whole purpose of that path; DO NOTHING would
// leave it pointing at the deleted one forever.
func (r *ChatAgentRepo) Upsert(ctx context.Context, ca *domain.ChatAgent) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO telegram.chat_agents (id, workspace_id, binding_id, agent_id, conversation_id)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (binding_id, agent_id) DO UPDATE
		    SET conversation_id = EXCLUDED.conversation_id,
		        last_used_at    = now()`,
		ca.ID, ca.WorkspaceID, ca.BindingID, ca.AgentID, ca.ConversationID)
	return err
}

func (r *ChatAgentRepo) Touch(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE telegram.chat_agents SET last_used_at = now() WHERE id = $1`, id)
	return err
}

/* ── pairing codes ───────────────────────────────────────────────────── */

type PairingRepo struct {
	pool *pgxpool.Pool
	txm  *postgres.TxManager
}

// Issue writes a code and retires every outstanding one for the same
// Telegram user, in one transaction.
//
// Retiring the old ones is what makes "I typed /start three times" leave
// ONE usable code. Without it, three codes would be live at once and the
// operator would have no way to know which of the three they were looking
// at — and two of them would be admissions nobody is tracking.
//
// They are marked consumed rather than deleted: a consumed code is a
// record that one was issued and superseded, and `consumed_at` already
// means "no longer redeemable" in the one query that matters.
func (r *PairingRepo) Issue(ctx context.Context, p *domain.PairingCode) error {
	return r.txm.WithinTx(ctx, func(ctx context.Context) error {
		conn := postgres.Conn(ctx, r.pool)
		if _, err := conn.Exec(ctx,
			`UPDATE telegram.pairing_codes SET consumed_at = now()
			  WHERE telegram_user_id = $1 AND consumed_at IS NULL`,
			p.TelegramUserID); err != nil {
			return err
		}
		_, err := conn.Exec(ctx,
			`INSERT INTO telegram.pairing_codes
			     (id, code_hash, telegram_user_id, telegram_chat_id, expires_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			p.ID, p.CodeHash, p.TelegramUserID, p.TelegramChatID, p.ExpiresAt)
		return err
	})
}

// Redeem consumes a code and returns what it was for, or nil.
//
// ══════════════════════════════════════════════════════════════════════
//
//	READ AND CONSUME ARE ONE STATEMENT
//
// ══════════════════════════════════════════════════════════════════════
//
// A SELECT followed by an UPDATE would let two concurrent confirmations
// both read an unconsumed row and both proceed — single-use enforced by
// timing. The UPDATE ... RETURNING below is atomic: the `consumed_at IS
// NULL` predicate is evaluated under the row lock the update takes, so
// exactly one caller can ever see a result.
//
// Expiry is evaluated here too, against `now` supplied by the caller's
// clock rather than the database's. That is deliberate and is the one
// place this package prefers the application clock: the tests need to
// expire a code without sleeping for ten minutes, and the comparison is
// against a timestamp this same process wrote.
func (r *PairingRepo) Redeem(ctx context.Context, codeHash []byte, now time.Time) (*domain.PairingCode, error) {
	var p domain.PairingCode
	err := r.pool.QueryRow(ctx,
		`UPDATE telegram.pairing_codes SET consumed_at = $2
		  WHERE code_hash = $1 AND consumed_at IS NULL AND expires_at > $2
		 RETURNING id, code_hash, telegram_user_id, telegram_chat_id,
		           expires_at, consumed_at, created_at`,
		codeHash, now).
		Scan(&p.ID, &p.CodeHash, &p.TelegramUserID, &p.TelegramChatID,
			&p.ExpiresAt, &p.ConsumedAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown, expired or already consumed. One answer for all three:
		// see app.ConfirmPairing.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

/* ── the update cursor ───────────────────────────────────────────────── */

type CursorRepo struct{ pool *pgxpool.Pool }

// Load returns the offset to resume from, or 0 for a deployment that has
// never polled.
//
// Zero means "whatever Telegram still has", which is its default and is
// correct for a first run: there is nothing this deployment has taken
// responsibility for yet.
func (r *CursorRepo) Load(ctx context.Context) (int64, error) {
	var v int64
	err := r.pool.QueryRow(ctx,
		`SELECT last_update_id FROM telegram.update_cursor WHERE id = TRUE`).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return v, nil
}

// Save advances the cursor, and never rewinds it.
//
// `GREATEST` is not decoration. A rewind would re-deliver updates this
// deployment already took responsibility for, which is the at-least-once
// behaviour the design rejected — and the way it would happen is an
// out-of-order write from a second poller, which is precisely the
// situation where nobody would think to look here.
func (r *CursorRepo) Save(ctx context.Context, lastUpdateID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO telegram.update_cursor (id, last_update_id) VALUES (TRUE, $1)
		 ON CONFLICT (id) DO UPDATE
		    SET last_update_id = GREATEST(telegram.update_cursor.last_update_id, EXCLUDED.last_update_id),
		        updated_at     = now()`, lastUpdateID)
	return err
}
