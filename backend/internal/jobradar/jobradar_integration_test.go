//go:build integration

// Scout × Job Radar — the foundation suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/jobradar/...
//
// The sentence this suite has to make convincing:
//
//	An agent, using only the generic tool infrastructure and the grants it
//	was given, can read and change a real Job Radar opportunity — and an
//	agent without those grants cannot.
//
// What is REAL here: the Job Radar schema, domain, repository, service and
// HTTP routes; the Agents tool registry; the authorization store; the
// schema validator; the four-gate executor; the turn loop; the audit trail;
// and Postgres. What is faked: the LLM, because the point is to control
// which tool calls arrive, not to test that a model produces them.
//
// ── Why this file lives in internal/jobradar and imports chat ──────────
// The dependency it needs is the one that already exists: jobradar/tools
// satisfies chat/ports.Tool. Putting the suite in internal/chat would mean
// the Agents module importing Job Radar to test itself, which is the arrow
// the architecture forbids. Here the arrow points the way it already does.
package jobradar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	"github.com/corsi/backend/internal/jobradar/adapters/httpapi"
	"github.com/corsi/backend/internal/jobradar/adapters/repo"
	"github.com/corsi/backend/internal/jobradar/app"
	"github.com/corsi/backend/internal/jobradar/domain"
	"github.com/corsi/backend/internal/jobradar/ports"
	jrtools "github.com/corsi/backend/internal/jobradar/tools"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// testSecretsKey is exactly 32 bytes before encoding, which is what
// AES-256 needs. It seals the chat provider credential this suite creates;
// nothing here depends on its value.
var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// ── Why this suite runs in a database of its own ──────────────────────
//
// Every other integration package resets exactly one schema, and those
// schemas are disjoint, so `go test ./...` can run them in parallel without
// them noticing each other. This suite is the first that needs TWO: the
// Job Radar tables the tools write to, and the chat tables the turn loop
// writes to. Resetting `chat` here would drop the schema out from under the
// Agents suite running concurrently in another process — which is exactly
// what happened the first time this file was written.
//
// So it creates its own database, once, and resets schemas inside it. The
// isolation is then structural rather than a rule about which tests may run
// together.
const privateDBName = "corsi_test_jobradar"

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
		panic("jobradar suite: " + err.Error())
	}
	suiteDSN = d

	code := m.Run()
	cleanup()
	os.Exit(code)
}

// createPrivateDB makes a database beside the one it was pointed at and
// returns a DSN for it plus a function that drops it.
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
	// it. That is the same contract scripts/integration-test.sh follows, and
	// it is what lets dsn() below refuse anything else without carving out
	// an exception for this package.
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

