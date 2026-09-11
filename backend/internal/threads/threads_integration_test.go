//go:build integration

// Threads × Content Agent — the foundation suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/threads/...
//
// The sentence this suite has to make convincing:
//
//	An agent, using only the generic tool infrastructure and the grants it
//	was given, can capture an idea, evolve it into a finished piece and
//	remove it — and an agent without those grants cannot.
//
// What is REAL here: the Threads schema, domain, repository, service and
// tools; the Agents tool registry; the authorization store; the schema
// validator; the four-gate executor; the turn loop; the audit trail; and
// Postgres. What is faked: the LLM, because the point is to control which
// tool calls arrive, not to test that a model produces them. A real model
// against a real gateway is the E2E, and it is a separate exercise.
//
// ── Why this file lives in internal/threads and imports chat ───────────
// The dependency it needs is the one that already exists: threads/tools
// satisfies chat/ports.Tool. Putting the suite in internal/chat would mean
// the Agents module importing Threads to test itself, which is the arrow
// the architecture forbids. Here the arrow points the way it already does.
package threads

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
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
	"github.com/corsi/backend/internal/threads/adapters/repo"
	"github.com/corsi/backend/internal/threads/app"
	"github.com/corsi/backend/internal/threads/domain"
	thports "github.com/corsi/backend/internal/threads/ports"
	thtools "github.com/corsi/backend/internal/threads/tools"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// testSecretsKey is exactly 32 bytes before encoding, which is what AES-256
// needs. It seals the chat provider credential this suite creates; nothing
// here depends on its value.
var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// ── Why this suite runs in a database of its own ──────────────────────
//
// It needs TWO schemas: the Threads tables the tools write to, and the chat
// tables the turn loop writes to. Resetting `chat` in the shared database
// would drop the schema out from under the Agents suite running
// concurrently in another process. So it creates its own database, once,
// and resets schemas inside it — the same arrangement, and the same
// reasoning, as the Job Radar suite.
const privateDBName = "corsi_test_threads"

// suiteDSN points at the database this package owns. Set by TestMain.
var suiteDSN string

func TestMain(m *testing.M) {
	admin := os.Getenv("TEST_POSTGRES_DSN")
	if admin == "" {
		// Nothing to set up; every test skips individually.
		os.Exit(m.Run())
	}

	d, cleanup, err := createPrivateDB(admin)
	if err != nil {
		panic("threads suite: " + err.Error())
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

	// Dropped first: a previous run killed mid-way leaves it behind, and a
	// stale database would be worse than no isolation at all.
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
	// it. That is what lets dsn() below refuse anything else without
	// carving out an exception for this package.
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

// dsn is where the suite learns which database it may destroy. This is the
// only way to obtain one, so no destructive statement can be reached
// without it.
func dsn(t *testing.T) string {
	if suiteDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping integration test")
	}
	testdb.AssertDestructible(t, suiteDSN)
	return suiteDSN
}

// migrateDSN points golang-migrate at one context's own version table.
//
// Omitting the table is the documented way to break a new module: the
// runner would read finance's table, find it populated, and conclude there
// is nothing to apply — creating no schema at all.
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
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()
	return u.String()
}

