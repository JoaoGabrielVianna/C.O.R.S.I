package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

/* ── pairing: the two halves ─────────────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	THE TELEGRAM SIDE ASKS. THE OPERATOR SIDE DECIDES.
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The flow ───────────────────────────────────────────────────────────
//
//	Telegram        /start          → a code, on the operator's screen
//	Operator side   confirm(code)   → a binding, for THEIR workspace
//
// The asymmetry is the whole security model. Anybody who finds this bot
// can produce a code — it costs them a row and tells them nothing. Only
// somebody who can reach ConfirmPairing can turn a code into access, and
// what they can grant is the workspace THEY named, never one the code
// proposed. There is no field in which a Telegram user can suggest a
// workspace, which is why "binding cannot cross workspace" is a property
// of the shape rather than a check somebody has to remember.
//
// ── Why a code and not "whitelist my user id" ──────────────────────────
// Because a numeric Telegram id is not a secret and is not proof of
// possession. Pasting one into a config file binds whoever holds that id
// TODAY, with no evidence that the person configuring it is the person
// holding the phone. A code that was produced BY the phone and typed BY
// the operator proves both ends of the pairing in one step.

// Pairing is the operator-side surface, narrowed.
//
// It exists so the operator tool receives an interface with two methods
// instead of the whole service: a CLI that could reach HandleUpdate would
// be a second poller waiting to be written by accident.
type Pairing interface {
	ConfirmPairing(ctx context.Context, workspaceID uuid.UUID, code string) (*domain.Binding, error)
	RevokePairing(ctx context.Context, telegramChatID int64) error
}

// commandStart issues a pairing code, or says the chat is already bound.
func (s *Service) commandStart(ctx context.Context, m *ports.InboundMessage) error {
	existing, err := s.bindings.FindByChat(ctx, m.TelegramChatID)
	if err != nil {
		return s.fail(ctx, m.TelegramChatID, "find binding", err)
	}
	if existing != nil && existing.Authorizes(m.TelegramUserID, m.TelegramChatID) {
		return s.say(ctx, m.TelegramChatID, copyAlreadyPaired)
	}

	// ── A live binding held by SOMEBODY ELSE ────────────────────────
	//
	// Same chat id, different Telegram user. In a private chat that
	// should be impossible, which is exactly why it is refused rather
	// than handled: the honest response to an impossible state is to stop,
	// not to re-pair over the top of a binding whose owner did not ask.
	if existing != nil && existing.Live() {
		s.log.Warn("telegram /start in a chat bound to another user",
			"chat_id", m.TelegramChatID)
		return s.say(ctx, m.TelegramChatID, copyNotPaired)
	}

	code, rec, err := domain.NewPairingCode(m.TelegramUserID, m.TelegramChatID, s.now())
	if err != nil {
		return s.fail(ctx, m.TelegramChatID, "mint pairing code", err)
	}
	if err := s.pairings.Issue(ctx, rec); err != nil {
		return s.fail(ctx, m.TelegramChatID, "issue pairing code", err)
	}
	// The code is logged NOWHERE. This line records that one was issued
	// and for whom, which is what an audit needs; the value itself exists
	// in exactly two places, and neither is a log file.
	s.log.Info("telegram pairing code issued",
		"chat_id", m.TelegramChatID, "pairing_id", rec.ID, "expires_at", rec.ExpiresAt)

	return s.say(ctx, m.TelegramChatID,
		formatPairingIssued(domain.Format(code), int(domain.PairingTTL.Minutes())))
}

// ConfirmPairing is the OPERATOR side. It binds the Telegram identity that
// asked for `code` to `workspaceID`.
//
// ── Who may call this, and how that is enforced ────────────────────────
// It is exported from the application layer and reached by the operator
// tool (cmd/telegramctl), which speaks to Postgres directly and therefore
// requires POSTGRES_DSN. There is no HTTP route, and that is a decision
// rather than an omission — see docs/integrations/telegram/README.md. The
// short version: this product has no real authentication, the workspace
// header is a public constant and the middleware validates nothing, so an
// HTTP "confirm pairing" endpoint would be an unauthenticated endpoint
// that hands out workspace access. Possession of the database credential
// is the strongest authentication factor this deployment actually has, so
// the pairing gate is put behind it.
//
// ── What it refuses ────────────────────────────────────────────────────
// An unknown, expired or already-consumed code, all with the same answer.
// A Telegram identity that already has a live binding, because one person
// reaching two workspaces from one phone has no way to say which they
// meant on any given message.
func (s *Service) ConfirmPairing(ctx context.Context, workspaceID uuid.UUID, code string) (*domain.Binding, error) {
	if workspaceID == uuid.Nil {
		return nil, domain.Invalid("a workspace id is required to confirm a pairing")
	}
	normalized := domain.NormalizeCode(code)
	if normalized == "" {
		return nil, domain.Invalid("a pairing code is required")
	}

	// Redemption is one statement: it reads and consumes together, so two
	// concurrent confirmations of the same code cannot both win. Expiry is
	// evaluated inside it for the same reason — a check here would be a
	// check against a clock the database does not share.
	rec, err := s.pairings.Redeem(ctx, domain.HashCode(normalized), s.now())
	if err != nil {
		return nil, err
	}
	if rec == nil {
		// Unknown, expired, consumed: one answer. Distinguishing them
		// would tell a caller whether a code was ever real.
		return nil, domain.NotFound("pairing code")
	}

	existing, err := s.bindings.FindByChat(ctx, rec.TelegramChatID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Live() {
		return nil, domain.Conflict("this telegram chat is already bound to a workspace; revoke it first")
	}

	b := &domain.Binding{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		// From the CODE, never from the caller. The operator confirms a
		// code; they do not get to name which Telegram account it binds,
		// because the account is what the code already fixed when it was
		// issued.
		TelegramUserID: rec.TelegramUserID,
		TelegramChatID: rec.TelegramChatID,
	}
	if err := s.bindings.Create(ctx, b); err != nil {
		return nil, err
	}
	s.log.Info("telegram binding created",
		"binding_id", b.ID, "chat_id", b.TelegramChatID, "workspace_id", workspaceID)
	return b, nil
}

// RevokePairing ends a binding. The operator side, like ConfirmPairing.
//
// The conversations it pointed at are NOT touched: they are ordinary
// C.O.R.S.I. threads with ordinary history, and revoking a phone's access
// is not a reason to delete what was said. The mapping rows cascade with
// the binding; the transcripts are in `chat.messages` and stay there.
func (s *Service) RevokePairing(ctx context.Context, telegramChatID int64) error {
	b, err := s.bindings.FindByChat(ctx, telegramChatID)
	if err != nil {
		return err
	}
	if b == nil || !b.Live() {
		return domain.NotFound("binding")
	}
	if err := s.bindings.Revoke(ctx, b.ID); err != nil {
		return err
	}
	s.log.Info("telegram binding revoked", "binding_id", b.ID, "chat_id", telegramChatID)
	return nil
}

/* ── status ──────────────────────────────────────────────────────────── */

func (s *Service) commandStatus(ctx context.Context, m *ports.InboundMessage) error {
	b, err := s.bindings.FindByChat(ctx, m.TelegramChatID)
	if err != nil {
		return s.fail(ctx, m.TelegramChatID, "find binding", err)
	}
	if b == nil || !b.Authorizes(m.TelegramUserID, m.TelegramChatID) {
		return s.say(ctx, m.TelegramChatID, formatStatus(false, "", false))
	}
	// The agent is named by resolving it, not by trusting the stored id:
	// an agent deleted since selection should read as "none", which is
	// what the operator can act on.
	var name string
	if agent, err := s.findAgent(ctx, b); err != nil {
		s.log.Warn("telegram status: resolve agent", "chat_id", m.TelegramChatID, "err", err)
	} else if agent != nil {
		name = agent.Name
	}
	return s.say(ctx, m.TelegramChatID,
		formatStatus(true, name, s.inflight.running(m.TelegramChatID)))
}