// dsn is where the suite learns which database it may destroy. Same
// placement and same reason as the other suites: this is the only way to
// obtain one, so no destructive statement can be reached without it.
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
//
// Job Radar and chat, because the turn loop writes to chat.messages and
// chat.tool_calls while the tools write to jobradar.opportunities, and a
// test that reset only one of them would carry the other's rows between
// runs.
func freshDB(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS jobradar CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_jobradar`,
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

	migrateUp(t, d, "jobradar", "schema_migrations_jobradar")
	migrateUp(t, d, "chat", "schema_migrations_chat")
}

type env struct {
	t    *testing.T
	r    chi.Router
	pool *pgxpool.Pool

	// svc is the Job Radar service the routes and the tools share. Held so
	// a test can seed through the same application layer a tool writes
	// through, rather than through SQL that could drift from it.
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

	// Job Radar, wired the way module.go wires it.
	repos := repo.New(pool)
	svc := app.NewService(repos.Opportunities, repos.Companies, log)

	// The Agents stack, wired the way cmd/corsi wires it — including the
	// seam the Job Radar tools arrive through. `Internal: false` is the
	// production catalogue, so nothing here depends on system.echo.
	registry := chattools.MustNew(chattools.Options{Extra: jrtools.New(svc)})
	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}
	llm := newFakeLLM()
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		llm, sealer, registry,
		chatreferences.MustNew(jrtools.NewReferenceResolver(svc)), log)

	// The Job Radar routes, behind a workspace middleware that trusts a
	// test header. Everything below the transport is production code.
	root := chi.NewRouter()
	root.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id, err := uuid.Parse(req.Header.Get("X-Test-Workspace-Id"))
			if err != nil {
				http.Error(w, "test header missing or invalid", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), id)))
		})
	})
	root.Route("/job-radar", func(r chi.Router) {
		httpapi.NewHandler(svc, log).Mount(r)
	})

	return &env{
		t: t, r: root, pool: pool, svc: svc, chatSvc: chatSvc,
		llm: llm, registry: registry,
		wsA: uuid.New(), wsB: uuid.New(),
	}
}

func (e *env) do(method, path string, ws uuid.UUID, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Workspace-Id", ws.String())
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// ctxFor builds the context a tool executes under: one carrying a
// workspace, exactly as the middleware produces for a real request.
func ctxFor(ws uuid.UUID) context.Context {
	return workspace.WithWorkspaceID(context.Background(), ws)
}

/* ── seeding ─────────────────────────────────────────────────────────── */

// seed creates one opportunity through the application layer.
func (e *env) seed(ws uuid.UUID, company, role string, stage *domain.PipelineStage) *domain.Opportunity {
	e.t.Helper()
	o, err := e.svc.CreateOpportunity(ctxFor(ws), ws, app.CreateInput{
		CompanyName: company,
		Role:        role,
		Location:    "Remote",
		Source:      "manual",
		Stage:       stage,
	})
	if err != nil {
		e.t.Fatalf("seed %s/%s: %v", company, role, err)
	}
	return o
}

func stagePtr(s domain.PipelineStage) *domain.PipelineStage { return &s }

/* ── the agent ───────────────────────────────────────────────────────── */

// newAgent creates a provider, an agent and a conversation, and returns
// the ids the turn loop needs. It is the ordinary path: nothing here is
// special to Job Radar, and no agent is named "Scout" — the whole point is
// that the name has no meaning to the system.
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
		SystemPrompt: "you help with the job search",
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

// newConversationWith opens a thread already about one entity — the state
// "Conversar com Scout" produces.
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

// errorsAs is errors.As, named here so the reference suite reads the same
// as the rest of the module.
func errorsAs(err error, target **chatdomain.Error) bool {
	return errors.As(err, target)
}

func (e *env) authorize(ws, agentID uuid.UUID, names ...string) {
	e.t.Helper()
	for _, n := range names {
		if err := e.chatSvc.AuthorizeTool(ctxFor(ws), ws, agentID, chatdomain.ToolName(n)); err != nil {
			e.t.Fatalf("authorize %s: %v", n, err)
		}
	}
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

// scriptToolCall makes round 1 ask for one tool and round 2 answer.
// scriptReply makes the turn answer in prose without calling anything.
func (f *fakeLLM) scriptReply(text string) {
	f.rounds = [][]chatports.StreamEvent{{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}}
}

func (f *fakeLLM) scriptToolCall(name, args string) {
	f.rounds = [][]chatports.StreamEvent{
		{
			{FinishReason: "tool_calls", Usage: &chatports.Usage{PromptTokens: 20, CompletionTokens: 10},
				ToolCalls: []chatdomain.ToolCall{{ID: "call_1", Name: chatdomain.ToolName(name), Arguments: args}}},
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

// finished returns the terminal tool event for a call, which is the one
// carrying ok/error and the error code.
func (c *collectSink) finished(name string) (chatapp.ToolEvent, bool) {
	for i := len(c.tools) - 1; i >= 0; i-- {
		if c.tools[i].Name == name && c.tools[i].Status != "running" {
			return c.tools[i], true
		}
	}
	return chatapp.ToolEvent{}, false
}

/* ── 1. the registry ─────────────────────────────────────────────────── */

// The three capabilities must exist in the catalogue a production binary
// builds — not behind the internal-tools switch.
func TestJobRadarToolsAreInTheRegistry(t *testing.T) {
	e := newEnv(t)

	// The whole catalogue, exhaustively. The count check at the bottom is
	// what makes this a gate rather than a spot check: a tool added without
	// a line here fails, which is the moment to decide whether an agent
	// should be able to do that at all.
	want := map[string]chatdomain.ToolEffect{
		"job_radar.opportunity.list":   chatdomain.EffectRead,
		"job_radar.opportunity.get":    chatdomain.EffectRead,
		"job_radar.opportunity.create": chatdomain.EffectWrite,
		"job_radar.opportunity.move":   chatdomain.EffectWrite,
		"job_radar.opportunity.delete": chatdomain.EffectWrite,
	}

	got := map[string]chatdomain.ToolEffect{}
	for _, d := range e.registry.Definitions() {
		if strings.HasPrefix(string(d.Name), "job_radar.") {
			got[string(d.Name)] = d.Effect
		}
	}

	for name, effect := range want {
		gotEffect, ok := got[name]
		if !ok {
			t.Fatalf("%s is not in the registry", name)
		}
		// The effect is what the interface shows a person before they grant
		// a capability. A write declared as a read would be the module
		// lying in the one field that exists to warn them.
		if gotEffect != effect {
			t.Errorf("%s effect = %q, want %q", name, gotEffect, effect)
		}
		if _, resolvable := e.registry.Lookup(chatdomain.ToolName(name)); !resolvable {
			t.Errorf("%s does not resolve to an executor", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("registry has %d job_radar tools, want %d: %v", len(got), len(want), got)
	}
}

// Every definition must survive the domain's own validation — that is what
// the composition root would enforce at start-up, and a tool that failed it
// would take the whole binary down rather than degrade quietly.
func TestJobRadarToolDefinitionsAreValid(t *testing.T) {
	for _, tool := range jrtools.New(nil) {
		if err := tool.Definition().Validate(); err != nil {
			t.Errorf("%s: %v", tool.Definition().Name, err)
		}
	}
}

// The stage vocabulary the model is told must be the one the domain
// enforces. Two lists that drift produce a tool whose description invites
// exactly the argument its parser refuses.
func TestMoveDescribesTheDomainStages(t *testing.T) {
	for _, tool := range jrtools.New(nil) {
		if tool.Definition().Name != "job_radar.opportunity.move" {
			continue
		}
		desc := tool.Definition().Schema.Properties["stage"].Description
		for _, name := range domain.StageNames() {
			if !strings.Contains(desc, name) {
				t.Errorf("the stage argument does not mention %q: %s", name, desc)
			}
		}
		return
	}
	t.Fatal("move tool not found")
}

/* ── 2. authorization ────────────────────────────────────────────────── */

// Registered is not authorized. This is the property the whole design
// exists for, and it is asserted through the real turn loop rather than
// against the store: what matters is that the EXECUTOR refuses, not that a
// row is absent.
func TestUnauthorizedAgentCannotMoveAnOpportunity(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	_, conv := e.newAgent(e.wsA, "no-grants")

	// Deliberately NOT authorized.
	e.llm.scriptToolCall("job_radar.opportunity.move",
		`{"opportunity_id":"`+opp.ID.String()+`","stage":"applied"}`)

	sink := e.turn(e.wsA, conv, "apliquei nessa")

	ev, ok := sink.finished("job_radar.opportunity.move")
	if !ok {
		t.Fatal("the turn ran no tool event for move")
	}
	// A refusal is not_executed: the capability never ran. The live frame
	// carries the outcome's own status, so it says the same thing the audit
	// row does.
	if ev.Status != string(chatdomain.ToolCallNotExecuted) || ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("status=%q code=%q, want error/%s", ev.Status, ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}

	// And the domain must be untouched. A refusal that still wrote would be
	// the worst possible outcome: an audit trail saying "denied" over a
	// database that changed.
	after, err := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if s, _ := after.Stage(); s != domain.StageSaved {
		t.Fatalf("stage = %q after a refused call; it must still be saved", s)
	}
}

// A grant is per agent and per tool. An agent holding `list` must not be
// able to `move`.
func TestGrantsAreIndividual(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	agent, conv := e.newAgent(e.wsA, "reader")
	e.authorize(e.wsA, agent, "job_radar.opportunity.list", "job_radar.opportunity.get")

	e.llm.scriptToolCall("job_radar.opportunity.move",
		`{"opportunity_id":"`+opp.ID.String()+`","stage":"applied"}`)
	sink := e.turn(e.wsA, conv, "move it")

	ev, ok := sink.finished("job_radar.opportunity.move")
	if !ok {
		t.Fatal("no terminal tool event")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("a read-only agent was allowed to move: code=%q", ev.ErrorCode)
	}
}

// The agent tools catalogue must show the capabilities and their grant
// state — this is what the Tools page renders, and the sprint requires the
// three to be authorizable individually there.
func TestAgentToolsReportCarriesJobRadar(t *testing.T) {
	e := newEnv(t)
	agent, _ := e.newAgent(e.wsA, "scout")

	report, err := e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agent)
	if err != nil {
		t.Fatalf("agent tools: %v", err)
	}

	found := map[string]bool{}
	for _, item := range report.Items {
		if strings.HasPrefix(string(item.Name), "job_radar.") {
			found[string(item.Name)] = item.Authorized
		}
	}
	for _, name := range []string{
		"job_radar.opportunity.list",
		"job_radar.opportunity.get",
		"job_radar.opportunity.move",
	} {
		authorized, listed := found[name]
		if !listed {
			t.Fatalf("%s is not in the agent's catalogue", name)
		}
		if authorized {
			t.Errorf("%s is authorized on a fresh agent; the default must be deny", name)
		}
	}

	// Granting one must not grant its neighbours.
	e.authorize(e.wsA, agent, "job_radar.opportunity.list")
	report, err = e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agent)
	if err != nil {
		t.Fatalf("agent tools: %v", err)
	}
	for _, item := range report.Items {
		switch string(item.Name) {
		case "job_radar.opportunity.list":
			if !item.Authorized {
				t.Error("list was granted and is not reported as authorized")
			}
		case "job_radar.opportunity.get", "job_radar.opportunity.move":
			if item.Authorized {
				t.Errorf("%s became authorized by granting a different tool", item.Name)
			}
		}
	}
}

/* ── 3. list ─────────────────────────────────────────────────────────── */

func TestListReturnsRealOpportunities(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	e.seed(e.wsA, "Stripe", "Infrastructure Engineer", stagePtr(domain.StageApplied))
	e.seed(e.wsA, "Stripe", "Backend Engineer", nil)

	out := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{})
	items := toolItems(t, out, "opportunities")
	if len(items) != 3 {
		t.Fatalf("got %d opportunities, want 3", len(items))
	}
	if total, _ := out["matching_total"].(float64); int(total) != 3 {
		t.Errorf("matching_total = %v, want 3", out["matching_total"])
	}

	// The untracked record must be reported as "discover" rather than as a
	// null the model has to interpret.
	var sawDiscover bool
	for _, it := range items {
		if it["stage"] == "discover" {
			sawDiscover = true
		}
	}
	if !sawDiscover {
		t.Error("the untracked opportunity was not labelled discover")
	}
}

func TestListFiltersByCompany(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	e.seed(e.wsA, "Stripe", "Infrastructure Engineer", stagePtr(domain.StageApplied))

	// Lowercase on purpose: a model working from "aquela da stripe" must
	// not fail because the stored name is capitalised.
	out := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{"company": "stripe"})
	items := toolItems(t, out, "opportunities")
	if len(items) != 1 {
		t.Fatalf("got %d, want 1: %v", len(items), items)
	}
	if items[0]["company"] != "Stripe" {
		t.Errorf("company = %v", items[0]["company"])
	}
}

func TestListFiltersByStage(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Acme", "A", stagePtr(domain.StageSaved))
	e.seed(e.wsA, "Acme", "B", stagePtr(domain.StageApplied))
	e.seed(e.wsA, "Acme", "C", nil)

	out := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{"stage": "applied"})
	if items := toolItems(t, out, "opportunities"); len(items) != 1 {
		t.Fatalf("stage=applied returned %d, want 1", len(items))
	}

	// "discover" is the word for the untracked set, and it is not a stage.
	out = e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{"stage": "discover"})
	if items := toolItems(t, out, "opportunities"); len(items) != 1 {
		t.Fatalf("stage=discover returned %d, want 1", len(items))
	}

	// An invented stage is a fixable mistake, reported as such.
	_, err := e.executeErr(e.wsA, "job_radar.opportunity.list", map[string]any{"stage": "hired"})
	if err == nil {
		t.Fatal("an unknown stage filter was accepted")
	}
}

// The listing must never cross a workspace. This is the ownership property
// the sprint requires, asserted where it would actually fail.
func TestListIsScopedToItsWorkspace(t *testing.T) {
	e := newEnv(t)
	e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))

	out := e.execute(t, e.wsB, "job_radar.opportunity.list", map[string]any{})
	if items := toolItems(t, out, "opportunities"); len(items) != 0 {
		t.Fatalf("workspace B saw %d of workspace A's opportunities", len(items))
	}
}

/* ── 4. get ──────────────────────────────────────────────────────────── */

func TestGetReturnsOneOpportunity(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))

	out := e.execute(t, e.wsA, "job_radar.opportunity.get",
		map[string]any{"opportunity_id": opp.ID.String()})

	detail, ok := out["opportunity"].(map[string]any)
	if !ok {
		t.Fatalf("no opportunity in %v", out)
	}
	if detail["company"] != "Acme" || detail["role"] != "Backend Engineer" {
		t.Errorf("got %v", detail)
	}
	if detail["stage"] != "saved" {
		t.Errorf("stage = %v, want saved", detail["stage"])
	}
}

func TestGetRefusesAnUnknownID(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, "job_radar.opportunity.get",
		map[string]any{"opportunity_id": uuid.New().String()})
	if err == nil {
		t.Fatal("a nonexistent id was accepted")
	}
	// The model has to be able to tell "wrong id" from "the tool is broken",
	// or it apologises instead of listing and retrying.
	var f *chatdomain.ToolFailure
	if !errors.As(err, &f) || f.Code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("error = %v, want a recoverable invalid-arguments failure", err)
	}
	if !strings.Contains(f.Message, "not found") {
		t.Errorf("message %q does not say the record was not found", f.Message)
	}
}

// A malformed id is a different mistake from a missing one, and telling
// them apart is what stops the model hunting for a record when it should be
// fixing its argument.
func TestGetRefusesAMalformedID(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, "job_radar.opportunity.get",
		map[string]any{"opportunity_id": "the-stripe-one"})
	if err == nil {
		t.Fatal("a malformed id was accepted")
	}
}

func TestGetCannotReachAnotherWorkspace(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))

	_, err := e.executeErr(e.wsB, "job_radar.opportunity.get",
		map[string]any{"opportunity_id": opp.ID.String()})
	if err == nil {
		t.Fatal("workspace B read workspace A's opportunity")
	}
	// Reported as not-found, never as forbidden: confirming that the record
	// exists elsewhere would leak the existence of another workspace's data.
	var f *chatdomain.ToolFailure
	if !errors.As(err, &f) || !strings.Contains(f.Message, "not found") {
		t.Errorf("cross-workspace read said %v; it must be indistinguishable from absence", err)
	}
}

/* ── 5. move ─────────────────────────────────────────────────────────── */

// The sprint's central assertion: a tool call changes the real domain and
// the change survives being read back.
func TestMovePersistsTheNewStage(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))

	out := e.execute(t, e.wsA, "job_radar.opportunity.move", map[string]any{
		"opportunity_id": opp.ID.String(),
		"stage":          "applied",
	})

	if out["stage"] != "applied" {
		t.Errorf("returned stage = %v", out["stage"])
	}
	if out["previous_stage"] != "saved" {
		t.Errorf("previous_stage = %v, want saved", out["previous_stage"])
	}

	// Read back through the module's own HTTP surface — the same route the
	// Job Radar UI calls. This is what makes the change real rather than
	// reported.
	rec := e.do("GET", "/job-radar/opportunities/"+opp.ID.String(), e.wsA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("readback status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Opportunity struct {
			Tracking *struct {
				Stage string `json:"stage"`
			} `json:"tracking"`
		} `json:"opportunity"`
		History []struct {
			FromStage *string `json:"from_stage"`
			ToStage   string  `json:"to_stage"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readback: %v", err)
	}
	if body.Opportunity.Tracking == nil || body.Opportunity.Tracking.Stage != "applied" {
		t.Fatalf("the database still reports %v", body.Opportunity.Tracking)
	}

	// The transition must have been recorded. A stage that changed without
	// an event is a pipeline whose history cannot explain where it is.
	last := body.History[len(body.History)-1]
	if last.ToStage != "applied" || last.FromStage == nil || *last.FromStage != "saved" {
		t.Errorf("last event = %v → %s, want saved → applied", last.FromStage, last.ToStage)
	}
}