func migrateUp(t *testing.T, d, dir, table string) {
	t.Helper()
	m, err := migrate.New("file://../../migrations/"+dir, migrateDSN(t, d, table))
	if err != nil {
		t.Fatalf("migrate.New(%s): %v", dir, err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up (%s): %v", dir, err)
	}
}

// freshDB drops both schemas this suite touches and migrates them forward.
func freshDB(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS threads CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_threads`,
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

	migrateUp(t, d, "threads", "schema_migrations_threads")
	migrateUp(t, d, "chat", "schema_migrations_chat")
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool

	// svc is the Threads service the tools use. Held so a test can seed
	// through the same application layer a tool writes through, rather than
	// through SQL that could drift from it.
	svc *app.Service
	// chatSvc is the real Agents service: registry, authorization store and
	// turn loop included.
	chatSvc *chatapp.Service
	llm     *fakeLLM
	// registry is the real tool catalogue, built exactly as the composition
	// root builds it.
	registry *chattools.Registry

	// Two workspaces, always. Every isolation assertion reads wsA's data
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

	// Threads, wired the way module.go wires it.
	svc := app.NewService(repo.New(pool).Threads, log)

	// The Agents stack, wired the way cmd/corsi wires it — including the
	// seam the Threads tools arrive through. `Internal: false` is the
	// production catalogue, so nothing here depends on system.echo.
	registry := chattools.MustNew(chattools.Options{Extra: thtools.New(svc)})
	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}
	llm := newFakeLLM()
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		llm, sealer, registry,
		chatreferences.MustNew(thtools.NewReferenceResolver(svc)), log)

	return &env{
		t: t, pool: pool, svc: svc, chatSvc: chatSvc,
		llm: llm, registry: registry,
		wsA: uuid.New(), wsB: uuid.New(),
	}
}

// ctxFor builds the context a tool executes under: one carrying a
// workspace, exactly as the middleware produces for a real request.
func ctxFor(ws uuid.UUID) context.Context {
	return workspace.WithWorkspaceID(context.Background(), ws)
}

/* ── seeding ─────────────────────────────────────────────────────────── */

// seed creates one thread through the application layer.
func (e *env) seed(ws uuid.UUID, title, content string, status domain.Status) *domain.Thread {
	e.t.Helper()
	th, err := e.svc.CreateThread(ctxFor(ws), ws, app.CreateInput{
		Title: title, Content: content, Status: &status,
	})
	if err != nil {
		e.t.Fatalf("seed %s: %v", title, err)
	}
	return th
}

/* ── the agent ───────────────────────────────────────────────────────── */

// newAgent creates a provider, an agent and a conversation, and returns the
// ids the turn loop needs. It is the ordinary path: nothing here is special
// to Threads, and no agent is named "Content" — the whole point is that the
// name has no meaning to the system.
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
		SystemPrompt: "you help with content",
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

// newConversationWith opens a conversation already about one entity.
func (e *env) newConversationWith(ws, agentID uuid.UUID, refs ...chatdomain.ContextReference) uuid.UUID {
	e.t.Helper()
	conv, err := e.chatSvc.CreateConversation(ctxFor(ws), chatapp.CreateConversationInput{
		WorkspaceID: ws, AgentID: agentID, Title: "t",
		ContextReferences: refs,
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

// allThreadTools is the grant set a content agent is given. Named once so a
// test that means "fully authorized" cannot drift from the catalogue.
var allThreadTools = []chatdomain.ToolName{
	thtools.ThreadListTool, thtools.ThreadGetTool, thtools.ThreadCreateTool,
	thtools.ThreadUpdateTool, thtools.ThreadDeleteTool,
}

/* ── the fake LLM ────────────────────────────────────────────────────── */

// fakeLLM replays a scripted sequence of rounds. Round n is what the nth
// provider call returns, which is how a tool-calling turn is expressed: the
// first round asks for a tool, the second answers in prose.
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

// scriptReply makes the turn answer in prose without calling anything.
func (f *fakeLLM) scriptReply(text string) {
	f.streamCalls = 0
	f.requests = nil
	f.rounds = [][]chatports.StreamEvent{{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}}
}

// scriptToolCall makes round 1 ask for one tool and round 2 answer.
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

// collectSink records what a turn emitted. The tool events are what say
// which capability actually ran and how it ended.
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

// finished returns the terminal tool event for a call, which is the one
// carrying ok/error and the error code.
func (c *collectSink) finished(name chatdomain.ToolName) (chatapp.ToolEvent, bool) {
	for i := len(c.tools) - 1; i >= 0; i-- {
		if c.tools[i].Name == string(name) && c.tools[i].Status != "running" {
			return c.tools[i], true
		}
	}
	return chatapp.ToolEvent{}, false
}

// turn runs one real turn through the real loop.
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
// catches half the mistakes a model makes.
func (e *env) execute(t *testing.T, ws uuid.UUID, name chatdomain.ToolName, args map[string]any) map[string]any {
	t.Helper()
	out, err := e.executeErr(ws, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (e *env) executeErr(ws uuid.UUID, name chatdomain.ToolName, args map[string]any) (map[string]any, error) {
	tool, ok := e.registry.Lookup(name)
	if !ok {
		e.t.Fatalf("%s is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		e.t.Fatalf("marshal args: %v", err)
	}
	validated, err := tool.Definition().Schema.ValidateArguments(string(raw))
	if err != nil {
		return nil, err
	}
	out, err := tool.Execute(ctxFor(ws), validated)
	if err != nil {
		return nil, err
	}
	// Round-tripped through JSON because that is what the model receives:
	// asserting on the Go map would let a value that cannot be encoded pass.
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

// liveThreads counts what a workspace can actually see. Read through the
// application layer, so a soft-deleted row that the repository still
// returns would fail here rather than pass quietly.
func (e *env) liveThreads(ws uuid.UUID) []*domain.Thread {
	e.t.Helper()
	items, _, err := e.svc.ListThreads(ctxFor(ws), ws, portsFilter())
	if err != nil {
		e.t.Fatalf("list threads: %v", err)
	}
	return items
}

// portsFilter is the zero filter: every live thread of the workspace.
func portsFilter() thports.ThreadFilter { return thports.ThreadFilter{} }

/* ── 1. the registry ─────────────────────────────────────────────────── */

// The five capabilities must exist in the catalogue a production binary
// builds — not behind the internal-tools switch.
func TestThreadsToolsAreInTheRegistry(t *testing.T) {
	e := newEnv(t)

	// The whole catalogue, exhaustively. The count check at the bottom is
	// what makes this a gate rather than a spot check: a tool added without
	// a line here fails, which is the moment to decide whether an agent
	// should be able to do that at all.
	want := map[chatdomain.ToolName]chatdomain.ToolEffect{
		thtools.ThreadListTool:   chatdomain.EffectRead,
		thtools.ThreadGetTool:    chatdomain.EffectRead,
		thtools.ThreadCreateTool: chatdomain.EffectWrite,
		thtools.ThreadUpdateTool: chatdomain.EffectWrite,
		thtools.ThreadDeleteTool: chatdomain.EffectWrite,
	}

	got := map[chatdomain.ToolName]chatdomain.ToolEffect{}
	for _, d := range e.registry.Definitions() {
		if strings.HasPrefix(string(d.Name), "threads.") {
			got[d.Name] = d.Effect
		}
	}

	for name, effect := range want {
		gotEffect, ok := got[name]
		if !ok {
			t.Fatalf("%s is not in the registry", name)
		}
		// The effect is what the interface shows a person before they grant
		// a capability. A write declared as a read would be the module lying
		// in the one field that exists to warn them.
		if gotEffect != effect {
			t.Errorf("%s effect = %q, want %q", name, gotEffect, effect)
		}
		if _, resolvable := e.registry.Lookup(name); !resolvable {
			t.Errorf("%s does not resolve to an executor", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("registry has %d threads tools, want %d: %v", len(got), len(want), got)
	}
}

// Every definition must survive the domain's own validation — that is what
// the composition root enforces at start-up, and a tool that failed it
// would take the whole binary down rather than degrade quietly.
func TestThreadsToolDefinitionsAreValid(t *testing.T) {
	for _, tool := range thtools.New(nil) {
		if err := tool.Definition().Validate(); err != nil {
			t.Errorf("%s: %v", tool.Definition().Name, err)
		}
	}
}

// The status vocabulary the model is told must be the one the domain
// enforces. Two lists that drift produce a tool whose description invites
// exactly the argument its parser refuses.
func TestTheStatusArgumentsDescribeTheDomainStatuses(t *testing.T) {
	seen := 0
	for _, tool := range thtools.New(nil) {
		prop, ok := tool.Definition().Schema.Properties["status"]
		if !ok {
			continue
		}
		seen++
		for _, name := range domain.StatusNames() {
			if !strings.Contains(prop.Description, name) {
				t.Errorf("%s: the status argument does not mention %q: %s",
					tool.Definition().Name, name, prop.Description)
			}
		}
	}
	if seen != 3 {
		t.Errorf("%d tools declare a status argument, want 3 (list, create, update)", seen)
	}
}

// The namespace is `threads`, and it is the namespace an operator sees
// grouped on the agent's settings page. It must not collide with one that
// already exists.
func TestTheNamespaceIsItsOwn(t *testing.T) {
	e := newEnv(t)
	for _, d := range e.registry.Definitions() {
		if d.Name.Namespace() == "threads" && !strings.HasPrefix(string(d.Name), "threads.thread.") {
			t.Errorf("%s claims the threads namespace and is not a thread capability", d.Name)
		}
	}
}

/* ── 2. authorization ────────────────────────────────────────────────── */

// Registered is not authorized. This is the property the whole design
// exists for, and it is asserted through the real turn loop rather than
// against the store: what matters is that the EXECUTOR refuses, not that a
// row is absent.
func TestAnUnauthorizedAgentCannotCreateAThread(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "unauthorized")
	_ = agentID

	e.llm.scriptToolCall(thtools.ThreadCreateTool,
		`{"title":"Microservices cedo demais","content":"ideia solta"}`)
	sink := e.turn(e.wsA, convID, "guarda essa ideia")

	ev, ok := sink.finished(thtools.ThreadCreateTool)
	if !ok {
		t.Fatal("no terminal tool event for create")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Errorf("error code = %q, want %q", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}
	// And — the part that matters — nothing was written.
	if got := len(e.liveThreads(e.wsA)); got != 0 {
		t.Fatalf("%d threads exist after an unauthorized create", got)
	}
}

// A new capability must not appear on an existing agent by surprise. Deny
// by default is the rule, and "the tool exists" has never been permission.
func TestEveryThreadsToolStartsUnauthorized(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "existing")

	report, err := e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agentID)
	if err != nil {
		t.Fatalf("agent tools: %v", err)
	}
	seen := 0
	for _, item := range report.Items {
		if item.Name.Namespace() != "threads" {
			continue
		}
		seen++
		if item.Authorized {
			t.Errorf("%s was authorized without anyone granting it", item.Name)
		}
	}
	if seen != len(allThreadTools) {
		t.Errorf("the agent's tool report lists %d threads tools, want %d", seen, len(allThreadTools))
	}
}

// A grant is per capability, not per module. An agent that may read must
// not thereby be able to write.
func TestAReadGrantDoesNotAuthorizeAWrite(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "reader")
	e.authorize(e.wsA, agentID, thtools.ThreadListTool, thtools.ThreadGetTool)

	e.llm.scriptToolCall(thtools.ThreadCreateTool, `{"title":"t","content":"c"}`)
	sink := e.turn(e.wsA, convID, "guarda isso")

	ev, _ := sink.finished(thtools.ThreadCreateTool)
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Errorf("error code = %q, want %q", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}
	if got := len(e.liveThreads(e.wsA)); got != 0 {
		t.Fatalf("%d threads exist; a read grant wrote one", got)
	}
}

// An agent literally named "Content" gets nothing for its name. Capability
// comes from a grant and from nowhere else — this test is what makes the
// absence of `if agent.name == "Content"` a property rather than a habit.
func TestAnAgentNamedContentIsNotPrivileged(t *testing.T) {
	e := newEnv(t)
	_, convID := e.newAgent(e.wsA, "Content")

	e.llm.scriptToolCall(thtools.ThreadCreateTool, `{"title":"t","content":"c"}`)
	sink := e.turn(e.wsA, convID, "guarda isso")

	ev, _ := sink.finished(thtools.ThreadCreateTool)
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("an agent called Content was allowed to write; error code = %q", ev.ErrorCode)
	}
}

// Revoking stops it, on the next turn, without anything else changing.
func TestRevokingAGrantStopsTheCapability(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allThreadTools...)

	e.llm.scriptToolCall(thtools.ThreadCreateTool, `{"title":"Antes","content":"texto"}`)
	if ev, _ := e.turn(e.wsA, convID, "guarda").finished(thtools.ThreadCreateTool); ev.ErrorCode != "" {
		t.Fatalf("the authorized create failed: %+v", ev)
	}

	if err := e.chatSvc.RevokeTool(ctxFor(e.wsA), e.wsA, agentID, thtools.ThreadCreateTool); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	e.llm.scriptToolCall(thtools.ThreadCreateTool, `{"title":"Depois","content":"texto"}`)
	ev, _ := e.turn(e.wsA, convID, "guarda outra").finished(thtools.ThreadCreateTool)
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Errorf("error code after revoke = %q, want %q", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}
	if got := len(e.liveThreads(e.wsA)); got != 1 {
		t.Fatalf("%d threads exist, want the 1 created before the revoke", got)
	}
}

/* ── 3. create ───────────────────────────────────────────────────────── */

// The minimum a person actually says: an idea, and a handle for it.
func TestCreateCapturesAnIdea(t *testing.T) {
	e := newEnv(t)

	out := e.execute(t, e.wsA, thtools.ThreadCreateTool, map[string]any{
		"title":   "Microservices cedo demais",
		"content": "Por que microservices cedo demais podem prejudicar a velocidade de startups.",
	})
	id, _ := out["thread_id"].(string)
	if id == "" {
		t.Fatalf("no id came back: %+v", out)
	}
	if out["created"] != true {
		t.Errorf("created = %v", out["created"])
	}

	// The id is immediately usable, which is the point of returning it: the
	// next thing the model does is update what it just made.
	got := e.execute(t, e.wsA, thtools.ThreadGetTool, map[string]any{"thread_id": id})
	th, _ := got["thread"].(map[string]any)
	if th["title"] != "Microservices cedo demais" {
		t.Errorf("title = %v", th["title"])
	}
	if !strings.Contains(th["content"].(string), "velocidade de startups") {
		t.Errorf("content = %v", th["content"])
	}
}

// Absent status means idea, which is where a captured thought belongs. The
// tool does not get its own opinion about where a new thread starts.
func TestCreateWithoutAStatusLandsInIdea(t *testing.T) {
	e := newEnv(t)
	out := e.execute(t, e.wsA, thtools.ThreadCreateTool, map[string]any{
		"title": "Microservices cedo demais", "content": "ideia",
	})
	if out["status"] != "idea" {
		t.Fatalf("status = %v, want idea", out["status"])
	}
}

// When the user says where they already are, the thread is born there.
func TestCreateCanStartFurtherAlong(t *testing.T) {
	e := newEnv(t)
	out := e.execute(t, e.wsA, thtools.ThreadCreateTool, map[string]any{
		"title": "Post que já publiquei", "content": "texto", "status": "published",
	})
	if out["status"] != "published" {
		t.Fatalf("status = %v", out["status"])
	}
}

// An invented status is refused as an invalid ARGUMENT, so the model can
// fix it, and the message names the vocabulary so it does not guess again.
func TestCreateRefusesAnInventedStatus(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, thtools.ThreadCreateTool, map[string]any{
		"title": "t", "content": "c", "status": "scheduled",
	})
	var fail *chatdomain.ToolFailure
	if !errors.As(err, &fail) || fail.Code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("error = %v, want invalid arguments", err)
	}
	for _, name := range domain.StatusNames() {
		if !strings.Contains(fail.Message, name) {
			t.Errorf("the refusal does not name %q: %s", name, fail.Message)
		}
	}
}

// The schema requires both. A create with no content would be a thread the
// user cannot recognise later by anything but its handle.
func TestCreateRequiresATitleAndContent(t *testing.T) {
	e := newEnv(t)
	for _, args := range []map[string]any{
		{"content": "c"},
		{"title": "t"},
	} {
		if _, err := e.executeErr(e.wsA, thtools.ThreadCreateTool, args); err == nil {
			t.Errorf("create(%v) was accepted", args)
		}
	}
}

/* ── 4. update is the core ───────────────────────────────────────────── */

// The whole product story in one test: idea → LinkedIn post → new hook →
// review. One thread throughout, and the persisted text really changes.
func TestContentEvolvesOnOneThread(t *testing.T) {
	e := newEnv(t)

	created := e.execute(t, e.wsA, thtools.ThreadCreateTool, map[string]any{
		"title":   "Microservices cedo demais",
		"content": "Microservices cedo demais podem matar a velocidade de uma startup.",
	})
	id := created["thread_id"].(string)

	// Turn 2: becomes a draft post.
	draftA := "Você não tem um problema de escala. Você tem um problema de organização.\n\n" +
		"Microservices resolvem o segundo custando o primeiro."
	out := e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": id, "content": draftA, "status": "draft",
	})
	if out["content_changed"] != true || out["status_changed"] != true {
		t.Fatalf("the update did not report what it moved: %+v", out)
	}
	if out["previous_status"] != "idea" {
		t.Errorf("previous_status = %v, want idea", out["previous_status"])
	}

	// Turn 3: a new hook. Same thread, different text.
	draftB := "Microservices são a forma mais cara de adiar uma decisão de produto.\n\n" +
		"Microservices resolvem o segundo custando o primeiro."
	e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": id, "content": draftB,
	})

	// Turn 4: ready to be read.
	out = e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": id, "status": "review",
	})
	if out["content_changed"] != false {
		t.Error("marking for review rewrote the text")
	}

	// One thread, and it holds the latest version.
	live := e.liveThreads(e.wsA)
	if len(live) != 1 {
		t.Fatalf("%d threads exist; the edits created copies", len(live))
	}
	if live[0].Content != draftB {
		t.Errorf("stored content is not the latest draft:\n%q", live[0].Content)
	}
	if live[0].Status != domain.StatusReview {
		t.Errorf("status = %q, want review", live[0].Status)
	}
	if live[0].ID.String() != id {
		t.Errorf("the surviving thread is not the one that was created")
	}
}

// An update naming one field leaves every other field alone. This is the
// grammar the tool promises, and it is what stops "marca como review" from
// touching a word of the draft.
func TestAnUpdateOnlyTouchesWhatItNames(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Título original", "texto original", domain.StatusDraft)

	e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": th.ID.String(), "status": "review",
	})

	got := e.execute(t, e.wsA, thtools.ThreadGetTool,
		map[string]any{"thread_id": th.ID.String()})["thread"].(map[string]any)
	if got["title"] != "Título original" || got["content"] != "texto original" {
		t.Fatalf("a status-only update changed something else: %+v", got)
	}
}

// The one that protects the user's work. An empty content is refused
// rather than stored, because storing it would erase the piece and report
// success — and a model sends one by accident far more often than a user
// asks for one.
func TestAnUpdateCannotEmptyTheContent(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Rascunho", "quatro parágrafos de trabalho real", domain.StatusDraft)

	for _, blank := range []string{"", "   ", "\n"} {
		_, err := e.executeErr(e.wsA, thtools.ThreadUpdateTool, map[string]any{
			"thread_id": th.ID.String(), "content": blank,
		})
		var fail *chatdomain.ToolFailure
		if !errors.As(err, &fail) || fail.Code != chatdomain.ToolErrInvalidArguments {
			t.Fatalf("content=%q was accepted or failed wrongly: %v", blank, err)
		}
	}

	reread, err := e.svc.GetThread(ctxFor(e.wsA), e.wsA, th.ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if reread.Content != "quatro parágrafos de trabalho real" {
		t.Fatalf("the work was destroyed: %q", reread.Content)
	}
}

// An update that changes nothing is refused before it reaches the row.
// Accepting it would move updated_at, which reorders every listing — an
// edit that did nothing announcing itself as the most recent work.
func TestAnUpdateThatNamesNoFieldIsRefused(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Rascunho", "texto", domain.StatusDraft)

	_, err := e.executeErr(e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": th.ID.String(),
	})
	if err == nil {
		t.Fatal("an update with no fields was accepted")
	}
}

// Re-sending the values a thread already holds is honest about having done
// nothing, and does not move updated_at.
func TestAnUpdateThatMovesNothingSaysSo(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Rascunho", "texto", domain.StatusDraft)
	before := th.UpdatedAt

	out := e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": th.ID.String(), "status": "draft", "content": "texto",
	})
	if out["unchanged"] != true {
		t.Errorf("a no-op update did not declare itself: %+v", out)
	}

	after, err := e.svc.GetThread(ctxFor(e.wsA), e.wsA, th.ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if !after.UpdatedAt.Equal(before) {
		t.Errorf("updated_at moved on an edit that changed nothing: %v → %v", before, after.UpdatedAt)
	}
}

// A fabricated id fails as something the model can recover from — list and
// try again — not as an execution failure that reads like a broken tool.
func TestUpdatingAThreadThatDoesNotExistIsRecoverable(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": uuid.New().String(), "status": "review",
	})
	var fail *chatdomain.ToolFailure
	if !errors.As(err, &fail) || fail.Code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("error = %v, want a recoverable invalid-arguments failure", err)
	}
}

// A string that is not an id at all is a different mistake from an id that
// resolves to nothing, and telling the model "not found" would send it
// looking for a record instead of fixing its argument.
func TestAMalformedIDIsReportedAsABadArgument(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, thtools.ThreadGetTool, map[string]any{"thread_id": "aquele de microservices"})
	var fail *chatdomain.ToolFailure
	if !errors.As(err, &fail) || fail.Code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(fail.Message, "threads.thread.list") {
		t.Errorf("the refusal does not tell the model how to get a real id: %s", fail.Message)
	}
}

/* ── 5. list ─────────────────────────────────────────────────────────── */

// "Quais conteúdos eu tenho em revisão?" — the question a status exists to
// answer.
func TestListNarrowsByStatus(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Uma ideia", "", domain.StatusIdea)
	inReview := e.seed(e.wsA, "Microservices cedo demais", "texto pronto", domain.StatusReview)
	e.seed(e.wsA, "Um rascunho", "meio texto", domain.StatusDraft)

	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{"status": "review"})
	rows, _ := out["threads"].([]any)
	if len(rows) != 1 {
		t.Fatalf("%d rows for status=review, want 1: %+v", len(rows), out)
	}
	row := rows[0].(map[string]any)
	if row["id"] != inReview.ID.String() {
		t.Errorf("the wrong thread came back: %+v", row)
	}
	if out["matching_total"] != float64(1) {
		t.Errorf("matching_total = %v", out["matching_total"])
	}
}

// The listing is how a model resolves "aquele de microservices" into an id.
func TestListFindsAThreadByText(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Por que builders vencem executores", "texto", domain.StatusDraft)
	target := e.seed(e.wsA, "Microservices cedo demais", "texto", domain.StatusDraft)

	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{"search": "microservices"})
	rows, _ := out["threads"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != target.ID.String() {
		t.Fatalf("search did not resolve the reference: %+v", out)
	}
}

// Search reaches the body too: a person remembering a sentence rather than
// the handle is asking a correct question.
func TestListSearchesTheContentAsWellAsTheTitle(t *testing.T) {
	e := newEnv(t)
	target := e.seed(e.wsA, "Sem título óbvio", "kubernetes não é uma estratégia", domain.StatusDraft)

	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{"search": "kubernetes"})
	rows, _ := out["threads"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != target.ID.String() {
		t.Fatalf("a search over the body found nothing: %+v", out)
	}
}

// An ambiguous reference must come back as several rows, so the model asks
// instead of guessing. This is the mechanism section 12 of the brief
// depends on: `list` is what makes "pega o de microservices" answerable.
func TestAnAmbiguousReferenceReturnsEveryCandidate(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Microservices cedo demais", "a", domain.StatusDraft)
	e.seed(e.wsA, "Microservices e Kubernetes", "b", domain.StatusDraft)
	e.seed(e.wsA, "Microservices em startups", "c", domain.StatusDraft)

	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{"search": "microservices"})
	rows, _ := out["threads"].([]any)
	if len(rows) != 3 {
		t.Fatalf("%d candidates, want 3: %+v", len(rows), out)
	}
}

// Most recently worked on first. Content work is returned to, so the thread
// touched an hour ago is the one being asked about.
func TestListIsOrderedByMostRecentWork(t *testing.T) {
	e := newEnv(t)
	first := e.seed(e.wsA, "Primeira", "a", domain.StatusIdea)
	e.seed(e.wsA, "Segunda", "b", domain.StatusIdea)

	// Touch the older one.
	e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": first.ID.String(), "status": "draft",
	})

	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{})
	rows, _ := out["threads"].([]any)
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	if rows[0].(map[string]any)["id"] != first.ID.String() {
		t.Errorf("the thread edited last is not first: %+v", rows)
	}
}

// A listing carries an excerpt so two drafts of the same piece can be told
// apart, and it declares when it cut — an excerpt that did not would be
// indistinguishable from a very short thread, and the model would rewrite a
// draft from its first sentence.
func TestAListingExcerptDeclaresWhenItIsPartial(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("palavra ", 200)
	e.seed(e.wsA, "Rascunho longo", long, domain.StatusDraft)
	e.seed(e.wsA, "Rascunho curto", "uma linha", domain.StatusDraft)

	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{})
	byTitle := map[string]map[string]any{}
	for _, raw := range out["threads"].([]any) {
		row := raw.(map[string]any)
		byTitle[row["title"].(string)] = row
	}

	if byTitle["Rascunho longo"]["excerpt_truncated"] != true {
		t.Errorf("a cut excerpt did not declare itself: %+v", byTitle["Rascunho longo"])
	}
	if byTitle["Rascunho curto"]["excerpt_truncated"] != false {
		t.Errorf("a complete excerpt claimed to be cut: %+v", byTitle["Rascunho curto"])
	}
	if byTitle["Rascunho curto"]["excerpt"] != "uma linha" {
		t.Errorf("excerpt = %v", byTitle["Rascunho curto"]["excerpt"])
	}
}

// `get` returns the WHOLE text. A model editing from an abbreviated version
// would hand back a truncated piece believing it was complete.
func TestGetReturnsTheCompleteText(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("palavra ", 500)
	th := e.seed(e.wsA, "Rascunho longo", long, domain.StatusDraft)

	got := e.execute(t, e.wsA, thtools.ThreadGetTool,
		map[string]any{"thread_id": th.ID.String()})["thread"].(map[string]any)
	if got["content"] != long {
		t.Fatalf("get returned %d characters of %d", len(got["content"].(string)), len(long))
	}
}

// An empty result says what to do next rather than looking like a failure.
func TestAnEmptyListingExplainsItself(t *testing.T) {
	e := newEnv(t)
	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{"status": "published"})
	if len(out["threads"].([]any)) != 0 {
		t.Fatal("rows came back from an empty workspace")
	}
	if _, ok := out["note"]; !ok {
		t.Errorf("an empty listing carried no note: %+v", out)
	}
}

/* ── 6. delete ───────────────────────────────────────────────────────── */

// An explicit, unambiguous request executes.
func TestDeleteRemovesAThreadTheUserAskedToRemove(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Era só um teste", "texto", domain.StatusIdea)

	out := e.execute(t, e.wsA, thtools.ThreadDeleteTool, map[string]any{"thread_id": th.ID.String()})
	if out["deleted"] != true {
		t.Fatalf("delete = %+v", out)
	}
	// It names what it removed, so the model acknowledges the piece rather
	// than echoing a uuid at the user.
	if out["title"] != "Era só um teste" {
		t.Errorf("the result does not name what was removed: %+v", out)
	}
	if got := len(e.liveThreads(e.wsA)); got != 0 {
		t.Fatalf("%d threads still visible", got)
	}
}

// SOFT delete, and the same operation any other surface would call. The row
// survives; content acquires links, and a hard delete would turn a
// published post's id and a conversation's context reference into dangling
// references.
func TestDeleteIsSoftAndTheRowSurvives(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Apagada", "texto que não deve sumir do disco", domain.StatusDraft)
	e.execute(t, e.wsA, thtools.ThreadDeleteTool, map[string]any{"thread_id": th.ID.String()})

	var deletedAt *time.Time
	var content string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT deleted_at, content FROM threads.threads WHERE id = $1`, th.ID,
	).Scan(&deletedAt, &content); err != nil {
		t.Fatalf("read the row directly: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at was not stamped")
	}
	if content != "texto que não deve sumir do disco" {
		t.Errorf("the text was destroyed: %q", content)
	}
}

