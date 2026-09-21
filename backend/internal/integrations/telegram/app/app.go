// Package app orchestrates one Telegram update into one C.O.R.S.I. turn.
//
// ══════════════════════════════════════════════════════════════════════
//
//	TELEGRAM IS AN INTERFACE TO C.O.R.S.I., NOT A SECOND RUNTIME
//
// ══════════════════════════════════════════════════════════════════════
//
// Nothing in this package decides anything a chat runtime decides. It does
// not choose a model, assemble a context window, authorize a capability,
// price a turn, apply a budget or write a receipt. It answers three
// questions — WHO is speaking, WHICH agent, WHICH conversation — and then
// calls the runtime, which is the same application service the web
// client's SSE route drives.
//
// The consequence worth stating: every guarantee the web chat has, this
// surface has, because there is one implementation of each and it is not
// here. A grant, a Confidential redaction, a WriteReceipt, a ReadReceipt,
// prompt caching, the terminal vocabulary and Safe Resume all behave
// identically, and a change to any of them changes both surfaces at once.
package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

// DefaultTurnTimeout bounds one turn started from Telegram.
//
// ── Why this surface needs one when the HTTP route does not ────────────
// The streaming routes run under `httpserver.WithoutRequestDeadline`: the
// turn lives as long as the reader's connection, and a reader who closes
// the tab ends it. Telegram has no connection to close. Without a deadline
// here, a gateway that accepts a request and never answers would hold this
// chat's single turn slot until the process restarts, and the operator
// would see a bot that stopped replying with no way to find out why.
//
// Five minutes is chosen against the worst legitimate turn rather than the
// typical one: four provider calls with three rounds of tool execution
// between them (see chat/app.maxToolRounds), each of which may be a slow
// external read.
//
// It composes with the runtime rather than fighting it. The runtime maps
// `context.DeadlineExceeded` to `FinishDeadline` — a terminal that is
// RESUMABLE by construction — so a turn killed here is persisted with its
// receipts, and the operator is offered [Continuar] rather than losing the
// work. See chat/domain.FinishDeadline and chat/app.ResumableFinish.
const DefaultTurnTimeout = 5 * time.Minute

type Deps struct {
	Bindings   ports.BindingRepo
	ChatAgents ports.ChatAgentRepo
	Pairings   ports.PairingRepo
	Bot        ports.BotAPI
	Runtime    ports.Runtime
	Logger     *slog.Logger
	// Now is the clock. Injected so the pairing tests can expire a code
	// without sleeping; nil means time.Now.
	Now func() time.Time
	// TurnTimeout overrides DefaultTurnTimeout. Zero means the default.
	TurnTimeout time.Duration
}

type Service struct {
	bindings   ports.BindingRepo
	chatAgents ports.ChatAgentRepo
	pairings   ports.PairingRepo
	bot        ports.BotAPI
	runtime    ports.Runtime
	log        *slog.Logger
	now        func() time.Time
	turnTTL    time.Duration
	inflight   *inflight
}

func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	ttl := d.TurnTimeout
	if ttl <= 0 {
		ttl = DefaultTurnTimeout
	}
	return &Service{
		bindings: d.Bindings, chatAgents: d.ChatAgents, pairings: d.Pairings,
		bot: d.Bot, runtime: d.Runtime, log: d.Logger,
		now: now, turnTTL: ttl, inflight: newInflight(),
	}
}

/* ── the one entry point ─────────────────────────────────────────────── */

// HandleUpdate processes one normalized Telegram update.
//
// ── What "error" means here, and what it does not ──────────────────────
// A returned error is for the LOG and for tests. It is never what the user
// sees: by the time this returns, whatever the person needed to be told
// has already been sent by the handler that decided it. Two channels on
// purpose — the log may name a conversation id and a terminal reason, and
// the phone may not.
func (s *Service) HandleUpdate(ctx context.Context, up ports.Update) error {
	switch {
	case up.Message != nil:
		return s.handleMessage(ctx, up.Message)
	case up.Callback != nil:
		return s.handleCallback(ctx, up.Callback)
	default:
		// An update kind this integration does not serve — an edited
		// message, a poll answer, a member joining. Ignored in silence:
		// answering would be this bot responding to things it does not
		// understand.
		s.log.Debug("telegram update ignored", "update_id", up.UpdateID)
		return nil
	}
}

/* ── inbound text ────────────────────────────────────────────────────── */