func TestMoveFromDiscoverEntersThePipeline(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", nil)

	out := e.execute(t, e.wsA, "job_radar.opportunity.move", map[string]any{
		"opportunity_id": opp.ID.String(),
		"stage":          "applied",
	})
	// There was no previous stage, and saying "saved" would invent a visit
	// that never happened.
	if out["previous_stage"] != "discover" {
		t.Errorf("previous_stage = %v, want discover", out["previous_stage"])
	}

	after, err := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if s, tracked := after.Stage(); !tracked || s != domain.StageApplied {
		t.Fatalf("stage = %q tracked=%v", s, tracked)
	}
}

func TestMoveRefusesAnUnknownStage(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))

	_, err := e.executeErr(e.wsA, "job_radar.opportunity.move", map[string]any{
		"opportunity_id": opp.ID.String(),
		"stage":          "hired",
	})
	if err == nil {
		t.Fatal("an unknown stage was accepted")
	}
	var f *chatdomain.ToolFailure
	if !errors.As(err, &f) || f.Code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("error = %v, want invalid arguments", err)
	}
	// The message must teach the vocabulary, not just refuse.
	for _, name := range domain.StageNames() {
		if !strings.Contains(f.Message, name) {
			t.Errorf("message %q omits stage %q", f.Message, name)
		}
	}

	after, _ := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)
	if s, _ := after.Stage(); s != domain.StageSaved {
		t.Fatalf("a refused move changed the stage to %q", s)
	}
}