// Deleting twice is a not-found rather than a silent success, which is what
// lets a caller tell "I removed it" from "it was already gone".
func TestDeletingTwiceIsNotASilentSuccess(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Apagada", "texto", domain.StatusIdea)
	e.execute(t, e.wsA, thtools.ThreadDeleteTool, map[string]any{"thread_id": th.ID.String()})

	if _, err := e.executeErr(e.wsA, thtools.ThreadDeleteTool,
		map[string]any{"thread_id": th.ID.String()}); err == nil {
		t.Fatal("the second delete reported success")
	}
}

// A deleted thread is gone from every read path, not just the listing.
func TestADeletedThreadIsUnreachable(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Apagada", "texto", domain.StatusIdea)
	e.execute(t, e.wsA, thtools.ThreadDeleteTool, map[string]any{"thread_id": th.ID.String()})

	for _, name := range []chatdomain.ToolName{thtools.ThreadGetTool, thtools.ThreadUpdateTool} {
		args := map[string]any{"thread_id": th.ID.String()}
		if name == thtools.ThreadUpdateTool {
			args["status"] = "draft"
		}
		if _, err := e.executeErr(e.wsA, name, args); err == nil {
			t.Errorf("%s still reaches a deleted thread", name)
		}
	}
}

/* ── 7. negative semantics ───────────────────────────────────────────── */

