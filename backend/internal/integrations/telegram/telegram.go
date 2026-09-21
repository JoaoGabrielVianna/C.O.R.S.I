// Package telegram is the Telegram integration.
//
// ══════════════════════════════════════════════════════════════════════
//
//	TELEGRAM IS AN INTERFACE TO C.O.R.S.I., NOT A SECOND RUNTIME
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What it owns ───────────────────────────────────────────────────────
// The Bot API protocol, the bot token, the long-polling transport, the
// numeric identity space Telegram assigns, and the routing state that
// turns a chat id into a workspace, an agent and a conversation. Nothing
// else. It owns no business rule and answers no product question.
//
// ── What it explicitly does NOT own ────────────────────────────────────
// Agents, conversations, memory, tools, grants, budgets, receipts, cost.
// Every one of those is the Agents module's, reached through
// `ports.Runtime`, and there is no second implementation of any of them
// anywhere in this directory. A message sent from a phone and a message
// sent from the web client go through the same application service, in the
// same order, with the same guarantees.
//
// ── The dependency direction, which is the unusual part ────────────────
//
//	GitHub    Agents  →  chat/ports.Tool      ←  GitHub Integration
//	          (the module declares, the integration implements)
//
//	Telegram  Telegram Integration  →  telegram/ports.Runtime  ←  Agents
//	          (the integration declares, cmd/corsi adapts)
//
// GitHub is DRIVEN by Agents; Telegram DRIVES it. So the interface belongs
// to the consumer in both cases, and in both cases neither package imports
// the other: this one names nothing in `internal/chat`, and `internal/chat`
// has never heard of Telegram. The arrow that would make them a cycle
// exists only in cmd/corsi, as a variable.
//
// ── The two things it hands out ────────────────────────────────────────
//
//	Run(ctx)        the long-polling loop. Started by cmd/corsi as a
//	                goroutine, stopped by cancelling its context.
//	Pairing()       the operator-side pairing service, used by
//	                cmd/telegramctl. No HTTP route — see README.
//
// There is no Register(r chi.Router) and that is a decision, not an
// omission: this integration serves no browser. Its management surface is
// an operator CLI, and a route with no caller is a surface nobody is
// testing.
package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/integrations/telegram/adapters/botapi"
	"github.com/corsi/backend/internal/integrations/telegram/adapters/poller"
	"github.com/corsi/backend/internal/integrations/telegram/adapters/repo"
	"github.com/corsi/backend/internal/integrations/telegram/app"
	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

type Deps struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// Token is the bot token, from the deployment's environment.
	//
	// ── Why it is a parameter and not a stored credential ──────────────
	// It identifies THIS DEPLOYMENT to Telegram, not a workspace to us:
	// one token serves the whole installation, it is issued by BotFather
	// to a person, and it is rotated by editing an environment. That is
	// the same argument META_THREADS_APP_SECRET makes, and the opposite
	// of a user's own token, which lives sealed in a table.
	//
	// It is never persisted, never logged, never returned and never put
	// in an error. See botapi.scrub.
	Token string
	// Runtime is the C.O.R.S.I. chat runtime. Supplied by cmd/corsi.
	Runtime ports.Runtime
	// Bot replaces the real Bot API client. Nil means the real one, which
	// is what production gets. It exists so the integration suite can
	// drive the whole stack against a fake without a global switch a
	// deployment could also read.
	Bot ports.BotAPI
	// TurnTimeout bounds one turn. Zero means app.DefaultTurnTimeout.
	TurnTimeout time.Duration
}

type Module struct {
	deps   Deps
	svc    *app.Service
	poller *poller.Poller
	bot    ports.BotAPI
}

// New wires the integration.
//
// ── Why a missing token is an error and not a warning ──────────────────
// Because this module is only constructed when a deployment asked for it.
// The switch is at the composition root: no TELEGRAM_BOT_TOKEN means no
// Telegram at all, and the rest of C.O.R.S.I. runs untouched. Reaching
// here with an empty token means the operator asked for the integration
// and the secret is not there, and starting anyway would produce a bot
// that silently answers nobody.
func New(deps Deps) (*Module, error) {
	bot := deps.Bot
	if bot == nil {
		c, err := botapi.New(botapi.Options{Token: deps.Token, Logger: deps.Logger})
		if err != nil {
			return nil, err
		}
		bot = c
	}
	if deps.Runtime == nil {
		return nil, domain.NotConfigured("the telegram integration has no chat runtime wired")
	}

	repos := repo.New(deps.Pool)
	svc := app.NewService(app.Deps{
		Bindings:    repos.Bindings,
		ChatAgents:  repos.ChatAgents,
		Pairings:    repos.Pairings,
		Bot:         bot,
		Runtime:     deps.Runtime,
		Logger:      deps.Logger,
		TurnTimeout: deps.TurnTimeout,
	})
	return &Module{
		deps:   deps,
		svc:    svc,
		bot:    bot,
		poller: poller.New(bot, repos.Cursor, svc, deps.Logger),
	}, nil
}

// Run verifies the token and then polls until ctx is cancelled.
//
// ── Why getMe happens here and not lazily ──────────────────────────────
// Because a bad token is a deployment problem, and a deployment problem
// should be visible at start-up rather than as a bot that never answers.
// It also names the bot in the log, so an operator running more than one
// can tell which is which without guessing from the token they cannot see.
func (m *Module) Run(ctx context.Context) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	me, err := m.bot.GetMe(verifyCtx)
	cancel()
	if err != nil {
		return err
	}
	m.deps.Logger.Info("telegram bot verified", "bot_username", me.Username, "bot_id", me.ID)
	return m.poller.Run(ctx)
}

// Pairing exposes the operator-side pairing operations.
//
// Narrowed to an interface rather than handing out the service, so the
// operator tool cannot reach HandleUpdate and start behaving like a second
// poller.
func (m *Module) Pairing() app.Pairing { return m.svc }