func TestMoveCannotReachAnotherWorkspace(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))

	if _, err := e.executeErr(e.wsB, "job_radar.opportunity.move", map[string]any{
		"opportunity_id": opp.ID.String(),
		"stage":          "rejected",
	}); err == nil {
		t.Fatal("workspace B moved workspace A's opportunity")
	}

	after, _ := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)
	if s, _ := after.Stage(); s != domain.StageSaved {
		t.Fatalf("a cross-workspace move changed the stage to %q", s)
	}
}

// Moving to the stage a record already occupies must not restart the stage
// clock or file a transition.
func TestMoveToTheSameStageIsANoOp(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))

	before, _ := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)

	out := e.execute(t, e.wsA, "job_radar.opportunity.move", map[string]any{
		"opportunity_id": opp.ID.String(),
		"stage":          "applied",
	})
	if unchanged, _ := out["unchanged"].(bool); !unchanged {
		t.Error("the result does not report the call as unchanged")
	}

	after, _ := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)
	if !after.Tracking.StageEnteredAt.Equal(before.Tracking.StageEnteredAt) {
		t.Errorf("the stage clock restarted: %v → %v",
			before.Tracking.StageEnteredAt, after.Tracking.StageEnteredAt)
	}
}

/* ── 6. the whole chain ──────────────────────────────────────────────── */