// The sharpest one in the suite. "Esse texto ficou ruim" is FEEDBACK, and
// the response to feedback is a rewrite. A model that conflated it with
// removal would destroy work the user spent time on and did not ask to
// lose.
//
// The tool cannot enforce this — it is a decision made before the call — so
// what is asserted is the two things that CAN be: the description tells the
// model plainly, and a turn in which the model correctly reads it as
// feedback leaves the thread standing.
func TestANegativeJudgementIsNotARemoval(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allThreadTools...)
	th := e.seed(e.wsA, "Microservices cedo demais", "um rascunho fraco", domain.StatusDraft)

	// The model reads it as criticism and answers in prose. Nothing is
	// called, and nothing is removed.
	e.llm.scriptReply("Entendi. Quer que eu reescreva com um ângulo mais concreto?")
	sink := e.turn(e.wsA, convID, "Esse texto ficou ruim.")

	if len(sink.tools) != 0 {
		t.Fatalf("a negative judgement triggered %d tool calls: %+v", len(sink.tools), sink.tools)
	}
	live := e.liveThreads(e.wsA)
	if len(live) != 1 || live[0].ID != th.ID {
		t.Fatalf("the thread did not survive being criticised: %+v", live)
	}
	if live[0].Status != domain.StatusDraft {
		t.Errorf("status = %q; criticism moved the lifecycle", live[0].Status)
	}
}

