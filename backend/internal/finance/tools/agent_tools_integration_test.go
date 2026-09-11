//go:build integration

// Finance × Agent — the foundation suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/finance/tools/...
//
// The sentence this suite has to make convincing:
//
//	An agent, using only the generic tool infrastructure and the grants it
//	was given, can read and maintain the financial record through
//	conversation — with the money arriving intact, the workspace holding,
//	and an agent without those grants able to do none of it.
//
// What is REAL here: the Finance schema, domain, repositories and
// application service; the Finance tools; the Agents tool registry; the
// authorization store; the schema validator; the four-gate executor; the
// turn loop; the audit trail; and Postgres. What is FAKED is the LLM,
// because the point of this file is to control which tool calls arrive, not
// to test that a model produces them.
//
// That division is deliberate and it is also a limit worth stating plainly:
// nothing in this file can prove that a model declines to record a
// hypothesis, because the model here is a script. Those are behavioural
// claims about a real model against a real gateway, and they live in
// ledger_live_test.go behind its own build tag.
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
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
	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// ── Why this suite runs in a database of its own ──────────────────────
//
// It needs TWO schemas: the Finance tables the tools write to, and the chat
// tables the turn loop writes to. Resetting either in the shared database
// would drop the schema out from under another package's suite running
// concurrently — and the finance one in particular, whose own suite lives
// one directory up and migrates the same tables. So it creates its own
// database, once, and resets schemas inside it.
const privateDBName = "corsi_test_finance_agent"

var suiteDSN string

func TestMain(m *testing.M) {
	admin := os.Getenv("TEST_POSTGRES_DSN")
	if admin == "" {
		os.Exit(m.Run())
	}
	d, cleanup, err := createPrivateDB(admin)
	if err != nil {
		panic("finance agent suite: " + err.Error())
	}
	suiteDSN = d
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func createPrivateDB(admin string) (string, func(), error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+privateDBName+` WITH (FORCE)`); err != nil {
		return "", nil, err
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+privateDBName); err != nil {
		return "", nil, err
	}

	u, err := url.Parse(admin)
	if err != nil {
		return "", nil, err
	}
	u.Path = "/" + privateDBName
	target := u.String()

	// This suite provisions its own database, so it is the party that knows
	// the database is disposable, and it says so at the moment it creates
	// it. That is what lets dsn() below refuse anything else.
	if err := testdb.Mark(ctx, target); err != nil {
		return "", nil, err
	}
	return target, func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+privateDBName+` WITH (FORCE)`)
	}, nil
}

func dsn(t *testing.T) string {
	if suiteDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping integration test")
	}
	testdb.AssertDestructible(t, suiteDSN)
	return suiteDSN
}