// One conversation, the real loop, an authorized agent: list to resolve the
// reference, then move, then read the database back.
//
// This is the sprint's acceptance scenario expressed as a test. The live
// version with a real model is recorded separately; this one is what keeps
// it working.
func TestConversationMovesARealOpportunity(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	agent, conv := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agent,
		"job_radar.opportunity.list",
		"job_radar.opportunity.get",
		"job_radar.opportunity.move")

	// Round 1: the model resolves the reference.
	e.llm.scriptToolCall("job_radar.opportunity.list", `{"company":"Acme"}`)
	sink := e.turn(e.wsA, conv, "quais vagas da Acme eu tenho?")
	if ev, ok := sink.finished("job_radar.opportunity.list"); !ok || ev.Status != "ok" {
		t.Fatalf("list did not succeed: %+v", ev)
	}

	// Round 2: having the id, the model moves it.
	e.llm.streamCalls = 0
	e.llm.scriptToolCall("job_radar.opportunity.move",
		`{"opportunity_id":"`+opp.ID.String()+`","stage":"applied"}`)
	sink = e.turn(e.wsA, conv, "apliquei nessa de Backend Engineer")

	ev, ok := sink.finished("job_radar.opportunity.move")
	if !ok || ev.Status != "ok" {
		t.Fatalf("move did not succeed: %+v", ev)
	}

	// The proof is the database, not the transcript.
	after, err := e.svc.GetOpportunity(ctxFor(e.wsA), e.wsA, opp.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if s, _ := after.Stage(); s != domain.StageApplied {
		t.Fatalf("stage = %q, want applied", s)
	}
}