// And the instruction that makes the above the likely reading is actually
// in the contract the model receives — named explicitly, because
// describing only the right reading and hoping is what leaves the wrong one
// available.
func TestTheDeleteToolWarnsAgainstTreatingCriticismAsRemoval(t *testing.T) {
	for _, tool := range thtools.New(nil) {
		if tool.Definition().Name != thtools.ThreadDeleteTool {
			continue
		}
		desc := tool.Definition().DeclaredDescription()
		for _, phrase := range []string{"ficou ruim", "archived", "threads.thread.update"} {
			if !strings.Contains(desc, phrase) {
				t.Errorf("the delete description does not mention %q", phrase)
			}
		}
		// And the generic write notice is on it, because it changes data.
		if !strings.Contains(desc, "CHANGES data") {
			t.Error("the write notice is missing from a write tool")
		}
		return
	}
	t.Fatal("delete tool not found")
}

// The update tool has to hold the other boundary: an instruction is not
// content. A model that stored "deixa mais provocativo" as the text would
// replace the work with a note about the work, and report success.
func TestTheUpdateToolTellsTheModelToWriteTheNewVersionItself(t *testing.T) {
	for _, tool := range thtools.New(nil) {
		if tool.Definition().Name != thtools.ThreadUpdateTool {
			continue
		}
		desc := tool.Definition().Description
		for _, phrase := range []string{"WRITE THE NEW VERSION YOURSELF", "Never send their instruction"} {
			if !strings.Contains(desc, phrase) {
				t.Errorf("the update description does not carry %q", phrase)
			}
		}
		return
	}
	t.Fatal("update tool not found")
}

