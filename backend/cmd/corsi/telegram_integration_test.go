//go:build integration

// Telegram T1 — the end-to-end suite.
//
// ── Why this file is in cmd/corsi ──────────────────────────────────────
// Because the thing under test is the COMPOSITION: a phone reaching an
// agent through the chat runtime. That crosses Agents and Telegram, and
// the only place allowed to know both is the composition root.
//
// The GitHub suite makes the same argument and then plays the role from
// inside its own package, re-wiring by hand. This one does not have to:
// `telegramruntime.go` — the adapter that IS the arrow between the two —
// lives in `package main`, so a test in `package main` exercises the
// production adapter rather than a copy of it. A copy is precisely where
// "Telegram quietly stopped reading receipts" would hide.
//
// ── What is real here ──────────────────────────────────────────────────
// Postgres, both migration timelines, the Telegram repositories, the
// Telegram application service, the REAL Bot API client over a real
// socket, the real chat repositories, the real chat application service,
// the real tool registry, the real conversation and message tables, the
// real write and read receipts, and the real Safe Resume path.
//
// What is not real: Telegram itself, and the LLM gateway. Telegram is an
// httptest server speaking the Bot API from its published reference; the
// gateway is the same kind of scripted fake the chat suite uses. Both are
// declared boundaries.
//
// ── On test content ────────────────────────────────────────────────────
// Every message in this file is synthetic. Nothing here is the operator's
// real Palace content, real finance data or real anything: the acceptance
// flow asks an agent to remember buying coffee, which is a sentence
// invented for this test.
//
//	Run with:  TEST_POSTGRES_DSN=postgres://corsi:corsi@localhost:5432/postgres?sslmode=disable \
//	           go test -tags=integration ./cmd/corsi/
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	chatreferences "github.com/corsi/backend/internal/chat/adapters/references"
	chatrepo "github.com/corsi/backend/internal/chat/adapters/repo"
	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	tgbotapi "github.com/corsi/backend/internal/integrations/telegram/adapters/botapi"
	tgrepo "github.com/corsi/backend/internal/integrations/telegram/adapters/repo"
	tgapp "github.com/corsi/backend/internal/integrations/telegram/app"
	tgdomain "github.com/corsi/backend/internal/integrations/telegram/domain"
	tgports "github.com/corsi/backend/internal/integrations/telegram/ports"
	"github.com/corsi/backend/internal/palace"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
)

/* ── fixtures that are not credentials ───────────────────────────────── */

const (
	fixtureBotToken = "999999:FIXTURE-TELEGRAM-TOKEN-NOT-REAL"
	fixtureAPIKey   = "sk-fixture-not-a-real-key"
)

// Exactly 32 bytes before encoding, which AES-256 needs.
var fixtureSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// The Telegram identities. Numbers, because that is all this integration
// knows about a person.
const (
	operatorUserID = int64(100100)
	operatorChatID = int64(100100)
	strangerUserID = int64(900900)
	strangerChatID = int64(900900)
)

/* ── the throwaway database ──────────────────────────────────────────── */

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping telegram integration test")
	}
	return v
}

func freshDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin := adminDSN(t)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	name := fmt.Sprintf("corsi_telegram_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = conn.Close(ctx)
		t.Skipf("cannot create a throwaway database (needs CREATEDB): %v", err)
	}
	_ = conn.Close(ctx)

	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Logf("cleanup: connect: %v", err)
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(),
			`DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// migrateUp applies one timeline against its own version table. The
// `-table` equivalent is mandatory: omitting it makes golang-migrate read
// finance's version table and conclude there is nothing to apply.
func migrateUp(t *testing.T, dsn, dir, table string) {
	t.Helper()
	u, err := url.Parse(strings.Replace(dsn, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()

	m, err := migrate.New("file://../../migrations/"+dir, u.String())
	if err != nil {
		t.Fatalf("migrate.New(%s): %v", dir, err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up %s: %v", dir, err)
	}
}

/* ── the fake Telegram ───────────────────────────────────────────────── */

// fakeTelegram speaks enough of the Bot API for this integration, and
// records everything it was asked to send.
//
// An HTTP server rather than a stubbed ports.BotAPI, for the reason the
// GitHub suite gives: the client is the part most likely to be wrong, and
// a test that replaced it would prove only that our abstraction calls our
// abstraction.
type fakeTelegram struct {
	srv *httptest.Server

	mu   sync.Mutex
	sent []sentMessage
	// authPaths is every request path. The token assertions read it.
	authPaths []string
}

type sentMessage struct {
	ChatID  int64
	Text    string
	Buttons []sentButton
}

type sentButton struct {
	Label string
	Data  string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.authPaths = append(f.authPaths, r.URL.Path)
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)

	switch {
	case strings.HasSuffix(r.URL.Path, "/getMe"):
		_ = enc.Encode(map[string]any{"ok": true, "result": map[string]any{
			"id": 4242, "username": "corsi_fixture_bot", "is_bot": true,
		}})

	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		var body struct {
			ChatID      int64  `json:"chat_id"`
			Text        string `json:"text"`
			ReplyMarkup *struct {
				InlineKeyboard [][]struct {
					Text         string `json:"text"`
					CallbackData string `json:"callback_data"`
				} `json:"inline_keyboard"`
			} `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Telegram's own rule, enforced here so a violation fails the test
		// instead of failing on somebody's phone.
		if body.Text == "" {
			_ = enc.Encode(map[string]any{"ok": false, "error_code": 400,
				"description": "Bad Request: message text is empty"})
			return
		}
		if len([]rune(body.Text)) > tgdomain.MaxMessageUnits {
			_ = enc.Encode(map[string]any{"ok": false, "error_code": 400,
				"description": "Bad Request: message is too long"})
			return
		}
		m := sentMessage{ChatID: body.ChatID, Text: body.Text}
		if body.ReplyMarkup != nil {
			for _, row := range body.ReplyMarkup.InlineKeyboard {
				for _, b := range row {
					if len(b.CallbackData) > tgdomain.MaxCallbackBytes {
						_ = enc.Encode(map[string]any{"ok": false, "error_code": 400,
							"description": "Bad Request: BUTTON_DATA_INVALID"})
						return
					}
					m.Buttons = append(m.Buttons, sentButton{Label: b.Text, Data: b.CallbackData})
				}
			}
		}
		f.mu.Lock()
		f.sent = append(f.sent, m)
		f.mu.Unlock()
		_ = enc.Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": len(f.sent)}})

	case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
		_ = enc.Encode(map[string]any{"ok": true, "result": true})

	default:
		_ = enc.Encode(map[string]any{"ok": true, "result": []any{}})
	}
}

func (f *fakeTelegram) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeTelegram) reset() {
	f.mu.Lock()
	f.sent = nil
	f.mu.Unlock()
}

// last returns the most recent message, failing the test when there is
// none — a silent bot is a failure, not an empty slice.
func (f *fakeTelegram) last(t *testing.T) sentMessage {
	t.Helper()
	msgs := f.messages()
	if len(msgs) == 0 {
		t.Fatal("the bot sent nothing")
	}
	return msgs[len(msgs)-1]
}

