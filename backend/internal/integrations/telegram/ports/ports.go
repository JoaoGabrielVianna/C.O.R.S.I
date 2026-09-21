// Package ports declares the interfaces the Telegram integration needs
// from the outside world, in both directions.
//
// ── The two directions, and why the second one is unusual here ─────────
//
//	OUTBOUND   BotAPI, and the repositories. The ordinary shape: this
//	           integration owns a protocol and a schema, and an adapter
//	           implements them.
//
//	INBOUND    Runtime. The C.O.R.S.I. chat runtime, as this integration
//	           needs it. Declared HERE, by the consumer, and satisfied by
//	           an adapter in cmd/corsi.
//
// ── Why Runtime is declared here and not imported from Agents ──────────
// Because `Integration → Module` is a forbidden import and this file is
// the reason it does not become one. GitHub solves the same problem from
// the other side: Agents declares `ports.Tool`, GitHub implements it, and
// cmd/corsi is the only place that knows both. Telegram DRIVES rather
// than being driven, so the interface belongs to the driver — the
// standard Go arrangement, and here it is also the architecture's.
//
// The types below are this integration's own. They are not the Agents
// module's structs with a different name: they carry exactly what a chat
// interface needs and nothing else, which is what keeps a change to
// `chat.domain.Message` from being a change to Telegram.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
)

// ErrNotResumable is what Runtime.Resume returns when the runtime REFUSED
// the resume — the named turn is not the conversation's last, is not an
// answer, or did not stop in a way that can be continued.
//
// ── Why the contract needs a sentinel at all ───────────────────────────
// Because "the runtime says no" and "the database is down" call for
// different words on a phone, and this package cannot tell them apart by
// inspecting the error: doing so would mean importing the Agents module's
// error vocabulary, which is the import this whole arrangement exists to
// avoid.
//
// So the adapter that satisfies Runtime declares it, by wrapping. A
// refusal is the ORDINARY outcome of pressing [Continuar] twice, and it
// must read as "that turn is finished", never as "something broke".
var ErrNotResumable = errors.New("turn is not resumable")

/* ── inbound: the C.O.R.S.I. chat runtime ────────────────────────────── */

// Runtime is the chat runtime, narrowed to what a messaging interface
// does.
//
// Every method takes a workspace id and it is always the BINDING's — never
// anything that arrived from Telegram. See domain/callback.go.
type Runtime interface {
	// ListAgents answers "which agents does this workspace have".
	//
	// It is also the authorization check for agent selection: an agent
	// that is not in this list cannot be selected, which makes "a forged
	// callback names an agent in another workspace" a refusal rather than
	// a rule somebody has to remember to write.
	ListAgents(ctx context.Context, workspaceID uuid.UUID) ([]Agent, error)

	// CreateConversation opens a new C.O.R.S.I. conversation for an agent.
	// Ordinary in every way: the same call the web client's "new thread"
	// button makes.
	CreateConversation(ctx context.Context, workspaceID, agentID uuid.UUID, title string) (uuid.UUID, error)

	// ConversationExists reports whether a conversation is still there and
	// still belongs to this workspace.
	//
	// It exists because this integration stores a conversation id in its
	// own schema with no foreign key (see the migration header), so the
	// row can outlive what it points at — an operator deleting a thread in
	// the web client is the ordinary way. Asking is how a stale mapping
	// becomes a new conversation instead of an error on somebody's phone.
	ConversationExists(ctx context.Context, workspaceID, conversationID uuid.UUID) (bool, error)

	// Send runs ONE ordinary C.O.R.S.I. turn: the user's message is
	// persisted, the agent's tools and grants apply, the budget applies,
	// the receipts are written.
	//
	// It is the same application service the SSE route drives. There is no
	// Telegram-specific path through the runtime and there must never be
	// one — a second path is a second place for the guarantees to be
	// subtly different.
	Send(ctx context.Context, in SendInput) (*TurnResult, error)

	// Resume continues an interrupted turn through the EXISTING safe
	// resume path.
	//
	// Not "send the word continue". A resume writes no user message,
	// carries the interrupted turn's receipt into the new attempt, and is
	// refused when the named turn is not the conversation's last or did
	// not stop in a resumable way. All of that is the runtime's, and this
	// method is how Telegram reaches it rather than reimplementing it.
	Resume(ctx context.Context, in ResumeInput) (*TurnResult, error)
}

// Agent is an agent as a chat interface needs it: something to name in a
// list and something to select.
type Agent struct {
	ID          uuid.UUID
	Name        string
	Description string
}

type SendInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	Content        string
}

type ResumeInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	// MessageID is the interrupted turn to continue. Required by the
	// runtime, and checked by it against the conversation's actual last
	// turn.
	MessageID uuid.UUID
}