/* ── 8. workspace isolation ──────────────────────────────────────────── */

// Every operation is workspace-scoped, and a cross-workspace id is
// indistinguishable from one that never existed. Told apart, the difference
// would confirm that another workspace holds that row.
func TestNoOperationCrossesAWorkspace(t *testing.T) {
	e := newEnv(t)
	mine := e.seed(e.wsA, "Meu conteúdo", "texto", domain.StatusDraft)

	cases := []struct {
		name chatdomain.ToolName
		args map[string]any
	}{
		{thtools.ThreadGetTool, map[string]any{"thread_id": mine.ID.String()}},
		{thtools.ThreadUpdateTool, map[string]any{"thread_id": mine.ID.String(), "status": "review"}},
		{thtools.ThreadDeleteTool, map[string]any{"thread_id": mine.ID.String()}},
	}
	for _, c := range cases {
		// As wsB, wsA's id must fail.
		_, err := e.executeErr(e.wsB, c.name, c.args)
		if err == nil {
			t.Fatalf("%s reached another workspace's thread", c.name)
		}
		// And it must fail EXACTLY as a fabricated id does.
		fabricated := map[string]any{}
		for k, v := range c.args {
			fabricated[k] = v
		}
		fabricated["thread_id"] = uuid.New().String()
		_, other := e.executeErr(e.wsB, c.name, fabricated)
		if other == nil {
			t.Fatalf("%s accepted a fabricated id", c.name)
		}
		if err.Error() == other.Error() {
			continue
		}
		// The ids differ, so the messages differ. What must match is the
		// SHAPE of the refusal: same code, both saying "not found".
		var a, b *chatdomain.ToolFailure
		if !errors.As(err, &a) || !errors.As(other, &b) || a.Code != b.Code {
			t.Errorf("%s: a cross-workspace id (%v) is distinguishable from a fabricated one (%v)",
				c.name, err, other)
		}
	}

	// And the thread was not touched by any of it.
	reread, err := e.svc.GetThread(ctxFor(e.wsA), e.wsA, mine.ID)
	if err != nil {
		t.Fatalf("the thread is gone: %v", err)
	}
	if reread.Status != domain.StatusDraft {
		t.Errorf("status = %q; another workspace moved it", reread.Status)
	}
}