func (f *fakeTelegram) allText() string {
	var b strings.Builder
	for _, m := range f.messages() {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

/* ── the fake LLM ────────────────────────────────────────────────────── */

type fakeLLM struct {
	mu       sync.Mutex
	rounds   [][]chatports.StreamEvent
	calls    int
	requests []chatports.CompletionRequest
	// gate, when set, blocks the FIRST Stream call until it is closed.
	// It is how a test holds one turn open long enough for a second
	// message to arrive — the honest way, through the gateway's own
	// latency rather than a sleep in the code under test.
	gate    chan struct{}
	running int
	// failWith, when set, makes the next provider call fail. It is how
	// the error-redaction assertion gets a REAL gateway failure whose
	// text contains exactly the internal detail that must not travel.
	failWith error
}

// failNext makes the next provider call return err.
func (f *fakeLLM) failNext(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = err
}

// hold makes the next turn block inside the provider call until `release`
// is closed, then answer with `script`.
func (f *fakeLLM) hold(release chan struct{}, script []chatports.StreamEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate = release
	f.rounds = [][]chatports.StreamEvent{script}
	f.calls = 0
}

// inFlight reports whether a provider call is currently blocked or
// running. Read by the concurrency test to know the first turn has really
// started before it sends the second message.
func (f *fakeLLM) inFlight() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running > 0
}

func (f *fakeLLM) script(rounds ...[]chatports.StreamEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rounds = rounds
	f.calls = 0
}

func (f *fakeLLM) Stream(_ context.Context, req chatports.CompletionRequest) (chatports.Stream, error) {
	f.mu.Lock()
	f.calls++
	f.running++
	f.requests = append(f.requests, req)
	script := answer("ok")
	if len(f.rounds) > 0 {
		if f.calls <= len(f.rounds) {
			script = f.rounds[f.calls-1]
		} else {
			script = f.rounds[len(f.rounds)-1]
		}
	}
	gate := f.gate
	f.gate = nil
	if f.failWith != nil {
		err := f.failWith
		f.failWith = nil
		f.running--
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()

	if gate != nil {
		<-gate
	}
	return &fakeStream{script: script, done: f.finished}, nil
}

// finished is called when a stream is closed, so inFlight goes back to
// false only once the turn has really left the provider.
func (f *fakeLLM) finished() {
	f.mu.Lock()
	if f.running > 0 {
		f.running--
	}
	f.mu.Unlock()
}

func (f *fakeLLM) Models(context.Context, chatports.Credentials) ([]chatports.Model, error) {
	return []chatports.Model{{ID: "test-model"}}, nil
}

func (f *fakeLLM) ModelPrices(context.Context, chatports.Credentials) (map[string]chatports.Price, error) {
	return map[string]chatports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}}, nil
}

func (f *fakeLLM) KeyInfo(context.Context, chatports.Credentials) (chatports.KeySpend, error) {
	return chatports.KeySpend{KeyAlias: "fixture"}, nil
}

// lastPrompt returns the messages of the most recent provider call. The
// context-isolation assertions read it: what the model was shown is the
// only place "context crossover" could actually happen.
func (f *fakeLLM) lastPrompt(t *testing.T) []chatports.ChatMessage {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("the provider was never called")
	}
	return f.requests[len(f.requests)-1].Messages
}

type fakeStream struct {
	script []chatports.StreamEvent
	i      int
	done   func()
	closed bool
}

func (s *fakeStream) Recv() (chatports.StreamEvent, error) {
	if s.i >= len(s.script) {
		return chatports.StreamEvent{}, io.EOF
	}
	ev := s.script[s.i]
	s.i++
	return ev, nil
}

func (s *fakeStream) Close() error {
	if !s.closed {
		s.closed = true
		if s.done != nil {
			s.done()
		}
	}
	return nil
}

func answer(text string) []chatports.StreamEvent {
	return []chatports.StreamEvent{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 40, CompletionTokens: 12}},
	}
}

func askTool(id, name, args string) []chatports.StreamEvent {
	return []chatports.StreamEvent{
		{FinishReason: "tool_calls",
			ToolCalls: []chatdomain.ToolCall{{ID: id, Name: chatdomain.ToolName(name), Arguments: args}},
			Usage:     &chatports.Usage{PromptTokens: 30, CompletionTokens: 8}},
	}
}

/* ── the environment ─────────────────────────────────────────────────── */

type env struct {
	t   *testing.T
	tg  *fakeTelegram
	llm *fakeLLM

	pool    *pgxpool.Pool
	chatSvc *chatapp.Service
	tgSvc   *tgapp.Service
	tgRepos *tgrepo.Repos

	// Two workspaces, always. Every isolation assertion reads wsA's data
	// back as wsB.
	wsA uuid.UUID
	wsB uuid.UUID
}