// Tool execution must land in the existing audit trail. The sprint forbids
// a second audit system inside Job Radar, so this asserts the first one
// actually covers it.
func TestMoveIsRecordedInTheExistingAuditTrail(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	agent, conv := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agent, "job_radar.opportunity.move")

	e.llm.scriptToolCall("job_radar.opportunity.move",
		`{"opportunity_id":"`+opp.ID.String()+`","stage":"applied"}`)
	e.turn(e.wsA, conv, "apliquei")

	records, err := e.chatSvc.ConversationToolCalls(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}

	var found *chatdomain.ToolCallRecord
	for i := range records {
		if records[i].ToolName == "job_radar.opportunity.move" {
			found = &records[i]
		}
	}
	if found == nil {
		t.Fatal("the move was not recorded in chat.tool_calls")
	}
	if found.Status != chatdomain.ToolCallOK {
		t.Errorf("status = %q", found.Status)
	}
	// The arguments are what make the record answer "what was changed".
	if found.Arguments == nil || !strings.Contains(*found.Arguments, opp.ID.String()) {
		t.Errorf("the record does not carry the arguments: %v", found.Arguments)
	}
	if found.ConversationID != conv {
		t.Errorf("conversation = %v, want %v", found.ConversationID, conv)
	}
}

/* ── 7. the import path ──────────────────────────────────────────────── */