// A listing never leaks across the boundary either.
func TestAListingOnlyEverSeesItsOwnWorkspace(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Conteúdo do A", "texto", domain.StatusDraft)
	e.seed(e.wsB, "Conteúdo do B", "texto", domain.StatusDraft)

	out := e.execute(t, e.wsB, thtools.ThreadListTool, map[string]any{})
	rows := out["threads"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["title"] != "Conteúdo do B" {
		t.Fatalf("wsB sees %+v", rows)
	}
	// The unbounded total must be scoped too: a count that saw both would
	// tell wsB that content it cannot read exists.
	if out["matching_total"] != float64(1) {
		t.Errorf("matching_total = %v, want 1", out["matching_total"])
	}
}

// The model has no way to name a workspace. There is no argument for it in
// any schema, which is what makes the isolation structural rather than a
// rule somebody remembers to apply.
func TestNoToolAcceptsAWorkspaceArgument(t *testing.T) {
	for _, tool := range thtools.New(nil) {
		for name := range tool.Definition().Schema.Properties {
			if strings.Contains(strings.ToLower(name), "workspace") {
				t.Errorf("%s declares a %q argument", tool.Definition().Name, name)
			}
		}
	}
}

// A call that arrives without a workspace refuses rather than defaulting.
// There is no fallback value that is not somebody's real work.
func TestACallWithoutAWorkspaceIsRefused(t *testing.T) {
	e := newEnv(t)
	tool, _ := e.registry.Lookup(thtools.ThreadListTool)
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("a call with no workspace on the context was served")
	}
}

/* ── 9. the audit trail ──────────────────────────────────────────────── */

// Tool execution must land in the EXISTING audit trail. The sprint forbids
// a second audit system inside Threads, so this asserts the first one
// actually covers all three writes — with everything a later question needs
// to be answerable: which tool, what input, what result, success or
// failure, which round, how long, which conversation.
func TestEveryWriteIsRecordedInTheExistingAuditTrail(t *testing.T) {
	e := newEnv(t)
	agent, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agent, allThreadTools...)

	// create
	e.llm.scriptToolCall(thtools.ThreadCreateTool,
		`{"title":"Microservices cedo demais","content":"ideia solta"}`)
	e.turn(e.wsA, conv, "guarda essa ideia")

	id := e.liveThreads(e.wsA)[0].ID.String()

	// update
	e.llm.scriptToolCall(thtools.ThreadUpdateTool,
		`{"thread_id":"`+id+`","content":"um post de verdade","status":"draft"}`)
	e.turn(e.wsA, conv, "transforma num post")

	// delete
	e.llm.scriptToolCall(thtools.ThreadDeleteTool, `{"thread_id":"`+id+`"}`)
	e.turn(e.wsA, conv, "pode apagar")

	records, err := e.chatSvc.ConversationToolCalls(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}

	byName := map[chatdomain.ToolName]*chatdomain.ToolCallRecord{}
	for i := range records {
		byName[records[i].ToolName] = &records[i]
	}

	for _, name := range []chatdomain.ToolName{
		thtools.ThreadCreateTool, thtools.ThreadUpdateTool, thtools.ThreadDeleteTool,
	} {
		rec, ok := byName[name]
		if !ok {
			t.Fatalf("%s was not recorded in chat.tool_calls", name)
		}
		if rec.Status != chatdomain.ToolCallOK {
			t.Errorf("%s: status = %q", name, rec.Status)
		}
		// The arguments are what make the record answer "what was changed".
		if rec.Arguments == nil || strings.TrimSpace(*rec.Arguments) == "" {
			t.Errorf("%s: the record carries no arguments", name)
		}
		// And the result is what makes it answer "what happened".
		if rec.Result == nil || strings.TrimSpace(*rec.Result) == "" {
			t.Errorf("%s: the record carries no result", name)
		}
		if rec.ConversationID != conv {
			t.Errorf("%s: conversation = %v, want %v", name, rec.ConversationID, conv)
		}
		if rec.Round < 1 {
			t.Errorf("%s: round = %d, want >= 1", name, rec.Round)
		}
		if rec.DurationMS < 0 {
			t.Errorf("%s: duration = %d", name, rec.DurationMS)
		}
		if rec.ProviderCallID == "" {
			t.Errorf("%s: no provider call id, so nothing correlates with the gateway's log", name)
		}
	}

	// The update's record has to carry the id it acted on, or "which thread
	// was rewritten" is unanswerable after the fact.
	if !strings.Contains(*byName[thtools.ThreadUpdateTool].Arguments, id) {
		t.Errorf("the update record does not name the thread: %v", *byName[thtools.ThreadUpdateTool].Arguments)
	}
}