// migrateDSN points golang-migrate at one context's own version table.
//
// An empty table name leaves the driver's default, which is what Finance
// uses — it predates the split. Omitting it for any module added since is
// the documented way to break one: the runner reads finance's table, finds
// it populated, and concludes there is nothing to apply.
func migrateDSN(t *testing.T, d, table string) string {
	t.Helper()
	for _, p := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(d, p) {
			d = "pgx5://" + strings.TrimPrefix(d, p)
			break
		}
	}
	u, err := url.Parse(d)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if table != "" {
		q := u.Query()
		q.Set("x-migrations-table", table)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func migrateUp(t *testing.T, d, dir, table string) {
	t.Helper()
	m, err := migrate.New("file://../../../migrations/"+dir, migrateDSN(t, d, table))
	if err != nil {
		t.Fatalf("migrate.New(%s): %v", dir, err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up (%s): %v", dir, err)
	}
}

func freshDB(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS finance CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations`,
		`DROP SCHEMA IF EXISTS chat CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_chat`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close reset conn: %v", err)
	}
	migrateUp(t, d, "finance", "")
	migrateUp(t, d, "chat", "schema_migrations_chat")
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool

	svc      *app.Service
	chatSvc  *chatapp.Service
	llm      *fakeLLM
	registry *chattools.Registry
	loc      *time.Location

	// Two workspaces, always. Every isolation assertion reads wsA's money
	// back as wsB.
	wsA uuid.UUID
	wsB uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dsn(t)
	freshDB(t, d)

	ctx := context.Background()
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// Finance, wired the way module.go wires it.
	svc := app.NewService(repo.New(pool), postgres.NewTxManager(pool), log)

	// The Agents stack, wired the way cmd/corsi wires it — including the
	// seam the Finance tools arrive through. `Internal: false` is the
	// production catalogue, so nothing here depends on system.echo.
	registry := chattools.MustNew(chattools.Options{Extra: New(svc, loc)})
	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}
	llm := newFakeLLM()
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		llm, sealer, registry,
		chatreferences.MustNew(NewReferenceResolver(svc, loc)), log)

	return &env{
		t: t, pool: pool, svc: svc, chatSvc: chatSvc,
		llm: llm, registry: registry, loc: loc,
		wsA: uuid.New(), wsB: uuid.New(),
	}
}

func ctxFor(ws uuid.UUID) context.Context {
	return workspace.WithWorkspaceID(context.Background(), ws)
}

/* ── seeding, through the application layer ──────────────────────────── */

// The fixtures are created through the same service the tools call, never
// through SQL: a seed that used its own INSERT could drift from the rules a
// write actually passes, and the suite would be proving things about a
// database state the product cannot produce.

func (e *env) seedCategory(ws uuid.UUID, name string, ty domain.EntryType) *domain.Category {
	e.t.Helper()
	c, err := e.svc.CreateCategory(ctxFor(ws), app.CreateCategoryInput{
		WorkspaceID: ws, Name: name, Type: ty, Color: "#123456", Icon: "tag",
	})
	if err != nil {
		e.t.Fatalf("seed category %s: %v", name, err)
	}
	return c
}

func (e *env) seedTx(ws uuid.UUID, cat *domain.Category, cents int64, desc string, when time.Time) *domain.Transaction {
	e.t.Helper()
	tx, err := e.svc.CreateTransaction(ctxFor(ws), app.CreateTransactionInput{
		WorkspaceID: ws, CategoryID: cat.ID, AmountCents: cents,
		Description: desc, OccurredAt: when,
	})
	if err != nil {
		e.t.Fatalf("seed transaction %s: %v", desc, err)
	}
	return tx
}

// today is midday in the reporting zone — the same instant the create tool
// stamps a dated row with, so fixtures and tool writes land in the same
// windows.
func (e *env) today() time.Time {
	n := time.Now().In(e.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, e.loc)
}

func (e *env) daysAgo(n int) time.Time { return e.today().AddDate(0, 0, -n) }

/* ── reading the database directly ───────────────────────────────────── */

// rawAmount reads the stored column, bypassing every Go type in between.
//
// The suite's central claim is about what is IN the database, and a
// readback through the same service that wrote it would agree with a
// consistent mistake. This does not.
func (e *env) rawAmount(id uuid.UUID) (int64, bool) {
	var cents int64
	err := e.pool.QueryRow(context.Background(),
		`SELECT amount_cents FROM finance.transactions WHERE id = $1`, id).Scan(&cents)
	if err != nil {
		return 0, false
	}
	return cents, true
}

func (e *env) rawRow(id uuid.UUID) (source string, deletedAt *time.Time, ok bool) {
	err := e.pool.QueryRow(context.Background(),
		`SELECT source::text, deleted_at FROM finance.transactions WHERE id = $1`, id).
		Scan(&source, &deletedAt)
	if err != nil {
		return "", nil, false
	}
	return source, deletedAt, true
}

func (e *env) liveCount(ws uuid.UUID) int {
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.transactions WHERE workspace_id = $1 AND deleted_at IS NULL`,
		ws).Scan(&n); err != nil {
		e.t.Fatalf("count: %v", err)
	}
	return n
}

/* ── the agent ───────────────────────────────────────────────────────── */

// newAgent creates a provider, an agent and a conversation. It is the
// ordinary path: nothing here is special to Finance, and the name is passed
// in so a test can prove that a name grants nothing.
func (e *env) newAgent(ws uuid.UUID, name string) (agentID, conversationID uuid.UUID) {
	e.t.Helper()
	ctx := ctxFor(ws)
	provider, err := e.chatSvc.CreateProvider(ctx, chatapp.CreateProviderInput{
		WorkspaceID: ws, Name: "fixture", BaseURL: "https://gateway.invalid",
		APIKey: "sk-test", DefaultModel: "test-model",
	})
	if err != nil {
		e.t.Fatalf("create provider: %v", err)
	}
	agent, err := e.chatSvc.CreateAgent(ctx, chatapp.CreateAgentInput{
		WorkspaceID: ws, ProviderID: provider.ID, Name: name,
		SystemPrompt: "you keep the financial record accurate",
	})
	if err != nil {
		e.t.Fatalf("create agent: %v", err)
	}
	conv, err := e.chatSvc.CreateConversation(ctx, chatapp.CreateConversationInput{
		WorkspaceID: ws, AgentID: agent.ID, Title: "t",
	})
	if err != nil {
		e.t.Fatalf("create conversation: %v", err)
	}
	return agent.ID, conv.ID
}

func (e *env) newConversationWith(ws, agentID uuid.UUID, refs ...chatdomain.ContextReference) uuid.UUID {
	e.t.Helper()
	conv, err := e.chatSvc.CreateConversation(ctxFor(ws), chatapp.CreateConversationInput{
		WorkspaceID: ws, AgentID: agentID, Title: "t", ContextReferences: refs,
	})
	if err != nil {
		e.t.Fatalf("create conversation with references: %v", err)
	}
	return conv.ID
}

func (e *env) authorize(ws, agentID uuid.UUID, names ...chatdomain.ToolName) {
	e.t.Helper()
	for _, n := range names {
		if err := e.chatSvc.AuthorizeTool(ctxFor(ws), ws, agentID, n); err != nil {
			e.t.Fatalf("authorize %s: %v", n, err)
		}
	}
}

func (e *env) revoke(ws, agentID uuid.UUID, names ...chatdomain.ToolName) {
	e.t.Helper()
	for _, n := range names {
		if err := e.chatSvc.RevokeTool(ctxFor(ws), ws, agentID, n); err != nil {
			e.t.Fatalf("revoke %s: %v", n, err)
		}
	}
}

// allFinanceTools is the grant set a financial agent is given. Named once
// so a test that means "fully authorized" cannot drift from the catalogue.
var allFinanceTools = []chatdomain.ToolName{
	TransactionListTool, TransactionGetTool, TransactionCreateTool,
	TransactionUpdateTool, TransactionDeleteTool, CategoryListTool, SummaryGetTool,
}

/* ── the fake LLM ────────────────────────────────────────────────────── */

type fakeLLM struct {
	rounds      [][]chatports.StreamEvent
	streamCalls int
	requests    []chatports.CompletionRequest
}

func newFakeLLM() *fakeLLM {
	return &fakeLLM{rounds: [][]chatports.StreamEvent{{
		{Delta: "ok"},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}}}
}

func (f *fakeLLM) scriptReply(text string) {
	f.streamCalls = 0
	f.requests = nil
	f.rounds = [][]chatports.StreamEvent{{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}}
}

func (f *fakeLLM) scriptToolCall(name chatdomain.ToolName, args string) {
	f.streamCalls = 0
	f.requests = nil
	f.rounds = [][]chatports.StreamEvent{
		{
			{FinishReason: "tool_calls", Usage: &chatports.Usage{PromptTokens: 20, CompletionTokens: 10},
				ToolCalls: []chatdomain.ToolCall{{ID: "call_1", Name: name, Arguments: args}}},
		},
		{
			{Delta: "pronto"},
			{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 30, CompletionTokens: 8}},
		},
	}
}