// newEnv wires exactly what run() wires, with two substitutions: the LLM
// gateway and Telegram's own server.
func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := freshDatabase(t)
	migrateUp(t, dsn, "chat", "schema_migrations_chat")
	migrateUp(t, dsn, "telegram", "schema_migrations_telegram")
	// Palace, because the suite needs at least ONE capability that
	// resolves its workspace from the context. See newEnv.
	migrateUp(t, dsn, "palace", "schema_migrations_palace")

	ctx := context.Background()
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sealer, err := secrets.New(secrets.Config{Key: fixtureSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// Agents, wired the way run() wires it. InternalTools is ON so the
	// suite has a real capability to execute — `system.echo` is a WRITE-
	// effect-free tool, which is exactly what the receipt assertions need
	// to distinguish from a real write.
	// ── Why Palace is wired into this suite ────────────────────────
	//
	// The first version of this harness registered only the INTERNAL
	// diagnostic tools, and `system.echo` is the one capability in the
	// product that does not call workspaceOf. So the suite proved the tool
	// loop ran and proved nothing about whether a real capability could
	// resolve anything — and a real `palace.artifact.create` from a phone
	// refused with "this call arrived without a workspace" on the first
	// live write.
	//
	// A capability that reads the context is therefore part of the
	// harness, not an optional extra: it is the only thing that exercises
	// the seam between a transport and the tool executor.
	llm := &fakeLLM{}
	palaceMod := palace.New(palace.Deps{Pool: pool, Logger: log})
	registry := chattools.MustNew(chattools.Options{
		Internal: true,
		Extra:    palaceMod.Tools(),
	})
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		llm, sealer, registry, chatreferences.MustNew(), log)

	// Telegram, with its REAL Bot API client pointed at the fake server —
	// the same substitution `github.connections.api_base_url` makes, so
	// nothing about the production path is bypassed to make this testable.
	tg := newFakeTelegram(t)
	bot, err := tgbotapi.New(tgbotapi.Options{
		Token: fixtureBotToken, BaseURL: tg.srv.URL, Logger: log,
	})
	if err != nil {
		t.Fatalf("build bot client: %v", err)
	}
	repos := tgrepo.New(pool)
	tgSvc := tgapp.NewService(tgapp.Deps{
		Bindings:   repos.Bindings,
		ChatAgents: repos.ChatAgents,
		Pairings:   repos.Pairings,
		Bot:        bot,
		// THE arrow. The production adapter, not a copy.
		Runtime: newTelegramRuntime(chatSvc, log),
		Logger:  log,
	})

	return &env{
		t: t, tg: tg, llm: llm, pool: pool,
		chatSvc: chatSvc, tgSvc: tgSvc, tgRepos: repos,
		wsA: uuid.New(), wsB: uuid.New(),
	}
}

/* ── driving the bot ─────────────────────────────────────────────────── */

var updateSeq int64

func nextUpdateID() int64 { updateSeq++; return updateSeq }

// send drives one inbound text message through the whole stack.
func (e *env) send(userID, chatID int64, text string) {
	e.t.Helper()
	err := e.tgSvc.HandleUpdate(context.Background(), tgports.Update{
		UpdateID: nextUpdateID(),
		Message: &tgports.InboundMessage{
			MessageID: nextUpdateID(), TelegramUserID: userID,
			TelegramChatID: chatID, ChatType: "private", Text: text,
		},
	})
	// An error here is the LOG's, not the user's — the handler has already
	// told the person whatever they needed. It is surfaced as a log line
	// so a failing assertion below has context.
	if err != nil {
		e.t.Logf("HandleUpdate returned: %v", err)
	}
}

// press drives one inline button press.
func (e *env) press(userID, chatID int64, data string) {
	e.t.Helper()
	err := e.tgSvc.HandleUpdate(context.Background(), tgports.Update{
		UpdateID: nextUpdateID(),
		Callback: &tgports.InboundCallback{
			CallbackID: uuid.NewString(), TelegramUserID: userID,
			TelegramChatID: chatID, ChatType: "private", Data: data,
		},
	})
	if err != nil {
		e.t.Logf("HandleUpdate(callback) returned: %v", err)
	}
}

/* ── fixtures ────────────────────────────────────────────────────────── */

// seedAgent creates a provider and an agent in a workspace.
func (e *env) seedAgent(ws uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	p, err := e.chatSvc.CreateProvider(ctx, chatapp.CreateProviderInput{
		WorkspaceID: ws, Name: "LiteLLM " + name, BaseURL: "https://gateway.invalid/v1",
		APIKey: fixtureAPIKey, DefaultModel: "test-model",
	})
	if err != nil {
		e.t.Fatalf("create provider: %v", err)
	}
	a, err := e.chatSvc.CreateAgent(ctx, chatapp.CreateAgentInput{
		WorkspaceID: ws, ProviderID: p.ID, Name: name,
		Description: "agente de teste", SystemPrompt: "você é " + name,
		Model: "test-model",
	})
	if err != nil {
		e.t.Fatalf("create agent: %v", err)
	}
	return a.ID
}