func (s *Service) handleMessage(ctx context.Context, m *ports.InboundMessage) error {
	// ── Groups ──────────────────────────────────────────────────────
	//
	// Out of scope, and refused rather than ignored, once. A group is a
	// room full of people who are not the operator; a bot that silently
	// read group messages would be a bot whose behaviour nobody in that
	// room could predict. The refusal names no workspace and no agent.
	if m.ChatType != "" && m.ChatType != chatTypePrivate {
		s.log.Info("telegram non-private chat refused",
			"chat_id", m.TelegramChatID, "chat_type", m.ChatType)
		return s.say(ctx, m.TelegramChatID, copyOnlyPrivate)
	}

	text := strings.TrimSpace(m.Text)
	if text == "" {
		// A sticker, a photo, a location. Out of scope for T1 and
		// deliberately silent rather than nagging: the operator knows they
		// sent a picture.
		return nil
	}
	if strings.HasPrefix(text, "/") {
		return s.handleCommand(ctx, m, text)
	}
	return s.handleConversationText(ctx, m, text)
}

const chatTypePrivate = "private"

func (s *Service) handleCommand(ctx context.Context, m *ports.InboundMessage, text string) error {
	// Telegram appends `@botname` to commands sent where more than one bot
	// could answer. Stripped so `/agents@CorsiBot` is `/agents`.
	cmd := strings.ToLower(strings.Fields(text)[0])
	if at := strings.Index(cmd, "@"); at > 0 {
		cmd = cmd[:at]
	}
	switch cmd {
	case "/start":
		return s.commandStart(ctx, m)
	case "/help":
		return s.say(ctx, m.TelegramChatID, copyHelp)
	case "/agents":
		return s.commandAgents(ctx, m)
	case "/status":
		return s.commandStatus(ctx, m)
	default:
		return s.say(ctx, m.TelegramChatID, copyUnknownCommand)
	}
}

/* ── the binding gate ────────────────────────────────────────────────── */

// requireBinding is the single door every authorized operation goes
// through.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE WORKSPACE NEVER COMES FROM TELEGRAM
//
// ══════════════════════════════════════════════════════════════════════
//
// It is resolved from a row an operator wrote when they confirmed a
// pairing code. Nothing in an update proposes one, nothing in a callback
// carries one, and there is no code path in this package that accepts a
// workspace as a parameter from the outside.
//
// The (user, chat) pair is checked, not just looked up: see
// domain.Binding.Authorizes. An unpaired sender, a revoked binding and a
// binding belonging to a different Telegram account all produce the SAME
// refusal, because telling them apart would answer questions to whoever
// asked.
func (s *Service) requireBinding(ctx context.Context, userID, chatID int64) (*domain.Binding, error) {
	b, err := s.bindings.FindByChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if b == nil || !b.Authorizes(userID, chatID) {
		return nil, domain.NotPaired("this telegram chat is not bound to a workspace")
	}
	return b, nil
}

/* ── agents ──────────────────────────────────────────────────────────── */

func (s *Service) commandAgents(ctx context.Context, m *ports.InboundMessage) error {
	b, err := s.requireBinding(ctx, m.TelegramUserID, m.TelegramChatID)
	if err != nil {
		return s.refuse(ctx, m.TelegramChatID, err)
	}
	agents, err := s.runtime.ListAgents(ctx, b.WorkspaceID)
	if err != nil {
		return s.fail(ctx, m.TelegramChatID, "list agents", err)
	}
	if len(agents) == 0 {
		return s.say(ctx, m.TelegramChatID, copyNoAgents)
	}

	buttons := make([]ports.Button, 0, len(agents))
	for _, a := range agents {
		label := a.Name
		if b.ActiveAgentID != nil && *b.ActiveAgentID == a.ID {
			// The current selection, marked in the label rather than by
			// reordering: a list that moves under your thumb is a list you
			// mis-tap.
			label = "• " + label
		}
		buttons = append(buttons, ports.Button{
			Label: label,
			Data:  domain.EncodeCallback(domain.CallbackSelectAgent, a.ID),
		})
	}
	return s.bot.SendMessage(ctx, ports.OutboundMessage{
		TelegramChatID: m.TelegramChatID,
		Text:           copyPickAgent,
		Buttons:        buttons,
	})
}