// A refusal is recorded too, and recorded AS a refusal. An audit trail that
// only holds successes cannot answer "did anything try".
func TestARefusedCallIsAudited(t *testing.T) {
	e := newEnv(t)
	_, conv := e.newAgent(e.wsA, "unauthorized")

	e.llm.scriptToolCall(thtools.ThreadCreateTool, `{"title":"t","content":"c"}`)
	e.turn(e.wsA, conv, "guarda isso")

	records, err := e.chatSvc.ConversationToolCalls(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("%d records, want 1", len(records))
	}
	// An unauthorized call is refused before it runs, so the audit says
	// not_executed. The property this test guards is unchanged: the grant
	// was absent and nothing happened.
	if records[0].Status != chatdomain.ToolCallNotExecuted {
		t.Errorf("status = %q, want not_executed", records[0].Status)
	}
	if records[0].ErrorCode != chatdomain.ToolErrNotAuthorized {
		t.Errorf("error code = %q", records[0].ErrorCode)
	}
}

/* ── 10. the whole story, through the turn loop ──────────────────────── */

// CREATE → UPDATE → UPDATE → STATUS → LIST → DELETE, every step a real turn
// through the real loop with a real grant check, a real schema validation
// and a real write. The fake here is only the model's choice of call.
//
// This is the scripted twin of the live exercise: the same six turns, in
// the same order, asserted on the same way. What it proves that the live
// run cannot is that the sequence holds deterministically, on every gate
// run, without a gateway.
func TestTheContentStoryEndToEnd(t *testing.T) {
	e := newEnv(t)
	agent, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agent, allThreadTools...)

	// ── 1. "Tive uma ideia. Guarda isso."
	e.llm.scriptToolCall(thtools.ThreadCreateTool,
		`{"title":"Microservices cedo demais","content":"Microservices cedo demais podem matar a velocidade de uma startup."}`)
	if ev, _ := e.turn(e.wsA, conv, "tive uma ideia de conteúdo, guarda isso").
		finished(thtools.ThreadCreateTool); ev.ErrorCode != "" {
		t.Fatalf("create failed: %+v", ev)
	}
	live := e.liveThreads(e.wsA)
	if len(live) != 1 {
		t.Fatalf("%d threads after create", len(live))
	}
	id := live[0].ID.String()
	if live[0].Status != domain.StatusIdea {
		t.Errorf("a captured idea landed in %q", live[0].Status)
	}

	// ── 2. "Transforma num post curto para LinkedIn."
	postV1 := "Você não tem um problema de escala.\n\nVocê tem um problema de organização."
	e.llm.scriptToolCall(thtools.ThreadUpdateTool,
		`{"thread_id":"`+id+`","content":`+quote(postV1)+`,"status":"draft"}`)
	e.turn(e.wsA, conv, "transforma num post curto para LinkedIn")
	if got := e.liveThreads(e.wsA)[0]; got.Content != postV1 || got.Status != domain.StatusDraft {
		t.Fatalf("after the rewrite: status=%q content=%q", got.Status, got.Content)
	}

	// ── 3. "Muda o hook." Same thread, and the text really moves.
	postV2 := "Microservices são a forma mais cara de adiar uma decisão de produto.\n\nVocê tem um problema de organização."
	e.llm.scriptToolCall(thtools.ThreadUpdateTool,
		`{"thread_id":"`+id+`","content":`+quote(postV2)+`}`)
	e.turn(e.wsA, conv, "muda o hook para algo mais provocativo")
	if got := e.liveThreads(e.wsA)[0]; got.Content != postV2 {
		t.Fatalf("the hook did not change: %q", got.Content)
	}
	if len(e.liveThreads(e.wsA)) != 1 {
		t.Fatal("the rewrite created a second thread")
	}

	// ── 4. "Marca como pronto para revisão."
	e.llm.scriptToolCall(thtools.ThreadUpdateTool, `{"thread_id":"`+id+`","status":"review"}`)
	e.turn(e.wsA, conv, "marca como pronto para revisão")
	if got := e.liveThreads(e.wsA)[0]; got.Status != domain.StatusReview {
		t.Fatalf("status = %q, want review", got.Status)
	} else if got.Content != postV2 {
		t.Fatalf("the status change rewrote the text: %q", got.Content)
	}

	// ── 5. "Quais conteúdos eu tenho em revisão?"
	e.llm.scriptToolCall(thtools.ThreadListTool, `{"status":"review"}`)
	sink := e.turn(e.wsA, conv, "quais conteúdos eu tenho em revisão?")
	ev, ok := sink.finished(thtools.ThreadListTool)
	if !ok || ev.ErrorCode != "" {
		t.Fatalf("list failed: %+v", ev)
	}
	// The thread the model was told about is the one that exists.
	out := e.execute(t, e.wsA, thtools.ThreadListTool, map[string]any{"status": "review"})
	rows := out["threads"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != id {
		t.Fatalf("the review listing does not hold the thread: %+v", rows)
	}

	// ── 6. "Era só um teste. Pode apagar."
	e.llm.scriptToolCall(thtools.ThreadDeleteTool, `{"thread_id":"`+id+`"}`)
	if ev, _ := e.turn(e.wsA, conv, "essa era só um teste, pode apagar").
		finished(thtools.ThreadDeleteTool); ev.ErrorCode != "" {
		t.Fatalf("delete failed: %+v", ev)
	}

	// Final state: nothing left standing.
	if got := len(e.liveThreads(e.wsA)); got != 0 {
		t.Fatalf("%d threads survived the cleanup", got)
	}
}

// quote is json.Marshal for a string, so a fixture containing newlines can
// be embedded in a scripted arguments payload without hand-escaping it.
func quote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
