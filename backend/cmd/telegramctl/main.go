// Command telegramctl is the OPERATOR side of Telegram pairing.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE SMALLEST SECURE GATE THIS DEPLOYMENT CAN ACTUALLY OFFER
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why a CLI and not an authenticated page ────────────────────────────
// Because there is no authenticated page. This product runs with no real
// authentication: `X-Workspace-Id` is accepted as ANY syntactically valid
// UUID without validation, the frontend sends a public constant, and the
// upstream gateway the middleware presupposes does not exist. Identity is
// a capability the platform is missing, and the documentation says so.
//
// So a "confirm pairing" HTTP route would be an UNAUTHENTICATED endpoint
// that hands out workspace access to whoever can reach the API, guarded by
// a header anybody can send. That is not a smaller version of a pairing
// gate; it is the absence of one, wearing a button.
//
// This binary requires POSTGRES_DSN. Possession of the database credential
// is the strongest authentication factor this deployment actually has, so
// the gate is put behind it. It is also honest about what it is: one
// operator, one machine, one database password — the same trust boundary
// `make backup` and `make migrate-up` already sit on.
//
// ── The tradeoff, stated plainly ───────────────────────────────────────
// It is less convenient than a button in Settings, and it does not
// generalise to a multi-user product. Both are true and neither matters
// yet: this is a single-operator system with no user model. The day real
// authentication exists, the right move is an authenticated route that
// calls the SAME app.ConfirmPairing — the application service takes a
// workspace and a code and knows nothing about how it was reached, so that
// change is a new adapter and not a new rule.
//
//	usage:
//	  telegramctl pair   -code <CODE> [-workspace <uuid>]
//	  telegramctl revoke -chat <telegram-chat-id>
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/telegram/adapters/repo"
	"github.com/corsi/backend/internal/integrations/telegram/app"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/workspace"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "telegramctl:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  telegramctl pair   -code <CODE> [-workspace <uuid>]
  telegramctl revoke -chat <telegram-chat-id>

POSTGRES_DSN must be set.`

func run() error {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		return fmt.Errorf("a subcommand is required")
	}

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("POSTGRES_DSN is not set")
	}

	switch os.Args[1] {
	case "pair":
		return pair(dsn, os.Args[2:])
	case "revoke":
		return revoke(dsn, os.Args[2:])
	default:
		fmt.Println(usage)
		return fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
}

func pair(dsn string, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	code := fs.String("code", "", "the pairing code shown by the bot")
	ws := fs.String("workspace", "", "workspace uuid (default: the dev sentinel)")
	_ = fs.Parse(args)

	if *code == "" {
		return fmt.Errorf("-code is required")
	}

	workspaceID := workspace.DevWorkspaceID
	if *ws != "" {
		parsed, err := uuid.Parse(*ws)
		if err != nil {
			return fmt.Errorf("-workspace must be a uuid: %w", err)
		}
		workspaceID = parsed
	} else {
		// Said out loud rather than assumed. The sentinel is what this
		// deployment's frontend sends, so it is almost certainly right —
		// and "almost certainly" is exactly the kind of thing worth
		// printing before somebody binds a phone to the wrong workspace.
		fmt.Printf("using the development sentinel workspace %s\n", workspaceID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svc, closePool, err := service(ctx, dsn)
	if err != nil {
		return err
	}
	defer closePool()

	b, err := svc.ConfirmPairing(ctx, workspaceID, *code)
	if err != nil {
		return err
	}
	// The binding id and the chat id, and nothing about the code. The code
	// is consumed and was never stored in a form anybody can read back.
	fmt.Printf("paired: telegram chat %d → workspace %s (binding %s)\n",
		b.TelegramChatID, b.WorkspaceID, b.ID)
	fmt.Println("send /agents in Telegram to choose an agent.")
	return nil
}

func revoke(dsn string, args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	chat := fs.Int64("chat", 0, "the telegram chat id to unbind")
	_ = fs.Parse(args)

	if *chat == 0 {
		return fmt.Errorf("-chat is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svc, closePool, err := service(ctx, dsn)
	if err != nil {
		return err
	}
	defer closePool()

	if err := svc.RevokePairing(ctx, *chat); err != nil {
		return err
	}
	fmt.Printf("revoked: telegram chat %d\n", *chat)
	fmt.Println("the conversations it used are untouched; they are ordinary C.O.R.S.I. threads.")
	return nil
}

// service builds only what the pairing paths need.
//
// ── Why this does not construct the Telegram module ────────────────────
// Because the module needs a bot token and a chat runtime, and pairing
// needs neither. Constructing them here would mean this tool required
// TELEGRAM_BOT_TOKEN to confirm a code — a secret in one more place, for
// no reason — and would give a CLI a live Bot API client it has no
// business holding.
//
// The application service is the same type the running process uses. Its
// Bot and Runtime fields are nil, and nothing on these two paths touches
// either: pairing reads and writes two tables and calls no capability, no
// agent and no gateway.
func service(ctx context.Context, dsn string) (app.Pairing, func(), error) {
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: dsn, MaxConns: 2, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		return nil, nil, err
	}
	repos := repo.New(pool)
	// A discarding logger. The service logs operational events; this is a
	// one-shot tool and its output is what it prints, not a log stream.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := app.NewService(app.Deps{
		Bindings: repos.Bindings,
		Pairings: repos.Pairings,
		Logger:   log,
	})
	return svc, pool.Close, nil
}