// selectAgent changes which agent this chat talks to.
//
// ── The authorization, and why it is a list membership test ────────────
// The id arrived in `callback_data`, which is client-controlled. It is
// checked against the agents the BOUND workspace actually has — not
// against a signature, not against what the last /agents message offered.
// A forged callback naming an agent in another workspace finds no match
// and is refused; so does one naming an agent that has since been deleted.
//
// Nothing here grants a capability. Selecting an agent chooses whose
// conversation the next message joins; what that agent may DO is the
// Agents module's grants, unchanged and unreachable from here.
func (s *Service) selectAgent(ctx context.Context, b *domain.Binding, agentID uuid.UUID) (string, error) {
	agents, err := s.runtime.ListAgents(ctx, b.WorkspaceID)
	if err != nil {
		return "", err
	}
	var chosen *ports.Agent
	for i := range agents {
		if agents[i].ID == agentID {
			chosen = &agents[i]
			break
		}
	}
	if chosen == nil {
		return "", domain.NotFound("agent")
	}
	if err := s.bindings.SetActiveAgent(ctx, b.ID, chosen.ID); err != nil {
		return "", err
	}
	// The conversation is opened now rather than on the first message, so
	// "selected" and "has somewhere to talk" are the same event. A failure
	// here is a failure of the selection, which is the honest reading: a
	// chat pointed at an agent it cannot open a thread with is not
	// selected in any useful sense.
	if _, err := s.conversationFor(ctx, b, *chosen); err != nil {
		return "", err
	}
	return chosen.Name, nil
}

// findAgent resolves the binding's active agent against what the workspace
// currently has.
//
// It is the same list membership test selectAgent performs, and it runs
// again on every message on purpose: an agent can be deleted between
// selecting it and speaking to it, and the product should say so rather
// than failing somewhere deeper with a sentence nobody can act on.
func (s *Service) findAgent(ctx context.Context, b *domain.Binding) (*ports.Agent, error) {
	if b.ActiveAgentID == nil {
		return nil, nil
	}
	agents, err := s.runtime.ListAgents(ctx, b.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for i := range agents {
		if agents[i].ID == *b.ActiveAgentID {
			return &agents[i], nil
		}
	}
	return nil, nil
}

/* ── the conversation mapping ────────────────────────────────────────── */

// conversationFor resolves, and creates if needed, the C.O.R.S.I.
// conversation for one (telegram chat, agent) pair.
//
// ══════════════════════════════════════════════════════════════════════
//
//	SWITCHING AGENTS SELECTS A CONVERSATION; IT NEVER MOVES ONE
//
// ══════════════════════════════════════════════════════════════════════
//
// Palace keeps conversation A, Scout keeps conversation B, and switching
// back to Palace returns to A with everything that was said in it. That is
// context isolation between agents, and it is structural: the pair is the
// key, so there is no code path that could reuse one thread under two
// agents even by mistake.
//
// ── Why a stored mapping can be stale, and what happens then ───────────
// There is no foreign key to `chat.conversations` — a cross-schema FK
// would be the coupling the architecture forbids. So the conversation this
// row points at can be deleted from the web client, and the mapping would
// then name nothing. Asking the runtime turns that into a NEW conversation
// rather than an error on somebody's phone: the old thread was deleted on
// purpose, and the honest continuation is a fresh one.
func (s *Service) conversationFor(ctx context.Context, b *domain.Binding, agent ports.Agent) (*domain.ChatAgent, error) {
	existing, err := s.chatAgents.Find(ctx, b.ID, agent.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		ok, err := s.runtime.ConversationExists(ctx, b.WorkspaceID, existing.ConversationID)
		if err != nil {
			return nil, err
		}
		if ok {
			return existing, nil
		}
		s.log.Info("telegram conversation mapping was stale; opening a new thread",
			"binding_id", b.ID, "agent_id", agent.ID, "conversation_id", existing.ConversationID)
	}

	convID, err := s.runtime.CreateConversation(ctx, b.WorkspaceID, agent.ID, conversationTitle(agent.Name))
	if err != nil {
		return nil, err
	}
	ca := &domain.ChatAgent{
		ID:             uuid.New(),
		WorkspaceID:    b.WorkspaceID,
		BindingID:      b.ID,
		AgentID:        agent.ID,
		ConversationID: convID,
	}
	if err := s.chatAgents.Upsert(ctx, ca); err != nil {
		return nil, err
	}
	return ca, nil
}

// conversationTitle labels a thread by where it came from and whose it is.
//
// ── Why a fixed label and not the first message ────────────────────────
// The runtime derives a title from the first user turn when the title is
// blank, which is right for the composer and wrong here: these threads are
// long-lived — one per agent, for the life of the binding — so a title
// taken from whatever was typed first would label a year of conversation
// with a question about coffee.
//
// The agent's name is in it because that is what makes the entries
// distinct in the web client's sidebar. It is a provenance label, so it is
// not translated, for the same reason a release snapshot is not.
func conversationTitle(agentName string) string { return "Telegram · " + agentName }