// The migration off localStorage must preserve the pipeline clock. Stamping
// every imported record with the import's own timestamp would reset every
// "days in stage" at once, which is the number the board exists to show.
func TestImportPreservesTheStageClock(t *testing.T) {
	e := newEnv(t)

	trackedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	enteredAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	rec := e.do("POST", "/job-radar/opportunities/import", e.wsA, map[string]any{
		"items": []map[string]any{{
			// Every record in a real browser document carries its own id,
			// and the import now requires one: it is what makes a repeated
			// import recognisable instead of duplicating. See
			// import_integration_test.go.
			"legacy_id":        "op_clock_fixture",
			"company":          "Acme",
			"role":             "Backend Engineer",
			"stage":            "applied",
			"tracked_at":       trackedAt.UnixMilli(),
			"stage_entered_at": enteredAt.UnixMilli(),
			"notes":            map[string]string{"general": "referral from a friend"},
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		CreatedCount int `json:"created_count"`
		FailedCount  int `json:"failed_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CreatedCount != 1 || body.FailedCount != 0 {
		t.Fatalf("created=%d failed=%d", body.CreatedCount, body.FailedCount)
	}

	items, _, err := e.svc.ListOpportunities(ctxFor(e.wsA), e.wsA, portsFilter())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d opportunities", len(items))
	}
	got := items[0]
	if !got.Tracking.TrackedAt.Equal(trackedAt) {
		t.Errorf("tracked_at = %v, want %v", got.Tracking.TrackedAt, trackedAt)
	}
	if !got.Tracking.StageEnteredAt.Equal(enteredAt) {
		t.Errorf("stage_entered_at = %v, want %v", got.Tracking.StageEnteredAt, enteredAt)
	}
	if got.Notes.General != "referral from a friend" {
		t.Errorf("notes were lost: %q", got.Notes.General)
	}
}

// A malformed record must not block the rest of the migration. One bad row
// from a year-old browser document should cost that row, not the pipeline.
func TestImportReportsPartialFailure(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/job-radar/opportunities/import", e.wsA, map[string]any{
		"items": []map[string]any{
			{"legacy_id": "op_partial_1", "company": "Acme", "role": "Backend Engineer", "stage": "applied"},
			{"legacy_id": "op_partial_2", "company": "Broken", "role": ""},
			{"legacy_id": "op_partial_3", "company": "Stripe", "role": "Infrastructure Engineer"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		CreatedCount int `json:"created_count"`
		FailedCount  int `json:"failed_count"`
		Failed       []struct {
			Index int    `json:"index"`
			Error string `json:"error"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CreatedCount != 2 {
		t.Errorf("created = %d, want 2", body.CreatedCount)
	}
	if body.FailedCount != 1 || len(body.Failed) != 1 {
		t.Fatalf("failed = %d, want 1", body.FailedCount)
	}
	if body.Failed[0].Index != 1 {
		t.Errorf("the failure names index %d, want 1", body.Failed[0].Index)
	}
}

/* ── execution helpers ───────────────────────────────────────────────── */

// execute runs one tool through the registry the same way the executor
// does: resolve, validate against the declared schema, then run with a
// workspace-carrying context.
//
// It goes through ValidateArguments rather than calling Execute directly so
// a schema that does not match what the tool reads fails here rather than
// in production.
func (e *env) execute(t *testing.T, ws uuid.UUID, name string, args map[string]any) map[string]any {
	t.Helper()
	out, err := e.executeErr(ws, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (e *env) executeErr(ws uuid.UUID, name string, args map[string]any) (map[string]any, error) {
	tool, ok := e.registry.Lookup(chatdomain.ToolName(name))
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

// portsFilter is the zero filter: every live opportunity of the workspace.
func portsFilter() ports.OpportunityFilter { return ports.OpportunityFilter{} }

func toolItems(t *testing.T, out map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := out[key].([]any)
	if !ok {
		t.Fatalf("%s is not a list in %v", key, out)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("entry is not an object: %v", r)
		}
		items = append(items, m)
	}
	return items
}
