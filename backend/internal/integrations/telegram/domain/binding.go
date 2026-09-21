// Package domain holds the Telegram integration's own rules: what a
// binding is, what a pairing code is, how callback data is encoded, and
// how one answer becomes several transport messages.
//
// Everything here is pure. No database, no HTTP, no Telegram types, and
// deliberately nothing from the Agents module — this package could not
// name an agent's model or a conversation's history if it wanted to.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Binding is one Telegram chat's admission to one workspace.
//
// ── What it is NOT ─────────────────────────────────────────────────────
// It is not a session, not a credential and not an authorization to do
// anything in particular. It answers exactly one question — "which
// workspace does this Telegram chat speak to" — and every capability
// question after that is answered by the Agents module, through the same
// grants the web client goes through.
type Binding struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	// TelegramUserID is who is typing. TelegramChatID is where. In a
	// private chat they are numerically equal; they are stored and checked
	// separately anyway, because the day one of them stops being true is
	// the day a group message would otherwise be answered as the operator.
	TelegramUserID int64
	TelegramChatID int64
	// ActiveAgentID is the agent this chat is currently talking to. Nil
	// means none has been selected — a real state, reached by every
	// freshly paired chat. See the schema.
	ActiveAgentID *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	RevokedAt     *time.Time
}

// Live reports whether this binding may carry a message.
func (b *Binding) Live() bool { return b != nil && b.RevokedAt == nil }

// Authorizes reports whether an update from this (user, chat) pair may be
// served by this binding.
//
// ── Why both halves ────────────────────────────────────────────────────
// The chat id is how the binding was found, so on its own this check is
// almost tautological. The user id is the half that matters: it is the
// only thing standing between "a message arrived in a chat we trust" and
// "the person we trust sent it". Telegram supplies both on every update
// and neither is client-controlled in the sense that matters — they are
// assigned by Telegram, not chosen by the sender.
func (b *Binding) Authorizes(telegramUserID, telegramChatID int64) bool {
	return b.Live() &&
		b.TelegramUserID == telegramUserID &&
		b.TelegramChatID == telegramChatID
}

// ChatAgent is one (binding, agent) pair and the conversation that carries
// it.
//
// The whole of the conversation model, in one struct: switching agents
// selects a different row, and every row keeps its own C.O.R.S.I.
// conversation with its own history.
type ChatAgent struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	BindingID      uuid.UUID
	AgentID        uuid.UUID
	ConversationID uuid.UUID
	CreatedAt      time.Time
	LastUsedAt     time.Time
}