// pair runs the real pairing flow end to end: the bot issues a code, and
// the operator side confirms it.
//
// The code is read back from the MESSAGE the bot sent, not from a return
// value, because that is the only place it legitimately exists — which is
// also the assertion that the operator can actually complete this flow.
func (e *env) pair(ws uuid.UUID, userID, chatID int64) {
	e.t.Helper()
	e.tg.reset()
	e.send(userID, chatID, "/start")
	code := extractCode(e.t, e.tg.last(e.t).Text)
	if _, err := e.tgSvc.ConfirmPairing(context.Background(), ws, code); err != nil {
		e.t.Fatalf("ConfirmPairing: %v", err)
	}
	e.tg.reset()
}

// extractCode finds the pairing code in the bot's message.
//
// It looks for the only token shaped like one, rather than an index into
// the copy: a test that depended on the exact wording would break every
// time somebody improved a sentence.
func extractCode(t *testing.T, text string) string {
	t.Helper()
	for _, field := range strings.Fields(text) {
		normalized := tgdomain.NormalizeCode(field)
		if len(normalized) != 10 {
			continue
		}
		ok := true
		for _, r := range normalized {
			if !strings.ContainsRune("23456789ABCDEFGHJKMNPQRSTVWXYZ", r) {
				ok = false
				break
			}
		}
		if ok {
			return field
		}
	}
	t.Fatalf("no pairing code in the bot's message: %q", text)
	return ""
}

// selectAgent presses the button for a named agent in the last /agents
// keyboard.
func (e *env) selectAgent(userID, chatID int64, name string) {
	e.t.Helper()
	e.send(userID, chatID, "/agents")
	for _, b := range e.tg.last(e.t).Buttons {
		if strings.Contains(b.Label, name) {
			e.tg.reset()
			e.press(userID, chatID, b.Data)
			return
		}
	}
	e.t.Fatalf("no button for agent %q in %+v", name, e.tg.last(e.t).Buttons)
}

/* ── reading the database directly ───────────────────────────────────── */

// conversationOf returns the C.O.R.S.I. conversation this Telegram chat
// uses for one agent, or uuid.Nil.
func (e *env) conversationOf(chatID int64, agentID uuid.UUID) uuid.UUID {
	e.t.Helper()
	var conv uuid.UUID
	err := e.pool.QueryRow(context.Background(),
		`SELECT ca.conversation_id
		   FROM telegram.chat_agents ca
		   JOIN telegram.bindings b ON b.id = ca.binding_id
		  WHERE b.telegram_chat_id = $1 AND ca.agent_id = $2`,
		chatID, agentID).Scan(&conv)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil
	}
	if err != nil {
		e.t.Fatalf("read conversation mapping: %v", err)
	}
	return conv
}

func (e *env) transcript(ws, conv uuid.UUID) []chatdomain.Message {
	e.t.Helper()
	msgs, err := e.chatSvc.ListMessages(context.Background(), ws, conv, 200)
	if err != nil {
		e.t.Fatalf("list messages: %v", err)
	}
	return msgs
}

/* ── seeding helpers that reach the chat runtime directly ────────────── */

// grantEcho authorizes the built-in diagnostic capability on an agent.
//
// `system.echo` is the only tool in the catalogue this suite can execute
// without standing up another bounded context, and its EFFECT is a read —
// which is exactly why it is useful here: a turn that ran it produces
// tool-call audit rows and NO write receipt, so the two receipt
// assertions can tell "something ran" from "something changed".
func grantEcho(t *testing.T, e *env, ws, agent uuid.UUID) {
	t.Helper()
	if err := e.chatSvc.AuthorizeTool(context.Background(), ws, agent, chatdomain.ToolName("system.echo")); err != nil {
		t.Fatalf("authorize system.echo: %v", err)
	}
}

func createConversationIn(ws, agent uuid.UUID) chatapp.CreateConversationInput {
	return chatapp.CreateConversationInput{WorkspaceID: ws, AgentID: agent}
}

func sendTo(ws, conv uuid.UUID, text string) chatapp.SendMessageInput {
	return chatapp.SendMessageInput{WorkspaceID: ws, ConversationID: conv, Content: text}
}