// TurnResult is what one turn produced, as a messaging interface needs it.
//
// ── Why the receipts are in here and not fetched separately ────────────
// Because a surface that has to remember to ask for evidence is a surface
// that will one day render prose without it — which is precisely the
// defect Agents 1.4.0 and 1.6.0 closed for the web client. Making them
// part of the turn's result means a Telegram reply cannot be composed
// without them being in hand.
type TurnResult struct {
	// MessageID is the persisted assistant turn. It is what a resume
	// button names.
	MessageID uuid.UUID
	// Text is the assistant's answer, exactly as persisted. It may be
	// empty: a turn that died before its first token is a real, persisted
	// turn with no words, and inventing some would be the one thing this
	// whole design exists to prevent.
	Text string
	// FinishReason is the runtime's own terminal vocabulary, passed
	// through unchanged. This package does not interpret it beyond
	// Resumable below.
	FinishReason string
	// Resumable is the runtime's answer to "may this be continued",
	// computed by the runtime's own predicate rather than by a copy of it
	// here. A second copy is how Telegram would keep offering a button
	// after the rule changed.
	Resumable bool
	// Error is set when the turn failed AFTER having been persisted. Note
	// that Text, MessageID and the receipts are still meaningful in that
	// case — `done` means "here is what was written", not "it worked".
	Error string

	// ── evidence ────────────────────────────────────────────────────
	//
	// Counts rather than the full receipts, because a Telegram footer can
	// state counts and must not state payloads.
	WritesExecuted int
	WritesFailed   int
	WritesRefused  int
	// ExternalReadStatus is the read receipt's status, verbatim:
	// VERIFIED_EXTERNAL_READ, FAILED_EXTERNAL_READ or NO_EXTERNAL_READ.
	ExternalReadStatus string
	// ExternalReadAvailable is the domain's own presentation gate: whether
	// this turn could have read externally at all. Absence is only worth
	// showing where absence is meaningful. See chat/domain.ReadReceipt.
	ExternalReadAvailable bool
}

/* ── outbound: Telegram ──────────────────────────────────────────────── */

// BotAPI is the slice of Telegram's Bot API this integration uses.
//
// Five methods, and the shortlist is the point: a fuller client would be
// surface nobody is testing. Transport is not in this interface —
// GetUpdates is long polling today and a webhook handler tomorrow, and
// neither changes a line of the application layer.
type BotAPI interface {
	// GetMe verifies the token at start-up and names the bot, so the log
	// can say which bot this deployment is without the operator guessing.
	GetMe(ctx context.Context) (BotIdentity, error)
	// GetUpdates is long polling. `offset` confirms everything below it.
	GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)
	// SendMessage posts ONE transport message. Chunking is the caller's,
	// because it is a product decision about where to break prose, not a
	// property of the wire.
	SendMessage(ctx context.Context, out OutboundMessage) error
	// AnswerCallbackQuery clears the button's spinner. Telegram shows a
	// loading state on a pressed inline button until this is called.
	AnswerCallbackQuery(ctx context.Context, callbackID, text string) error
}

type BotIdentity struct {
	ID       int64
	Username string
}

// Update is one inbound event, normalized.
//
// ── What is deliberately missing ───────────────────────────────────────
// The sender's username, first name and language. None of them is
// identity, none of them is authorization, and a field that exists is a
// field something eventually reads. The numeric ids are the whole of what
// this integration knows about a person.
type Update struct {
	UpdateID int64
	// Message is a text message. Nil when this update is something else.
	Message *InboundMessage
	// Callback is an inline button press. Nil when this update is
	// something else.
	Callback *InboundCallback
}

type InboundMessage struct {
	MessageID      int64
	TelegramUserID int64
	TelegramChatID int64
	// ChatType is Telegram's own: "private", "group", "supergroup",
	// "channel". Carried so the application can refuse everything that is
	// not "private" — groups are out of scope, and a group is a room full
	// of people who are not the operator.
	ChatType string
	Text     string
}

type InboundCallback struct {
	CallbackID     string
	TelegramUserID int64
	TelegramChatID int64
	ChatType       string
	// Data is CLIENT-CONTROLLED. See domain/callback.go.
	Data string
}

// OutboundMessage is one transport message.
type OutboundMessage struct {
	TelegramChatID int64
	Text           string
	// Buttons is an inline keyboard, one row per entry. One column
	// throughout: an agent list on a phone reads as a column, and a
	// two-column layout would truncate the names this product uses.
	Buttons []Button
}

type Button struct {
	Label string
	// Data goes into `callback_data` and comes back verbatim. Never more
	// than domain.MaxCallbackBytes.
	Data string
}

/* ── outbound: storage ───────────────────────────────────────────────── */

// BindingRepo stores who may speak to which workspace.
//
// Every method that reads by a Telegram id returns the binding without
// checking it: authorization is domain.Binding.Authorizes, in one place,
// applied by the application layer. A repository that also decided would
// be a second authorization point free to disagree with the first.
type BindingRepo interface {
	FindByChat(ctx context.Context, telegramChatID int64) (*domain.Binding, error)
	Create(ctx context.Context, b *domain.Binding) error
	SetActiveAgent(ctx context.Context, id, agentID uuid.UUID) error
	Revoke(ctx context.Context, id uuid.UUID) error
}

// ChatAgentRepo stores the (binding, agent) → conversation mapping.
type ChatAgentRepo interface {
	Find(ctx context.Context, bindingID, agentID uuid.UUID) (*domain.ChatAgent, error)
	Upsert(ctx context.Context, ca *domain.ChatAgent) error
	Touch(ctx context.Context, id uuid.UUID) error
}

// PairingRepo stores issued codes.
type PairingRepo interface {
	// Issue writes a new code and expires every outstanding one for the
	// same Telegram user, in one transaction. Three `/start`s leave one
	// live code, not three.
	Issue(ctx context.Context, p *domain.PairingCode) error
	// Redeem consumes a code by its hash and returns what it was for.
	//
	// The read and the consumption are ONE statement, so two concurrent
	// redemptions cannot both succeed. A code that is expired, already
	// consumed or unknown returns the same not-found — telling them apart
	// would answer "was this ever a real code" to whoever asked.
	Redeem(ctx context.Context, codeHash []byte, now time.Time) (*domain.PairingCode, error)
}

// CursorRepo stores the last update this deployment took responsibility
// for. See the migration header on at-most-once.
type CursorRepo interface {
	Load(ctx context.Context) (int64, error)
	Save(ctx context.Context, lastUpdateID int64) error
}