func (f *fakeLLM) Stream(_ context.Context, req chatports.CompletionRequest) (chatports.Stream, error) {
	f.streamCalls++
	f.requests = append(f.requests, req)
	idx := f.streamCalls - 1
	if idx >= len(f.rounds) {
		idx = len(f.rounds) - 1
	}
	return &fakeStream{script: f.rounds[idx]}, nil
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

type fakeStream struct {
	script []chatports.StreamEvent
	i      int
}

func (s *fakeStream) Recv() (chatports.StreamEvent, error) {
	if s.i >= len(s.script) {
		return chatports.StreamEvent{}, io.EOF
	}
	ev := s.script[s.i]
	s.i++
	return ev, nil
}

func (s *fakeStream) Close() error { return nil }

type collectSink struct {
	text  strings.Builder
	tools []chatapp.ToolEvent
}

func (c *collectSink) Reasoning(string) error { return nil }
func (c *collectSink) Delta(t string) error   { c.text.WriteString(t); return nil }
func (c *collectSink) Tool(ev chatapp.ToolEvent) error {
	c.tools = append(c.tools, ev)
	return nil
}

func (c *collectSink) finished(name chatdomain.ToolName) (chatapp.ToolEvent, bool) {
	for i := len(c.tools) - 1; i >= 0; i-- {
		if c.tools[i].Name == string(name) && c.tools[i].Status != "running" {
			return c.tools[i], true
		}
	}
	return chatapp.ToolEvent{}, false
}

func (e *env) turn(ws, convID uuid.UUID, content string) *collectSink {
	e.t.Helper()
	sink := &collectSink{}
	if _, err := e.chatSvc.SendMessage(ctxFor(ws), chatapp.SendMessageInput{
		WorkspaceID: ws, ConversationID: convID, Content: content,
	}, sink); err != nil {
		e.t.Fatalf("send message: %v", err)
	}
	return sink
}

/* ── direct tool execution ───────────────────────────────────────────── */

// execute runs one tool the way the executor runs it: arguments are
// serialised, validated against the declared schema, and only then handed
// over. A test that called Execute directly would be skipping the gate that
// catches half the mistakes a model makes — including, here, a decimal
// amount.
func (e *env) execute(t *testing.T, ws uuid.UUID, name chatdomain.ToolName, args map[string]any) map[string]any {
	t.Helper()
	out, err := e.executeErr(ws, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (e *env) executeErr(ws uuid.UUID, name chatdomain.ToolName, args map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		e.t.Fatalf("marshal args: %v", err)
	}
	return e.executeRaw(ws, name, string(raw))
}

// executeRaw takes the arguments as the model would have produced them, so
// a test can send something the Go type system would not let it build —
// a decimal where an integer is declared, above all.
func (e *env) executeRaw(ws uuid.UUID, name chatdomain.ToolName, raw string) (map[string]any, error) {
	tool, ok := e.registry.Lookup(name)
	if !ok {
		e.t.Fatalf("%s is not registered", name)
	}
	validated, err := tool.Definition().Schema.ValidateArguments(raw)
	if err != nil {
		return nil, err
	}
	out, err := tool.Execute(ctxFor(ws), validated)
	if err != nil {
		return nil, err
	}
	// Round-tripped through JSON because that is what the model receives.
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

/* ── small readers ───────────────────────────────────────────────────── */

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("field %q is missing or not a string in %v", key, m)
	}
	return v
}

func num(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("field %q is missing or not a number in %v", key, m)
	}
	return int64(v)
}

func rows(t *testing.T, m map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := m[key].([]any)
	if !ok {
		t.Fatalf("field %q is missing or not a list in %v", key, m)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		row, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("row is not an object: %v", r)
		}
		out = append(out, row)
	}
	return out
}

func nested(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("field %q is missing or not an object in %v", key, m)
	}
	return v
}

func toolCode(err error) chatdomain.ToolErrorCode {
	var f *chatdomain.ToolFailure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}
