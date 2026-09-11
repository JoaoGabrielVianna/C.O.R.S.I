//go:build integration

// Integration test for the chat bounded context: migrations, repositories
// and HTTP handlers against a real Postgres, with the LLM port replaced by
// a scripted fake. Skipped unless TEST_POSTGRES_DSN is set.
//
//	Run with:  TEST_POSTGRES_DSN=postgres://corsi:corsi@localhost:5432/corsi?sslmode=disable \
//	           go test -tags=integration ./internal/chat/...
//
// Until this suite existed, 23 routes and 4 tables had no regression at
// all: the end-to-end evidence was collected by hand, which proves the
// module worked on one afternoon and protects nothing afterwards. Everything the Agents v1 roadmap does next (context
// builder, memory, sources, budgets) edits SendMessage, so the tests here
// are deliberately weighted toward the invariants that would destroy the
// product if they broke silently, not toward line coverage.
//
// Why the shared database is safe here: this suite only ever touches the
// `chat` schema and the `schema_migrations_chat` version table, and the
// finance suite only ever touches `finance` and `schema_migrations`. They
// can run concurrently, as `go test ./...` does, without either one wiping
// the other. The entrypoint test makes the opposite choice (a throwaway
// database) because it applies BOTH timelines and would race this one.
package chat

import (
	"bytes"
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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/adapters/httpapi"
	"github.com/corsi/backend/internal/chat/adapters/references"
	"github.com/corsi/backend/internal/chat/adapters/repo"
	"github.com/corsi/backend/internal/chat/adapters/tools"
	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// testAPIKey is a fixture, not a credential: it is a literal in a test file
// and could never authenticate against anything. Nothing in this suite ever
// reads a real key from the environment.
const testAPIKey = "sk-fixture-not-a-real-key"

// testSecretsKey is exactly 32 bytes before encoding, which is what
// AES-256 needs. Same reasoning as above: it is a constant, on purpose.
var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// migrateDSN rewrites postgres:// to pgx5://, the scheme the golang-migrate
// pgx/v5 driver registers under. Mirrors cmd/migrate/main.go.
func migrateDSN(d string) string {
	for _, p := range []string{"postgres://", "postgres" + "ql://"} {
		if strings.HasPrefix(d, p) {
			return "pgx5://" + strings.TrimPrefix(d, p)
		}
	}
	return d
}

// chatMigrateDSN points the driver at schema_migrations_chat.
//
// This is not a detail. Omitting it makes golang-migrate read the finance
// version table, see a fully applied timeline, and conclude there is
// nothing to do — leaving every test below running against a database with
// no `chat` schema at all. The same flag is mandatory in the Makefile and
// in the container entrypoint.
func chatMigrateDSN(t *testing.T, d string) string {
	t.Helper()
	u, err := url.Parse(migrateDSN(d))
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	q.Set("x-migrations-table", "schema_migrations_chat")
	u.RawQuery = q.Encode()
	return u.String()
}

// dsn is where the suite learns which database it is allowed to destroy.
//
// The guard lives HERE rather than beside each DROP, and that placement is
// the point: this is the only way any test in this package can obtain a
// database, so there is no arrangement of calls that reaches a destructive
// statement without having passed it. A guard next to the DROP is a call
// somebody deletes in a refactor and nothing notices until a real database
// is gone. See internal/platform/testdb.
func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping integration test")
	}
	testdb.AssertDestructible(t, v)
	return v
}

// freshDB runs the chat timeline down then up, so every test starts from an
// empty schema. Six migrations over four tables; it costs well under a
// second.
func freshDB(t *testing.T, d string) {
	t.Helper()
	m, err := migrate.New("file://../../migrations/chat", chatMigrateDSN(t, d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

type envOption func(*envConfig)

type envConfig struct {
	sealerKey string
	// internalTools mirrors the production switch. True by default here
	// because almost every test in the suite needs a tool it can actually
	// run, and `system.echo` is the only one this binary ships.
	internalTools bool
	// extraTools go through tools.Options.Extra, the seam a host binary
	// supplies real capabilities through. Empty by default.
	extraTools []ports.Tool
	// referenceResolvers arrive through the same seam a host binary uses
	// for them. Empty by default, which is a build that can resolve no
	// subjects — and the shape every deployment had before they existed.
	referenceResolvers []ports.ContextReferenceResolver
	// promptCache mirrors the production switch, inverted. True by default
	// here because it is true by default in production: a harness that
	// silently built the un-cached request would test a shape no deployment
	// sends. See withPromptCacheDisabled.
	promptCache bool
}

// withoutSecretsKey builds the stack the way a deployment with no
// SECRETS_KEY boots: the sealer constructs, and every operation that needs
// it fails with a 503 instead of the process refusing to start.
func withoutSecretsKey() envOption {
	return func(c *envConfig) { c.sealerKey = "" }
}

// withProductionToolCatalogue builds the stack the way a production
// deployment does: `CHAT_ENABLE_INTERNAL_TOOLS` unset, so the diagnostics
// are not in the registry at all.
func withProductionToolCatalogue() envOption {
	return func(c *envConfig) { c.internalTools = false }
}

// withExtraTools adds capabilities through the same seam a host binary
// uses. It exists because some properties — scoping above all — cannot be
// stated against a catalogue of one, and adding a second diagnostic to the
// production registry to make a test expressible would be a product change
// wearing test clothes.
func withExtraTools(extra ...ports.Tool) envOption {
	return func(c *envConfig) { c.extraTools = append(c.extraTools, extra...) }
}

// withPromptCacheDisabled builds the stack the way a deployment that set
// CHAT_DISABLE_PROMPT_CACHE=true boots.
//
// It is the kill switch on an external boundary, and the property it has to
// have is not "caching is off" but "the request body is the one this module
// sent before caching existed". That is asserted on the bytes — see
// TestKillSwitchRestoresThePreCachingRequestBody.
func withPromptCacheDisabled() envOption {
	return func(c *envConfig) { c.promptCache = false }
}

// withReferenceResolvers supplies entity resolvers the same way the
// composition root does.
func withReferenceResolvers(rs ...ports.ContextReferenceResolver) envOption {
	return func(c *envConfig) { c.referenceResolvers = append(c.referenceResolvers, rs...) }
}

type env struct {
	t    *testing.T
	r    chi.Router
	pool *pgxpool.Pool
	llm  *fakeLLM
	// svc is the same service the router is wired to. Held so a test can
	// reach an application entry point that deliberately has no route —
	// app.CreateProposedMemory is one, and the fact that it is unreachable
	// over HTTP is the property being tested.
	svc *app.Service
	// Two workspaces, always. Every isolation assertion in this file reads
	// wsA's data back from wsB.
	wsA uuid.UUID
	wsB uuid.UUID
}

// newEnv wires the real repositories, service and handler over a real pool,
// with the LLM port faked and the workspace middleware replaced by one that
// trusts a test header. Everything below the transport is production code.
func newEnv(t *testing.T, opts ...envOption) *env {
	t.Helper()
	d := dsn(t)
	freshDB(t, d)
	return newEnvOn(t, d, opts...)
}

// newEnvOn wires the same stack over a database the caller has already
// prepared, rather than resetting the schema first. The migration tests
// need it: the rows they seeded are the subject of the assertion.
func newEnvOn(t *testing.T, d string, opts ...envOption) *env {
	t.Helper()
	cfg := envConfig{sealerKey: testSecretsKey, internalTools: true, promptCache: true}
	for _, o := range opts {
		o(&cfg)
	}

	ctx := context.Background()

	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sealer, err := secrets.New(secrets.Config{Key: cfg.sealerKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	llm := newFakeLLM()
	// The real registry. Faking it would mean the authorization tests proved
	// something about a fake catalogue; the registry is code either way, so
	// the production one is the cheaper and the more honest choice.
	registry := tools.MustNew(tools.Options{Internal: cfg.internalTools, Extra: cfg.extraTools})
	svc := app.NewService(repo.New(pool), postgres.NewTxManager(pool), llm, sealer, registry,
		// The reference registry the env was configured with. Empty by
		// default: this suite is about capabilities, and a build with no
		// providers is a real deployment shape.
		references.MustNew(cfg.referenceResolvers...), log,
		// Mirrors cmd/corsi: the deployment decides, and the deployment's
		// default is on.
		app.WithPromptCache(cfg.promptCache))
	h := httpapi.NewHandler(svc, log)

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
	// Mounted under /chat so the paths exercised here are the paths
	// production serves; the prefix lives in module.go, not in Mount.
	root.Route("/chat", func(r chi.Router) { h.Mount(r) })

	return &env{t: t, r: root, pool: pool, llm: llm, svc: svc, wsA: uuid.New(), wsB: uuid.New()}
}

func (e *env) do(method, path string, ws uuid.UUID, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.doCtx(context.Background(), method, path, ws, body)
}

// doCtx is the same request with a caller-supplied context, so a test can
// cancel mid-stream the way a closed browser tab does.
func (e *env) doCtx(ctx context.Context, method, path string, ws uuid.UUID, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var br io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		br = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, br).WithContext(ctx)
	if br != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Test-Workspace-Id", ws.String())
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %T: %v (body %s)", out, err, rec.Body.String())
	}
	return out
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, want, rec.Body.String())
	}
}

// wantErrorCode asserts the wire error contract: {"error":{"code","message"}}.
func wantErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	wantStatus(t, rec, status)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body %s)", err, rec.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q (body %s)", body.Error.Code, code, rec.Body.String())
	}
	if body.Error.Message == "" {
		t.Fatalf("error message is empty; the contract requires an actionable one")
	}
}

/* ── fixtures ────────────────────────────────────────────────────────── */

type seeded struct {
	providerID     string
	agentID        string
	conversationID string
}

// seed creates the provider → agent → conversation chain a turn needs.
// Nothing in the module works without all three, so almost every test
// starts here.
func (e *env) seed(ws uuid.UUID) seeded {
	e.t.Helper()
	return e.seedWith(ws, map[string]any{})
}

// seedWith allows a test to override agent fields (system prompt, history
// limit) without repeating the whole chain.
func (e *env) seedWith(ws uuid.UUID, agentOverrides map[string]any) seeded {
	e.t.Helper()

	rec := e.do("POST", "/chat/providers", ws, map[string]any{
		"name":          "LiteLLM " + uuid.NewString()[:8],
		"base_url":      "https://gateway.invalid/v1",
		"api_key":       testAPIKey,
		"default_model": "test-model",
	})
	wantStatus(e.t, rec, http.StatusCreated)
	provider := decode[map[string]any](e.t, rec)

	agentBody := map[string]any{
		"provider_id":   provider["id"],
		"name":          "Agente " + uuid.NewString()[:8],
		"description":   "agente de teste",
		"system_prompt": "você é o agente de testes do João",
		"model":         "test-model",
	}
	for k, v := range agentOverrides {
		agentBody[k] = v
	}
	rec = e.do("POST", "/chat/agents", ws, agentBody)
	wantStatus(e.t, rec, http.StatusCreated)
	agent := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/conversations", ws, map[string]any{"agent_id": agent["id"]})
	wantStatus(e.t, rec, http.StatusCreated)
	conv := decode[map[string]any](e.t, rec)

	return seeded{
		providerID:     provider["id"].(string),
		agentID:        agent["id"].(string),
		conversationID: conv["id"].(string),
	}
}

/* ── fake LLM ────────────────────────────────────────────────────────── */

// fakeLLM is the stand-in for ports.LLM. Nothing in this suite reaches the
// network. This suite is about our own persistence and transport, and the
// external boundary is a separate, explicitly unverified concern.
//
// Every field is written by the test before the request and read after it.
// The whole turn runs on the request goroutine, so no synchronisation is
// needed.
type fakeLLM struct {
	// script is replayed by Recv, in order.
	script []ports.StreamEvent
	// rounds, when set, replaces script per provider call: the first call
	// replays rounds[0], the second rounds[1], and so on. It is what lets a
	// test drive a whole tool loop — ask for a tool, then answer — instead of
	// only the single call a turn used to be. A call past the end of the
	// slice replays the last entry, so a test that scripts one answer does
	// not have to predict how many times the loop will ask for it.
	rounds [][]ports.StreamEvent
	// openErr is returned by Stream itself: a failure with no stream, which
	// the transport can still answer with a status code.
	openErr error
	// recvErr is returned after the script is drained, instead of io.EOF:
	// a failure once frames are already flowing.
	recvErr error
	// beforeEvent runs just before Recv returns script[i]. The abort test
	// uses it to cancel the request between frames.
	beforeEvent func(i int)

	models    []ports.Model
	modelsErr error
	prices    map[string]ports.Price
	pricesErr error
	keySpend  ports.KeySpend

	// Recorded for assertions.
	lastRequest ports.CompletionRequest
	lastCreds   ports.Credentials
	// requests is every completion request of the process, in order. A turn
	// can now make several, and the tool tests assert on what the second one
	// carried that the first did not.
	requests    []ports.CompletionRequest
	streamCalls int
	// keyInfoCalls exists so a test can assert that the local budget gate
	// never consults the gateway's own max_budget. The two layers are
	// deliberately separate; this is what proves they stayed that way.
	keyInfoCalls int
	closed       bool
}

func newFakeLLM() *fakeLLM {
	return &fakeLLM{
		script: []ports.StreamEvent{
			{Reasoning: "pensando um pouco"},
			{Delta: "olá"},
			{Delta: ", João"},
			{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 41, CompletionTokens: 17}},
		},
		models:   []ports.Model{{ID: "test-model", OwnedBy: "fixture"}},
		prices:   map[string]ports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}},
		keySpend: ports.KeySpend{KeyAlias: "fixture", Spend: 0.4242},
	}
}

// reply replaces the scripted answer, keeping the terminal usage frame.
func (f *fakeLLM) reply(text string, prompt, completion int) {
	f.script = []ports.StreamEvent{
		{Delta: text},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: prompt, CompletionTokens: completion}},
	}
}

func (f *fakeLLM) Stream(_ context.Context, req ports.CompletionRequest) (ports.Stream, error) {
	f.streamCalls++
	f.lastRequest = req
	f.lastCreds = req.Creds
	f.requests = append(f.requests, req)
	if f.openErr != nil {
		return nil, f.openErr
	}
	return &fakeStream{llm: f, script: f.scriptFor(f.streamCalls)}, nil
}

// scriptFor picks what the nth call (1-based) replays.
func (f *fakeLLM) scriptFor(call int) []ports.StreamEvent {
	if len(f.rounds) == 0 {
		return f.script
	}
	if call > len(f.rounds) {
		return f.rounds[len(f.rounds)-1]
	}
	return f.rounds[call-1]
}

func (f *fakeLLM) Models(_ context.Context, creds ports.Credentials) ([]ports.Model, error) {
	f.lastCreds = creds
	return f.models, f.modelsErr
}

func (f *fakeLLM) ModelPrices(_ context.Context, creds ports.Credentials) (map[string]ports.Price, error) {
	f.lastCreds = creds
	if f.pricesErr != nil {
		return nil, f.pricesErr
	}
	return f.prices, nil
}

func (f *fakeLLM) KeyInfo(_ context.Context, creds ports.Credentials) (ports.KeySpend, error) {
	f.keyInfoCalls++
	f.lastCreds = creds
	return f.keySpend, nil
}

type fakeStream struct {
	llm *fakeLLM
	// script is this call's frames, resolved when the stream opened. Held
	// per stream rather than read off the fake, so a multi-round turn does
	// not have every open racing the same cursor.
	script []ports.StreamEvent
	i      int
}

func (s *fakeStream) Recv() (ports.StreamEvent, error) {
	if s.i < len(s.script) {
		if s.llm.beforeEvent != nil {
			s.llm.beforeEvent(s.i)
		}
		ev := s.script[s.i]
		s.i++
		return ev, nil
	}
	if s.llm.recvErr != nil {
		return ports.StreamEvent{}, s.llm.recvErr
	}
	return ports.StreamEvent{}, io.EOF
}

func (s *fakeStream) Close() error {
	s.llm.closed = true
	return nil
}

/* ── SSE parsing ─────────────────────────────────────────────────────── */

type sseFrame struct {
	event string
	data  string
}

// parseSSE reads the recorded body back into frames. Deliberately a second
// implementation rather than a call into the frontend parser: a test that
// reuses the parser it is testing proves nothing.
func parseSSE(t *testing.T, body string) []sseFrame {
	t.Helper()
	var out []sseFrame
	for _, raw := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var f sseFrame
		var data []string
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				f.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		f.data = strings.Join(data, "\n")
		out = append(out, f)
	}
	return out
}

func eventNames(frames []sseFrame) []string {
	names := make([]string, 0, len(frames))
	for _, f := range frames {
		names = append(names, f.event)
	}
	return names
}

func frameOf(frames []sseFrame, event string) (sseFrame, bool) {
	for _, f := range frames {
		if f.event == event {
			return f, true
		}
	}
	return sseFrame{}, false
}

/* ── reads used by several tests ─────────────────────────────────────── */

type apiMessage struct {
	ID                    string   `json:"id"`
	Role                  string   `json:"role"`
	Content               string   `json:"content"`
	Reasoning             string   `json:"reasoning"`
	ReasoningMS           int      `json:"reasoning_ms"`
	Model                 string   `json:"model"`
	PromptTokens          int      `json:"prompt_tokens"`
	CompletionTokens      int      `json:"completion_tokens"`
	UsageSource           string   `json:"usage_source"`
	InputCostPerToken     *float64 `json:"input_cost_per_token"`
	OutputCostPerToken    *float64 `json:"output_cost_per_token"`
	Cost                  *float64 `json:"cost"`
	EstimatedPromptTokens *int     `json:"estimated_prompt_tokens"`
	// What the input was made of, when the provider reported it. Nil is
	// ABSENT — the gateway said nothing — and never a measured zero. See
	// migration 0019 and ports.Usage.
	CacheReadTokens     *int `json:"cache_read_tokens"`
	CacheCreationTokens *int `json:"cache_creation_tokens"`
	ReasoningTokens     *int `json:"reasoning_tokens"`
	// ContextReport is nil on user turns and on assistant turns written
	// before the column existed. See inspector_integration_test.go.
	ContextReport *apiContextReport `json:"context_report"`
	// References is the capability selection frozen on a user turn. Nil on
	// every assistant turn and on every turn that made no selection, which
	// is the same fact.
	References   []apiTurnReference `json:"references"`
	FinishReason string             `json:"finish_reason"`
	Error        string             `json:"error"`
	CreatedAt    string             `json:"created_at"`
	Seq          int64              `json:"seq"`
}

// apiContextReport mirrors domain.ContextReport on the wire. Declared in
// the shared harness because both the transcript and the `done` frame carry
// it, and both are read from more than one test file.
type apiContextReport struct {
	Blocks               []apiContextBlock `json:"blocks"`
	TotalCharacters      int               `json:"total_characters"`
	TotalEstimatedTokens int               `json:"total_estimated_tokens"`
	// Rounds is present only on a turn that called the provider more than
	// once, which today means a turn that used tools.
	Rounds []apiContextRound `json:"rounds"`
	// CacheBreakpointAfter names the block this turn asked the provider to
	// cache up to, or is empty when it asked for nothing.
	CacheBreakpointAfter string `json:"cache_breakpoint_after"`
}

type apiContextRound struct {
	Round                int      `json:"round"`
	AddedCharacters      int      `json:"added_characters"`
	AddedEstimatedTokens int      `json:"added_estimated_tokens"`
	PromptTokens         int      `json:"prompt_tokens"`
	CompletionTokens     int      `json:"completion_tokens"`
	UsageSource          string   `json:"usage_source"`
	Cost                 *float64 `json:"cost"`
	FinishReason         string   `json:"finish_reason"`
	Tools                []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		ErrorCode  string `json:"error_code"`
		DurationMS int    `json:"duration_ms"`
	} `json:"tools"`
	// Usage is the provider's own per-call detail, absent on a gateway that
	// reports only the two totals.
	Usage *apiRoundUsage `json:"usage"`
}

// apiRoundUsage mirrors domain.RoundUsage. Every field is a pointer for the
// reason the domain type gives: nil means the provider did not report it,
// which is a different fact from a measured zero.
type apiRoundUsage struct {
	TotalTokens         *int `json:"total_tokens"`
	CacheReadTokens     *int `json:"cache_read_tokens"`
	CacheCreationTokens *int `json:"cache_creation_tokens"`
	CachedTokens        *int `json:"cached_tokens"`
	ReasoningTokens     *int `json:"reasoning_tokens"`
}

type apiContextBlock struct {
	Kind            string                `json:"kind"`
	Items           int                   `json:"items"`
	Characters      int                   `json:"characters"`
	EstimatedTokens int                   `json:"estimated_tokens"`
	Exclusions      []apiContextExclusion `json:"exclusions"`
}

// apiTurnReference mirrors domain.TurnReference on the wire.
type apiTurnReference struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type apiContextExclusion struct {
	Reason     string `json:"reason"`
	Items      int    `json:"items"`
	Characters int    `json:"characters"`
}

// block finds one kind in the report, or fails.
func (r *apiContextReport) block(t *testing.T, kind string) apiContextBlock {
	t.Helper()
	for _, b := range r.Blocks {
		if b.Kind == kind {
			return b
		}
	}
	t.Fatalf("report has no %s block: %+v", kind, r.Blocks)
	return apiContextBlock{}
}

func (r *apiContextReport) hasBlock(kind string) bool {
	for _, b := range r.Blocks {
		if b.Kind == kind {
			return true
		}
	}
	return false
}

func (e *env) messages(ws uuid.UUID, conversationID string) []apiMessage {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/messages", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		Items []apiMessage `json:"items"`
	}](e.t, rec).Items
}

type usageLine struct {
	Model                string   `json:"model"`
	Messages             int64    `json:"messages"`
	PromptTokens         int64    `json:"prompt_tokens"`
	CompletionTokens     int64    `json:"completion_tokens"`
	TotalTokens          int64    `json:"total_tokens"`
	InputCostPerToken    *float64 `json:"input_cost_per_token"`
	OutputCostPerToken   *float64 `json:"output_cost_per_token"`
	Cost                 float64  `json:"cost"`
	UnpricedMessages     int64    `json:"unpriced_messages"`
	ProviderMessages     int64    `json:"provider_messages"`
	EstimatedMessages    int64    `json:"estimated_messages"`
	UnknownUsageMessages int64    `json:"unknown_usage_messages"`
}

type usageReport struct {
	Currency              string      `json:"currency"`
	From                  *string     `json:"from"`
	To                    *string     `json:"to"`
	Priced                bool        `json:"priced"`
	Messages              int64       `json:"messages"`
	TotalPromptTokens     int64       `json:"total_prompt_tokens"`
	TotalCompletionTokens int64       `json:"total_completion_tokens"`
	TotalTokens           int64       `json:"total_tokens"`
	EstimatedCost         float64     `json:"estimated_cost"`
	ProviderMessages      int64       `json:"provider_messages"`
	EstimatedMessages     int64       `json:"estimated_messages"`
	UnknownUsageMessages  int64       `json:"unknown_usage_messages"`
	UnpricedMessages      int64       `json:"unpriced_messages"`
	Lines                 []usageLine `json:"lines"`
}

// usage reads a usage report from any of the three levels.
func (e *env) usage(ws uuid.UUID, path string) usageReport {
	e.t.Helper()
	rec := e.do("GET", path, ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[usageReport](e.t, rec)
}

/* ── migrations ──────────────────────────────────────────────────────── */

// TestChatMigrationRoundTrip proves the chat timeline is reversible and
// that it uses its own version table. If the table flag were dropped, the
// Up below would be a no-op against a finance-migrated database and the
// schema assertion would fail.
func TestChatMigrationRoundTrip(t *testing.T) {
	d := dsn(t)
	m, err := migrate.New("file://../../migrations/chat", chatMigrateDSN(t, d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()

	for _, step := range []struct {
		name string
		fn   func() error
	}{
		{"up", m.Up},
		{"down", m.Down},
		{"up again", m.Up},
	} {
		if err := step.fn(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Fatalf("%s: %v", step.name, err)
		}
	}

	ctx := context.Background()
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 2, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	for _, table := range []string{"providers", "agents", "conversations", "messages"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'chat' AND table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("probe chat.%s: %v", table, err)
		}
		if !exists {
			t.Fatalf("chat.%s does not exist after up; is -table schema_migrations_chat wired?", table)
		}
	}

	// The version table itself is the point: chat must never advance the
	// finance one.
	var versionTable bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_name = 'schema_migrations_chat')`).Scan(&versionTable); err != nil {
		t.Fatalf("probe version table: %v", err)
	}
	if !versionTable {
		t.Fatal("schema_migrations_chat missing; the chat timeline is writing somewhere else")
	}
}

/* ── secrets ─────────────────────────────────────────────────────────── */

// TestProviderSecrecy is the invariant that has no second chance: a leaked
// provider key cannot be un-leaked. It checks all three surfaces the
// earlier audit checked by hand — response body, stored bytes, and what
// the adapter actually receives.
func TestProviderSecrecy(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/chat/providers", e.wsA, map[string]any{
		"name":          "LiteLLM",
		"base_url":      "https://gateway.invalid/v1/",
		"api_key":       testAPIKey,
		"default_model": "test-model",
	})
	wantStatus(t, rec, http.StatusCreated)

	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatalf("the api key came back in the create response: %s", rec.Body.String())
	}
	created := decode[map[string]any](t, rec)
	if hint, _ := created["api_key_hint"].(string); hint == "" {
		t.Fatal("api_key_hint is empty; the UI has no way to tell which key is stored")
	}
	if _, ok := created["api_key"]; ok {
		t.Fatal("the response carries an api_key field at all")
	}
	// Trailing slash is normalised so the adapter can append /chat/completions.
	if got := created["base_url"].(string); got != "https://gateway.invalid/v1" {
		t.Fatalf("base_url = %q, want the trailing slash trimmed", got)
	}

	id := created["id"].(string)

	// Reads must not leak it either.
	rec = e.do("GET", "/chat/providers/"+id, e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatal("the api key came back in the read response")
	}
	rec = e.do("GET", "/chat/providers", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatal("the api key came back in the list response")
	}

	// Stored bytes must be ciphertext, not the token with extra steps.
	var cipher []byte
	if err := e.pool.QueryRow(context.Background(),
		`SELECT api_key_cipher FROM chat.providers WHERE id = $1`, id).Scan(&cipher); err != nil {
		t.Fatalf("read cipher: %v", err)
	}
	if len(cipher) == 0 {
		t.Fatal("api_key_cipher is empty")
	}
	if bytes.Contains(cipher, []byte(testAPIKey)) {
		t.Fatal("the plaintext key is sitting inside api_key_cipher")
	}

	// And the round trip has to actually work: the adapter must receive the
	// original token, or the whole sealing exercise is decorative.
	rec = e.do("GET", "/chat/providers/"+id+"/models", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	if e.llm.lastCreds.APIKey != testAPIKey {
		t.Fatalf("adapter received %q, want the unsealed key", e.llm.lastCreds.APIKey)
	}
	if e.llm.lastCreds.BaseURL != "https://gateway.invalid/v1" {
		t.Fatalf("adapter received base_url %q", e.llm.lastCreds.BaseURL)
	}
}

// TestSecretsKeyMissing pins the 503 contract: the API boots, refuses to
// write or read a credential, and keeps serving everything that does not
// need the sealer.
func TestSecretsKeyMissing(t *testing.T) {
	e := newEnv(t, withoutSecretsKey())

	rec := e.do("POST", "/chat/providers", e.wsA, map[string]any{
		"name": "LiteLLM", "base_url": "https://gateway.invalid/v1",
		"api_key": testAPIKey, "default_model": "test-model",
	})
	wantErrorCode(t, rec, http.StatusServiceUnavailable, "not_configured")

	// Listing does not need the sealer, so it must keep working.
	rec = e.do("GET", "/chat/providers", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
}

/* ── referential guards ──────────────────────────────────────────────── */

// TestReferentialGuards covers the two 409s. They live in the application
// layer rather than in the FK because both parents are soft-deleted, and an
// UPDATE sails straight past ON DELETE RESTRICT. That makes them exactly
// the kind of check that breaks in silence.
func TestReferentialGuards(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("DELETE", "/chat/providers/"+s.providerID, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusConflict, "conflict")

	rec = e.do("DELETE", "/chat/agents/"+s.agentID, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusConflict, "conflict")

	// Remove the dependents bottom-up and the same deletes must now pass.
	wantStatus(t, e.do("DELETE", "/chat/conversations/"+s.conversationID, e.wsA, nil), http.StatusNoContent)
	wantStatus(t, e.do("DELETE", "/chat/agents/"+s.agentID, e.wsA, nil), http.StatusNoContent)
	wantStatus(t, e.do("DELETE", "/chat/providers/"+s.providerID, e.wsA, nil), http.StatusNoContent)

	// Soft delete means gone from the API, not gone from the table.
	rec = e.do("GET", "/chat/agents/"+s.agentID, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")
}

/* ── workspace isolation ─────────────────────────────────────────────── */

// TestWorkspaceIsolation is the single most important invariant in the
// module: every repository filters by workspace, and there is no global
// query. It is checked on all four tables, by list and by direct id.
func TestWorkspaceIsolation(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// A turn, so there is a message to try to read across the boundary.
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta secreta do workspace A"}), http.StatusOK)

	type probe struct {
		name string
		path string
	}
	// Lists must come back empty for the other workspace.
	for _, p := range []probe{
		{"providers", "/chat/providers"},
		{"agents", "/chat/agents"},
		{"conversations", "/chat/conversations"},
	} {
		rec := e.do("GET", p.path, e.wsB, nil)
		wantStatus(t, rec, http.StatusOK)
		items := decode[struct {
			Items []map[string]any `json:"items"`
		}](t, rec).Items
		if len(items) != 0 {
			t.Fatalf("%s: workspace B sees %d of workspace A's rows", p.name, len(items))
		}
	}

	// Direct reads by id must be 404, not 403 and not a leak.
	for _, p := range []probe{
		{"provider", "/chat/providers/" + s.providerID},
		{"agent", "/chat/agents/" + s.agentID},
		{"conversation", "/chat/conversations/" + s.conversationID},
		{"transcript", "/chat/conversations/" + s.conversationID + "/messages"},
		{"conversation usage", "/chat/conversations/" + s.conversationID + "/usage"},
		{"agent usage", "/chat/agents/" + s.agentID + "/usage"},
		{"provider spend", "/chat/providers/" + s.providerID + "/spend"},
	} {
		rec := e.do("GET", p.path, e.wsB, nil)
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
		if strings.Contains(rec.Body.String(), "pergunta secreta") {
			t.Fatalf("%s: workspace A content leaked into a workspace B response", p.name)
		}
	}

	// Writes across the boundary must not land either.
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsB,
		map[string]any{"content": "invasão"})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	rec = e.do("DELETE", "/chat/conversations/"+s.conversationID, e.wsB, nil)
	if rec.Code == http.StatusNoContent {
		// A soft delete that matched nothing is not an error, but it must
		// not have touched workspace A's row.
		if got := e.do("GET", "/chat/conversations/"+s.conversationID, e.wsA, nil); got.Code != http.StatusOK {
			t.Fatal("workspace B deleted workspace A's conversation")
		}
	}

	// And workspace A still sees everything it owns.
	if msgs := e.messages(e.wsA, s.conversationID); len(msgs) != 2 {
		t.Fatalf("workspace A now has %d messages, want 2", len(msgs))
	}
}

// TestConversationBelongsToItsAgent pins the ownership chain: a
// conversation created under agent A must not be reachable through another
// agent's id, and a conversation must refuse an agent that does not exist.
func TestConversationBelongsToItsAgent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/conversations", e.wsA, map[string]any{"agent_id": uuid.NewString()})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	// An agent from another workspace is equally unusable.
	other := e.seed(e.wsB)
	rec = e.do("POST", "/chat/conversations", e.wsA, map[string]any{"agent_id": other.agentID})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	// The real one works, and reports the agent it belongs to.
	rec = e.do("GET", "/chat/conversations/"+s.conversationID, e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	conv := decode[map[string]any](t, rec)
	if conv["agent_id"] != s.agentID {
		t.Fatalf("conversation.agent_id = %v, want %s", conv["agent_id"], s.agentID)
	}
}

/* ── the turn ────────────────────────────────────────────────────────── */

// TestSendMessageHappyPath is the payload contract. The Agents v1 roadmap
// replaces buildWireMessages with a context builder, and this
// test is what proves that replacement did not change what the provider
// receives.
func TestSendMessageHappyPath(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "bom dia, tudo certo com a acentuação?"})
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	frames := parseSSE(t, rec.Body.String())
	got := eventNames(frames)
	want := []string{"reasoning", "delta", "delta", "done"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("frames = %v, want %v", got, want)
	}
	// Reasoning always precedes the first answer token, and never rides
	// inside a delta.
	if frames[0].data != `{"text":"pensando um pouco"}` {
		t.Fatalf("reasoning frame = %s", frames[0].data)
	}

	done, _ := frameOf(frames, "done")
	if !strings.Contains(done.data, `"finish_reason":"stop"`) {
		t.Fatalf("done frame lost the finish reason: %s", done.data)
	}

	/* what the provider was asked */

	req := e.llm.lastRequest
	if len(req.Messages) != 2 {
		t.Fatalf("wire messages = %d, want system + user", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "você é o agente de testes do João" {
		t.Fatalf("first wire message = %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || !strings.Contains(req.Messages[1].Content, "acentuação") {
		t.Fatalf("second wire message = %+v", req.Messages[1])
	}
	if req.Model != "test-model" || req.MaxTokens != domain.DefaultMaxTokens {
		t.Fatalf("request = model %q, max_tokens %d", req.Model, req.MaxTokens)
	}
	// Spend attribution: pure metadata, but the gateway's own reports
	// depend on it and it is invisible from the UI.
	if req.User != s.agentID {
		t.Fatalf("user = %q, want the agent id", req.User)
	}
	for key, want := range map[string]string{
		"agent_id":        s.agentID,
		"conversation_id": s.conversationID,
		"workspace_id":    e.wsA.String(),
	} {
		if req.Metadata[key] != want {
			t.Fatalf("metadata[%s] = %q, want %q", key, req.Metadata[key], want)
		}
	}
	if !e.llm.closed {
		t.Fatal("the stream was never closed")
	}

	/* what was persisted */

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("roles = %s, %s", msgs[0].Role, msgs[1].Role)
	}
	if msgs[0].Seq >= msgs[1].Seq {
		t.Fatalf("seq is not increasing: %d then %d", msgs[0].Seq, msgs[1].Seq)
	}
	a := msgs[1]
	if a.Content != "olá, João" {
		t.Fatalf("assistant content = %q, want the deltas concatenated in order", a.Content)
	}
	if a.Reasoning != "pensando um pouco" {
		t.Fatalf("reasoning = %q, want it stored apart from content", a.Reasoning)
	}
	if a.PromptTokens != 41 || a.CompletionTokens != 17 {
		t.Fatalf("tokens = %d in / %d out, want 41 / 17", a.PromptTokens, a.CompletionTokens)
	}
	if a.Model != "test-model" || a.FinishReason != "stop" {
		t.Fatalf("stamp = model %q, finish %q", a.Model, a.FinishReason)
	}

	// The title is derived locally from the first user turn, without
	// spending a token on it.
	rec = e.do("GET", "/chat/conversations/"+s.conversationID, e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	conv := decode[map[string]any](t, rec)
	title, _ := conv["title"].(string)
	if title == "" || !strings.HasPrefix(title, "bom dia") {
		t.Fatalf("title = %q, want it derived from the first message", title)
	}
	if conv["last_message_at"] == nil {
		t.Fatal("last_message_at was not stamped; the sidebar ordering depends on it")
	}
	if e.llm.streamCalls != 1 {
		t.Fatalf("the provider was called %d times for one turn", e.llm.streamCalls)
	}
}

// TestHistoryReplayAndReasoningExclusion covers two things that are easy to
// break together: the tail of the thread is replayed on the next turn, and
// the reasoning channel never is. Replaying reasoning would silently change
// what the model sees and quietly multiply the bill.
func TestHistoryReplayAndReasoningExclusion(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "primeira pergunta"}), http.StatusOK)

	e.llm.reply("segunda resposta", 60, 20)
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "segunda pergunta"}), http.StatusOK)

	req := e.llm.lastRequest
	if len(req.Messages) != 4 {
		t.Fatalf("wire messages = %d, want system + 3 turns", len(req.Messages))
	}
	roles := []string{req.Messages[0].Role, req.Messages[1].Role, req.Messages[2].Role, req.Messages[3].Role}
	if strings.Join(roles, ",") != "system,user,assistant,user" {
		t.Fatalf("roles = %v", roles)
	}
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "pensando um pouco") {
			t.Fatal("the reasoning channel was replayed to the provider")
		}
	}
}

// TestHistoryLimitBoundsTheReplay pins the only cost control the module has
// today. The limit counts messages including the one just written, which is
// the behaviour a context budget has to preserve or deliberately change.
func TestHistoryLimitBoundsTheReplay(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"history_limit": 2, "system_prompt": ""})

	for i, text := range []string{"um", "dois", "três"} {
		e.llm.reply("resposta "+text, 10+i, 5)
		wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
			map[string]any{"content": "pergunta " + text}), http.StatusOK)
	}

	// history_limit 2 and no system prompt: the tail is the previous
	// assistant turn plus the question just asked.
	req := e.llm.lastRequest
	if len(req.Messages) != 2 {
		t.Fatalf("wire messages = %d, want exactly history_limit", len(req.Messages))
	}
	if req.Messages[0].Role != "assistant" || req.Messages[0].Content != "resposta dois" {
		t.Fatalf("first replayed message = %+v", req.Messages[0])
	}
	if req.Messages[1].Content != "pergunta três" {
		t.Fatalf("last replayed message = %+v", req.Messages[1])
	}

	// Nothing was dropped from storage, only from the replay.
	if msgs := e.messages(e.wsA, s.conversationID); len(msgs) != 6 {
		t.Fatalf("transcript has %d messages, want 6", len(msgs))
	}
}

// TestUpstreamFailurePreservesTheQuestion is the failure mode that would
// cost the user their typing. The question is written before the provider
// is called, on purpose, and the failed turn is recorded so the thread
// explains itself.
func TestUpstreamFailurePreservesTheQuestion(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.openErr = domain.Upstream("LLM provider returned 401: invalid api key")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta que vai falhar"})
	// Nothing was streamed, so the transport still owns the status code.
	wantErrorCode(t, rec, http.StatusBadGateway, "upstream")

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the failed turn", len(msgs))
	}
	if msgs[0].Content != "pergunta que vai falhar" {
		t.Fatalf("the question was lost: %+v", msgs[0])
	}
	if msgs[1].FinishReason != string(domain.FinishError) {
		t.Fatalf("assistant finish_reason = %q, want error", msgs[1].FinishReason)
	}
	if msgs[1].Error == "" {
		t.Fatal("the failed turn carries no reason")
	}
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatal("the upstream error echoed the api key")
	}
}

// TestMidStreamFailureKeepsPartialText covers the other half: once frames
// are flowing there is no status code left, so the failure arrives inside
// the stream and whatever text already landed is kept.
func TestMidStreamFailureKeepsPartialText(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{{Delta: "começo da resposta"}}
	e.llm.recvErr = domain.Upstream("the gateway hung up mid-stream")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta que falha no meio"})
	// The headers went out with the first delta, so this is a 200 carrying
	// an error frame.
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	if names := eventNames(frames); strings.Join(names, ",") != "delta,error" {
		t.Fatalf("frames = %v, want delta then error", names)
	}
	errFrame, _ := frameOf(frames, "error")
	if !strings.Contains(errFrame.data, `"code":"upstream"`) {
		t.Fatalf("error frame = %s, want the wire error contract", errFrame.data)
	}

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want 2", len(msgs))
	}
	if msgs[1].Content != "começo da resposta" {
		t.Fatalf("partial text was dropped: %q", msgs[1].Content)
	}
	if msgs[1].FinishReason != string(domain.FinishError) {
		t.Fatalf("finish_reason = %q, want error", msgs[1].FinishReason)
	}
}

// TestClientDisconnectStillPersists exercises the abort path, which the
// module has carried as UNVERIFIED since it was written: a reader that goes
// away mid-stream must not cost the answer that already arrived. The
// write-back runs on a context detached from the request precisely for
// this.
func TestClientDisconnectStillPersists(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.llm.script = []ports.StreamEvent{
		{Delta: "primeiro pedaço"},
		{Delta: ", segundo pedaço"},
		{Delta: ", terceiro pedaço que o provider nunca chega a mandar"},
	}
	// Hang up just before the second delta would be written out. The
	// second chunk has already been produced by the provider at that
	// point, so it is kept; the third is never pulled, because a reader
	// that is gone is a reason to stop paying for tokens.
	e.llm.beforeEvent = func(i int) {
		if i == 1 {
			cancel()
		}
	}

	rec := e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta interrompida"})
	_ = rec // the client is gone; whatever the recorder holds is irrelevant

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the aborted turn", len(msgs))
	}
	if msgs[1].FinishReason != string(domain.FinishAborted) {
		t.Fatalf("finish_reason = %q, want aborted", msgs[1].FinishReason)
	}
	if msgs[1].Content != "primeiro pedaço, segundo pedaço" {
		t.Fatalf("assistant content = %q, want everything the provider actually produced", msgs[1].Content)
	}
	if strings.Contains(msgs[1].Content, "terceiro") {
		t.Fatal("the stream kept pulling tokens after the reader was gone")
	}
	if msgs[1].Error != "" {
		t.Fatalf("an abort was recorded as an error: %q", msgs[1].Error)
	}
}

// TestEmptyContentIsRejected: a blank turn must not reach the provider, and
// must not leave a row behind.
func TestEmptyContentIsRejected(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "   \n  "})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	if e.llm.streamCalls != 0 {
		t.Fatal("a blank message reached the provider")
	}
	if msgs := e.messages(e.wsA, s.conversationID); len(msgs) != 0 {
		t.Fatalf("transcript has %d messages after a rejected turn", len(msgs))
	}
}

/* ── truncate and regenerate ─────────────────────────────────────────── */

// TestTruncateAndRegenerate covers the operation the UI cannot do on its
// own. Both regenerate and edit mean "replace this turn"; getting it wrong
// leaves the thread reading as if the question had been asked twice, and
// feeds the model that duplicate forever after.
func TestTruncateAndRegenerate(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	path := "/chat/conversations/" + s.conversationID + "/messages"

	wantStatus(t, e.do("POST", path, e.wsA, map[string]any{"content": "primeira"}), http.StatusOK)
	e.llm.reply("segunda resposta", 20, 8)
	wantStatus(t, e.do("POST", path, e.wsA, map[string]any{"content": "segunda"}), http.StatusOK)

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 4 {
		t.Fatalf("transcript has %d messages, want 4", len(msgs))
	}
	lastUser := msgs[2]
	lastAssistant := msgs[3]
	maxSeqBefore := lastAssistant.Seq

	// Cutting from an assistant turn is refused: it would leave a question
	// hanging with no reply and no way to re-ask it without duplicating.
	rec := e.do("DELETE", path+"/"+itoa(lastAssistant.Seq), e.wsA, nil)
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	if len(e.messages(e.wsA, s.conversationID)) != 4 {
		t.Fatal("a refused truncate still deleted rows")
	}

	// Cutting from the user turn removes it and everything after.
	rec = e.do("DELETE", path+"/"+itoa(lastUser.Seq), e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decode[map[string]int64](t, rec)["deleted"]; got != 2 {
		t.Fatalf("deleted = %d, want 2", got)
	}
	msgs = e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages after truncate, want 2", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("truncate cut in the wrong place: %+v", msgs)
	}

	// Regenerate: the same question again. The thread must end with one
	// question and one fresh answer, not two of each.
	e.llm.reply("resposta regenerada", 22, 9)
	wantStatus(t, e.do("POST", path, e.wsA, map[string]any{"content": "segunda"}), http.StatusOK)

	msgs = e.messages(e.wsA, s.conversationID)
	if len(msgs) != 4 {
		t.Fatalf("transcript has %d messages after regenerate, want 4", len(msgs))
	}
	if msgs[3].Content != "resposta regenerada" {
		t.Fatalf("last answer = %q", msgs[3].Content)
	}
	// seq is not recycled: the new rows sit above the deleted ones, which
	// is what keeps ordering stable across a truncate.
	if msgs[2].Seq <= maxSeqBefore {
		t.Fatalf("seq was recycled: new user turn is %d, deleted max was %d", msgs[2].Seq, maxSeqBefore)
	}

	// Truncating something that is not there is a 404, not a silent no-op.
	rec = e.do("DELETE", path+"/99999", e.wsA, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	// And a seq of zero or below never reaches the service.
	rec = e.do("DELETE", path+"/0", e.wsA, nil)
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
}

/* ── usage ───────────────────────────────────────────────────────────── */

// TestUsageAggregation checks the arithmetic and, more importantly, the
// honesty of the unpriced path: when the rate card cannot be read the
// tokens stay exact and every cost is zero, flagged by priced=false. Any
// budget built on top of this has to know the difference.
func TestUsageAggregation(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	path := "/chat/conversations/" + s.conversationID + "/messages"

	e.llm.reply("resposta um", 41, 17)
	wantStatus(t, e.do("POST", path, e.wsA, map[string]any{"content": "um"}), http.StatusOK)
	e.llm.reply("resposta dois", 59, 23)
	wantStatus(t, e.do("POST", path, e.wsA, map[string]any{"content": "dois"}), http.StatusOK)

	// 100 prompt tokens at 1e-6 plus 40 completion tokens at 3e-6.
	const wantCost = 100*1e-6 + 40*3e-6

	got := e.usage(e.wsA, "/chat/conversations/"+s.conversationID+"/usage")
	if !got.Priced {
		t.Fatal("priced = false although both turns were stamped with a cost")
	}
	if got.TotalPromptTokens != 100 || got.TotalCompletionTokens != 40 {
		t.Fatalf("tokens = %d in / %d out, want 100 / 40", got.TotalPromptTokens, got.TotalCompletionTokens)
	}
	if got.TotalTokens != 140 {
		t.Fatalf("total_tokens = %d, want 140", got.TotalTokens)
	}
	if !nearly(got.EstimatedCost, wantCost) {
		t.Fatalf("estimated_cost = %v, want %v", got.EstimatedCost, wantCost)
	}
	if len(got.Lines) != 1 || got.Lines[0].Model != "test-model" || got.Lines[0].Messages != 2 {
		t.Fatalf("lines = %+v, want one line for test-model with 2 messages", got.Lines)
	}
	if got.ProviderMessages != 2 || got.EstimatedMessages != 0 || got.UnknownUsageMessages != 0 {
		t.Fatalf("source counts = %d provider / %d estimated / %d unknown, want 2/0/0",
			got.ProviderMessages, got.EstimatedMessages, got.UnknownUsageMessages)
	}

	// The agent report aggregates the same numbers across its conversations,
	// and so does the workspace one, which has no agent or conversation in
	// its path at all.
	for _, path := range []string{
		"/chat/agents/" + s.agentID + "/usage",
		"/chat/usage",
	} {
		wider := e.usage(e.wsA, path)
		if wider.TotalPromptTokens != got.TotalPromptTokens ||
			wider.TotalCompletionTokens != got.TotalCompletionTokens ||
			!nearly(wider.EstimatedCost, got.EstimatedCost) {
			t.Fatalf("%s = %+v, does not match the single conversation", path, wider)
		}
	}

	// The rate card going down no longer touches a report: the cost of a
	// turn was decided when the turn happened, and reading it back is a
	// database query, not a gateway call.
	//
	// This replaces the older assertion that an unreachable price list
	// zeroed every cost — the behaviour turn accounting exists to remove.
	e.llm.pricesErr = domain.Upstream("model info unavailable")
	after := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if !after.Priced || !nearly(after.EstimatedCost, wantCost) {
		t.Fatalf("an unreachable rate card changed a historical report: priced=%v cost=%v, want true and %v",
			after.Priced, after.EstimatedCost, wantCost)
	}
	if after.TotalPromptTokens != 100 || after.TotalCompletionTokens != 40 {
		t.Fatalf("tokens = %+v, want the counts intact", after)
	}
}

// TestUsageIsAttributedToTheRightTurn: two agents, two conversations, one
// workspace. Usage must not bleed across either boundary.
func TestUsageIsAttributedToTheRightTurn(t *testing.T) {
	e := newEnv(t)
	first := e.seed(e.wsA)
	second := e.seed(e.wsA)

	e.llm.reply("resposta do primeiro", 10, 5)
	wantStatus(t, e.do("POST", "/chat/conversations/"+first.conversationID+"/messages", e.wsA,
		map[string]any{"content": "para o primeiro"}), http.StatusOK)

	e.llm.reply("resposta do segundo", 70, 30)
	wantStatus(t, e.do("POST", "/chat/conversations/"+second.conversationID+"/messages", e.wsA,
		map[string]any{"content": "para o segundo"}), http.StatusOK)

	for _, tc := range []struct {
		name             string
		agentID          string
		prompt, complete int64
	}{
		{"first", first.agentID, 10, 5},
		{"second", second.agentID, 70, 30},
	} {
		rec := e.do("GET", "/chat/agents/"+tc.agentID+"/usage", e.wsA, nil)
		wantStatus(t, rec, http.StatusOK)
		got := decode[usageReport](t, rec)
		if got.TotalPromptTokens != tc.prompt || got.TotalCompletionTokens != tc.complete {
			t.Fatalf("%s agent usage = %d/%d, want %d/%d",
				tc.name, got.TotalPromptTokens, got.TotalCompletionTokens, tc.prompt, tc.complete)
		}
	}
}

// TestProviderSpendIsSeparateFromTheEstimate pins the distinction the UI
// depends on: the estimate is ours, the spend is the gateway's.
func TestProviderSpendIsSeparateFromTheEstimate(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	budget := 10.0
	e.llm.keySpend = ports.KeySpend{KeyAlias: "fixture", Spend: 0.4242, MaxBudget: &budget}

	rec := e.do("GET", "/chat/providers/"+s.providerID+"/spend", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	got := decode[struct {
		KeyAlias  string   `json:"key_alias"`
		Spend     float64  `json:"spend"`
		MaxBudget *float64 `json:"max_budget"`
	}](t, rec)
	if got.Spend != 0.4242 || got.MaxBudget == nil || *got.MaxBudget != 10 {
		t.Fatalf("spend = %+v", got)
	}
}

/* ── turn accounting ─────────────────────────────────────────────────── */

// send runs one turn and asserts it was accepted, which is the preamble of
// every accounting test below.
func (e *env) send(ws uuid.UUID, conversationID, content string) {
	e.t.Helper()
	rec := e.do("POST", "/chat/conversations/"+conversationID+"/messages", ws,
		map[string]any{"content": content})
	wantStatus(e.t, rec, http.StatusOK)
}

// assistantTurns filters a transcript down to the model's turns, which are
// the only rows that carry accounting.
func (e *env) assistantTurns(ws uuid.UUID, conversationID string) []apiMessage {
	e.t.Helper()
	var out []apiMessage
	for _, m := range e.messages(ws, conversationID) {
		if m.Role == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

// TestHistoricalCostSurvivesAPriceChange is the invariant turn accounting exists
// for. Two identical turns, a price change between them: each keeps what it
// cost when it ran, and the report is their sum — not two turns repriced at
// whatever the gateway quotes now.
//
// A report that reached for the current rate card would return twice the
// new price here, and this test is what says so.
func TestHistoricalCostSurvivesAPriceChange(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Turn one, at the opening rate card.
	e.llm.prices = map[string]ports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}}
	e.llm.reply("resposta barata", 100, 10)
	e.send(e.wsA, s.conversationID, "primeira")
	const cheap = 100*1e-6 + 10*3e-6

	// The gateway raises its prices tenfold.
	e.llm.prices = map[string]ports.Price{"test-model": {InputCostPerToken: 10e-6, OutputCostPerToken: 30e-6}}
	e.llm.reply("resposta cara", 100, 10)
	e.send(e.wsA, s.conversationID, "segunda")
	const dear = 100*10e-6 + 10*30e-6

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 2 {
		t.Fatalf("assistant turns = %d, want 2", len(turns))
	}
	for i, want := range []float64{cheap, dear} {
		if turns[i].Cost == nil {
			t.Fatalf("turn %d has no cost", i)
		}
		if !nearly(*turns[i].Cost, want) {
			t.Fatalf("turn %d cost = %v, want %v", i, *turns[i].Cost, want)
		}
	}
	// The rate card each turn was priced at is stamped on the turn, which
	// is what makes the cost above reproducible without the gateway.
	if turns[0].InputCostPerToken == nil || *turns[0].InputCostPerToken != 1e-6 {
		t.Fatalf("turn 0 input rate = %v, want the rate in force when it ran", turns[0].InputCostPerToken)
	}
	if turns[1].InputCostPerToken == nil || *turns[1].InputCostPerToken != 10e-6 {
		t.Fatalf("turn 1 input rate = %v, want the raised rate", turns[1].InputCostPerToken)
	}

	rep := e.usage(e.wsA, "/chat/conversations/"+s.conversationID+"/usage")
	if !nearly(rep.EstimatedCost, cheap+dear) {
		t.Fatalf("report cost = %v, want %v (the two turns as they happened, not %v repriced)",
			rep.EstimatedCost, cheap+dear, 2*dear)
	}
	// The group spans a price change, so there is no single unit rate to
	// report for it. Null, not the latest one, and certainly not zero.
	if rep.Lines[0].InputCostPerToken != nil {
		t.Fatalf("line reported a unit rate of %v across a price change", *rep.Lines[0].InputCostPerToken)
	}

	// One more turn back at the old price proves the direction is not
	// "always the newest": history is not rewritten downward either.
	e.llm.prices = map[string]ports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}}
	e.llm.reply("terceira", 100, 10)
	e.send(e.wsA, s.conversationID, "terceira")
	rep = e.usage(e.wsA, "/chat/conversations/"+s.conversationID+"/usage")
	if !nearly(rep.EstimatedCost, cheap+dear+cheap) {
		t.Fatalf("report cost = %v, want %v", rep.EstimatedCost, cheap+dear+cheap)
	}
}

// TestTurnWithoutProviderUsageIsEstimated: a gateway that never sends a
// usage frame must not produce a free turn. The counts become the
// characters/4 estimate and are labelled as such, everywhere.
func TestTurnWithoutProviderUsageIsEstimated(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// A complete, successful stream — with no usage frame anywhere in it.
	e.llm.script = []ports.StreamEvent{
		{Delta: "uma resposta inteira sem frame de usage"},
		{FinishReason: "stop"},
	}
	e.send(e.wsA, s.conversationID, "pergunta sem usage")

	turns := e.assistantTurns(e.wsA, s.conversationID)
	a := turns[0]
	if a.UsageSource != "estimated" {
		t.Fatalf("usage_source = %q, want estimated", a.UsageSource)
	}
	if a.PromptTokens == 0 || a.CompletionTokens == 0 {
		t.Fatalf("tokens = %d/%d; a turn the model answered was recorded as free",
			a.PromptTokens, a.CompletionTokens)
	}
	if a.Cost == nil || *a.Cost <= 0 {
		t.Fatalf("cost = %v, want the estimate priced", a.Cost)
	}

	rep := e.usage(e.wsA, "/chat/conversations/"+s.conversationID+"/usage")
	if rep.EstimatedMessages != 1 || rep.ProviderMessages != 0 {
		t.Fatalf("source counts = %d provider / %d estimated, want 0/1",
			rep.ProviderMessages, rep.EstimatedMessages)
	}
	if rep.TotalTokens == 0 {
		t.Fatal("the report shows zero tokens for a turn that was answered")
	}
	// The estimate is declared, not laundered into the exact number.
	if rep.EstimatedMessages+rep.ProviderMessages+rep.UnknownUsageMessages != rep.Messages {
		t.Fatalf("source counts do not account for %d messages", rep.Messages)
	}
}

// TestUnknownAccountingIsNeverZero covers the two ways a turn ends up with
// something we do not know, and pins that neither is written as a confident
// zero: a call that never opened a stream, and a turn whose price could not
// be read.
func TestUnknownAccountingIsNeverZero(t *testing.T) {
	t.Run("a call that never reached the model", func(t *testing.T) {
		e := newEnv(t)
		s := e.seed(e.wsA)
		e.llm.openErr = domain.Upstream("the gateway refused the request")

		rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
			map[string]any{"content": "pergunta que nunca sai"})
		wantErrorCode(t, rec, http.StatusBadGateway, "upstream")

		a := e.assistantTurns(e.wsA, s.conversationID)[0]
		if a.UsageSource != "unknown" {
			t.Fatalf("usage_source = %q, want unknown for a call that may never have happened", a.UsageSource)
		}
		if a.Cost != nil {
			t.Fatalf("cost = %v, want null — we do not know that this was free", *a.Cost)
		}
		// The prediction survives even here: what we were about to send is
		// knowable regardless of how the turn ended.
		if a.EstimatedPromptTokens == nil || *a.EstimatedPromptTokens == 0 {
			t.Fatalf("estimated_prompt_tokens = %v, want the context estimate", a.EstimatedPromptTokens)
		}

		rep := e.usage(e.wsA, "/chat/conversations/"+s.conversationID+"/usage")
		if rep.UnknownUsageMessages != 1 || rep.UnpricedMessages != 1 {
			t.Fatalf("report = %d unknown / %d unpriced, want 1/1", rep.UnknownUsageMessages, rep.UnpricedMessages)
		}
		if rep.Priced {
			t.Fatal("priced = true while a turn in the report has no known cost")
		}
	})

	t.Run("a turn whose rate card could not be read", func(t *testing.T) {
		e := newEnv(t)
		s := e.seed(e.wsA)
		e.llm.pricesErr = domain.Upstream("model info unavailable")
		e.llm.reply("respondeu, mas sem preço conhecido", 41, 17)
		e.send(e.wsA, s.conversationID, "pergunta sem tabela de preço")

		a := e.assistantTurns(e.wsA, s.conversationID)[0]
		// Real usage is not downgraded by a missing price.
		if a.UsageSource != "provider" || a.PromptTokens != 41 || a.CompletionTokens != 17 {
			t.Fatalf("usage = %q %d/%d, want the provider's numbers intact",
				a.UsageSource, a.PromptTokens, a.CompletionTokens)
		}
		if a.Cost != nil {
			t.Fatalf("cost = %v with no rate card, want null", *a.Cost)
		}
		if a.InputCostPerToken != nil || a.OutputCostPerToken != nil {
			t.Fatal("a rate was stamped from a card that could not be read")
		}

		rep := e.usage(e.wsA, "/chat/conversations/"+s.conversationID+"/usage")
		if rep.EstimatedCost != 0 || rep.Priced {
			t.Fatalf("report = cost %v priced %v, want 0 and false", rep.EstimatedCost, rep.Priced)
		}
		// Zero cost and unknown cost look identical in the money column.
		// This counter is the only thing that tells them apart.
		if rep.UnpricedMessages != 1 {
			t.Fatalf("unpriced_messages = %d, want 1 — otherwise the zero above reads as free",
				rep.UnpricedMessages)
		}
		if rep.TotalPromptTokens != 41 {
			t.Fatalf("tokens = %d, want them recorded even unpriced", rep.TotalPromptTokens)
		}
	})
}

// TestInterruptedTurnsRecordWhatIsKnown: a stream that dies mid-flight and
// a reader that hangs up both leave a real bill behind at the provider.
// Neither may be recorded as a free turn.
func TestInterruptedTurnsRecordWhatIsKnown(t *testing.T) {
	t.Run("mid-stream failure", func(t *testing.T) {
		e := newEnv(t)
		s := e.seed(e.wsA)
		e.llm.script = []ports.StreamEvent{{Delta: "começo da resposta"}}
		e.llm.recvErr = domain.Upstream("the gateway hung up mid-stream")

		e.send(e.wsA, s.conversationID, "pergunta que falha no meio")

		a := e.assistantTurns(e.wsA, s.conversationID)[0]
		if a.FinishReason != string(domain.FinishError) {
			t.Fatalf("finish_reason = %q, want error", a.FinishReason)
		}
		// The stream opened, so input tokens were charged whatever happened
		// afterwards. Estimated, declared, and not zero.
		if a.UsageSource != "estimated" || a.PromptTokens == 0 {
			t.Fatalf("usage = %q %d in; a failed turn was recorded as costing nothing",
				a.UsageSource, a.PromptTokens)
		}
	})

	t.Run("client disconnect", func(t *testing.T) {
		e := newEnv(t)
		s := e.seed(e.wsA)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		e.llm.script = []ports.StreamEvent{{Delta: "primeiro"}, {Delta: ", segundo"}}
		e.llm.beforeEvent = func(i int) {
			if i == 1 {
				cancel()
			}
		}
		_ = e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
			map[string]any{"content": "pergunta interrompida"})

		a := e.assistantTurns(e.wsA, s.conversationID)[0]
		if a.FinishReason != string(domain.FinishAborted) {
			t.Fatalf("finish_reason = %q, want aborted", a.FinishReason)
		}
		if a.UsageSource != "estimated" || a.PromptTokens == 0 {
			t.Fatalf("usage = %q %d in; an aborted turn was recorded as free",
				a.UsageSource, a.PromptTokens)
		}
		// The write-back runs detached, so pricing it must survive the
		// cancelled request too.
		if a.Cost == nil {
			t.Fatal("an aborted turn lost its cost because the request context was gone")
		}
	})
}

// TestModelIdentityIsHistorical: an agent's model can be changed, and the
// answers it already gave must keep saying which model actually produced
// them — both on the message and in the report that groups by model.
func TestModelIdentityIsHistorical(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"model": "modelo-antigo"})
	e.llm.prices = map[string]ports.Price{
		"modelo-antigo": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6},
		"modelo-novo":   {InputCostPerToken: 5e-6, OutputCostPerToken: 9e-6},
	}

	e.llm.reply("resposta do modelo antigo", 100, 10)
	e.send(e.wsA, s.conversationID, "pergunta de ontem")

	// The agent is reconfigured. Nothing about yesterday changed.
	rec := e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA, map[string]any{"model": "modelo-novo"})
	wantStatus(t, rec, http.StatusOK)

	a := e.assistantTurns(e.wsA, s.conversationID)[0]
	if a.Model != "modelo-antigo" {
		t.Fatalf("model = %q, want modelo-antigo — the turn is claiming to come from today's configuration", a.Model)
	}
	if a.InputCostPerToken == nil || *a.InputCostPerToken != 1e-6 {
		t.Fatalf("input rate = %v, want the old model's", a.InputCostPerToken)
	}

	rep := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if len(rep.Lines) != 1 || rep.Lines[0].Model != "modelo-antigo" {
		t.Fatalf("lines = %+v, want the turn grouped under the model that produced it", rep.Lines)
	}

	// A new turn lands under the new model, side by side with the old one.
	e.llm.reply("resposta do modelo novo", 100, 10)
	e.send(e.wsA, s.conversationID, "pergunta de hoje")
	rep = e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if len(rep.Lines) != 2 {
		t.Fatalf("lines = %+v, want one per model", rep.Lines)
	}
	byModel := map[string]usageLine{}
	for _, l := range rep.Lines {
		byModel[l.Model] = l
	}
	if _, ok := byModel["modelo-antigo"]; !ok {
		t.Fatal("the old model's line disappeared when the agent was reconfigured")
	}
	if !nearly(byModel["modelo-novo"].Cost, 100*5e-6+10*9e-6) {
		t.Fatalf("new model cost = %v", byModel["modelo-novo"].Cost)
	}
}

// TestUsageWindow: a report can be asked for a slice of time, and the slice
// is decided by when each turn happened, not by when its thread started.
func TestUsageWindow(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.llm.reply("resposta antiga", 100, 0)
	e.send(e.wsA, s.conversationID, "há três dias")
	// Backdate that one turn. The conversation itself stays "today", which
	// is exactly the confusion the per-message timestamp avoids.
	old := e.assistantTurns(e.wsA, s.conversationID)[0]
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE chat.messages SET created_at = now() - interval '3 days' WHERE id = $1`, old.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	e.llm.reply("resposta de hoje", 7, 0)
	e.send(e.wsA, s.conversationID, "agora")

	now := time.Now().UTC()
	rfc := func(t time.Time) string { return t.Format(time.RFC3339) }

	cases := []struct {
		name  string
		query string
		want  int64
	}{
		{"lifetime", "", 107},
		{"since yesterday", "?from=" + rfc(now.AddDate(0, 0, -1)), 7},
		{"before yesterday", "?to=" + rfc(now.AddDate(0, 0, -1)), 100},
		{"a closed window around the old turn",
			"?from=" + rfc(now.AddDate(0, 0, -4)) + "&to=" + rfc(now.AddDate(0, 0, -2)), 100},
		{"a window with nothing in it",
			"?from=" + rfc(now.AddDate(0, 0, -30)) + "&to=" + rfc(now.AddDate(0, 0, -20)), 0},
	}
	for _, tc := range cases {
		for _, base := range []string{
			"/chat/usage",
			"/chat/agents/" + s.agentID + "/usage",
			"/chat/conversations/" + s.conversationID + "/usage",
		} {
			got := e.usage(e.wsA, base+tc.query)
			if got.TotalPromptTokens != tc.want {
				t.Fatalf("%s %s: prompt tokens = %d, want %d", base, tc.name, got.TotalPromptTokens, tc.want)
			}
		}
	}

	// The window is echoed back, so a cached report cannot be mistaken for
	// a different period.
	rep := e.usage(e.wsA, "/chat/usage?from="+rfc(now.AddDate(0, 0, -1)))
	if rep.From == nil || rep.To != nil {
		t.Fatalf("window echoed as from=%v to=%v", rep.From, rep.To)
	}

	// A window that cannot be parsed is a bad request, not a silent
	// lifetime total dressed up as a day.
	wantErrorCode(t, e.do("GET", "/chat/usage?from=ontem", e.wsA, nil), http.StatusBadRequest, "invalid")
	wantErrorCode(t, e.do("GET", "/chat/usage?from="+rfc(now)+"&to="+rfc(now.AddDate(0, 0, -1)),
		e.wsA, nil), http.StatusBadRequest, "invalid")
}

// TestWorkspaceUsageIsIsolated: the report that has no agent and no
// conversation in its path still cannot see another workspace's spend.
func TestWorkspaceUsageIsIsolated(t *testing.T) {
	e := newEnv(t)
	a := e.seed(e.wsA)
	b := e.seed(e.wsB)

	e.llm.reply("resposta de A", 100, 10)
	e.send(e.wsA, a.conversationID, "de A")
	e.llm.reply("resposta de B", 7, 3)
	e.send(e.wsB, b.conversationID, "de B")

	for _, tc := range []struct {
		ws               uuid.UUID
		prompt, complete int64
	}{
		{e.wsA, 100, 10},
		{e.wsB, 7, 3},
	} {
		got := e.usage(tc.ws, "/chat/usage")
		if got.TotalPromptTokens != tc.prompt || got.TotalCompletionTokens != tc.complete {
			t.Fatalf("workspace usage = %d/%d, want %d/%d — spend crossed a workspace boundary",
				got.TotalPromptTokens, got.TotalCompletionTokens, tc.prompt, tc.complete)
		}
	}
}

// TestRowsPredatingTheAccountingMigration: migration 0007 landed on a
// populated table, and this is what it may and may not claim about what was
// already there.
//
// It runs the timeline back to 0006, writes rows exactly as the module
// wrote them then, and migrates forward — which is the only way to exercise
// the backfill rather than the column defaults.
func TestRowsPredatingTheAccountingMigration(t *testing.T) {
	d := dsn(t)
	freshDB(t, d)
	ctx := context.Background()

	m, err := migrate.New("file://../../migrations/chat", chatMigrateDSN(t, d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	// 0006 is the last version before turn accounting existed.
	if err := m.Migrate(6); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 6: %v", err)
	}

	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 2, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// The provider → agent → conversation chain, by hand: the module's own
	// writers are not available at this schema version.
	ws := uuid.New()
	var providerID, agentID, conversationID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.providers (workspace_id, name, base_url, api_key_cipher, default_model)
		 VALUES ($1, 'legado', 'https://gateway.invalid/v1', '\x00'::bytea, 'test-model') RETURNING id`,
		ws).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.agents (workspace_id, provider_id, name, model)
		 VALUES ($1, $2, 'agente legado', 'test-model') RETURNING id`,
		ws, providerID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.conversations (workspace_id, agent_id) VALUES ($1, $2) RETURNING id`,
		ws, agentID).Scan(&conversationID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	// Two assistant turns as 0006 stored them, and the user turn beside
	// them. Only the first carries counts.
	rows := []struct {
		label            string
		role             string
		content          string
		prompt, complete int
		finish           string
		// wantSource is what the backfill is entitled to conclude.
		wantSource string
	}{
		{"answered", "assistant", "resposta antiga", 900, 300, "stop", "provider"},
		{"failed", "assistant", "", 0, 0, "error", "unknown"},
		{"question", "user", "pergunta antiga", 0, 0, "", "unknown"},
	}
	ids := make(map[string]uuid.UUID, len(rows))
	for _, r := range rows {
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO chat.messages
			 (workspace_id, conversation_id, role, content, model, prompt_tokens, completion_tokens, finish_reason)
			 VALUES ($1, $2, $3, $4, 'test-model', $5, $6, $7) RETURNING id`,
			ws, conversationID, r.role, r.content, r.prompt, r.complete, r.finish).Scan(&id); err != nil {
			t.Fatalf("seed %s row: %v", r.label, err)
		}
		ids[r.label] = id
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to accounting: %v", err)
	}

	for _, r := range rows {
		var source string
		var cost, inRate *float64
		var estimate *int
		if err := pool.QueryRow(ctx,
			`SELECT usage_source, cost, input_cost_per_token, estimated_prompt_tokens
			   FROM chat.messages WHERE id = $1`, ids[r.label]).
			Scan(&source, &cost, &inRate, &estimate); err != nil {
			t.Fatalf("read %s row: %v", r.label, err)
		}
		// A non-zero count could only have come from a provider usage frame:
		// nothing else ever wrote those columns. That is the whole of what
		// the backfill may claim.
		if source != r.wantSource {
			t.Fatalf("%s row: usage_source = %q, want %q", r.label, source, r.wantSource)
		}
		// And this is what it may not: we do not know what the rate card
		// said back then, and an invented number would be worse than none.
		if cost != nil || inRate != nil || estimate != nil {
			t.Fatalf("%s row was given precision it never had: cost=%v rate=%v estimate=%v",
				r.label, cost, inRate, estimate)
		}
	}

	// Read back through the real service: old rows still count their tokens,
	// and the report says out loud that their cost is missing rather than
	// reporting a total that looks complete.
	e := newEnvOn(t, d)
	rep := e.usage(ws, "/chat/conversations/"+conversationID.String()+"/usage")
	if rep.TotalPromptTokens != 900 || rep.TotalCompletionTokens != 300 {
		t.Fatalf("legacy tokens = %d/%d, want 900/300 — old rows still count",
			rep.TotalPromptTokens, rep.TotalCompletionTokens)
	}
	if rep.ProviderMessages != 1 || rep.UnknownUsageMessages != 1 {
		t.Fatalf("source counts = %d provider / %d unknown, want 1/1",
			rep.ProviderMessages, rep.UnknownUsageMessages)
	}
	if rep.UnpricedMessages != 2 || rep.Priced {
		t.Fatalf("report = %d unpriced, priced=%v; costless historical rows must be declared, not hidden",
			rep.UnpricedMessages, rep.Priced)
	}
	if rep.EstimatedCost != 0 {
		t.Fatalf("estimated_cost = %v, want 0 with the count beside it saying why", rep.EstimatedCost)
	}
}

// TestAccountingIndexServesTheDailyQuery: the workspace-by-period query is
// the one budgets will run constantly, and it must not be a table scan.
func TestAccountingIndexServesTheDailyQuery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	var exists bool
	if err := e.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes
		                WHERE schemaname = 'chat' AND tablename = 'messages'
		                  AND indexname = 'messages_workspace_created_idx')`).Scan(&exists); err != nil {
		t.Fatalf("probe index: %v", err)
	}
	if !exists {
		t.Fatal("messages_workspace_created_idx is missing; a daily total is a sequential scan without it")
	}
}

/* ── conversations ───────────────────────────────────────────────────── */

// TestConversationLifecycle covers rename, ordering and soft delete, which
// together are the whole sidebar.
func TestConversationLifecycle(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// A second conversation on the same agent, so ordering has something to
	// order.
	rec := e.do("POST", "/chat/conversations", e.wsA, map[string]any{"agent_id": s.agentID})
	wantStatus(t, rec, http.StatusCreated)
	newer := decode[map[string]any](t, rec)["id"].(string)

	// Manual rename, a route that had never been exercised.
	rec = e.do("PATCH", "/chat/conversations/"+s.conversationID, e.wsA,
		map[string]any{"title": "Planejamento do v1"})
	wantStatus(t, rec, http.StatusOK)
	if got := decode[map[string]any](t, rec)["title"]; got != "Planejamento do v1" {
		t.Fatalf("title = %v", got)
	}

	// A blank title is refused rather than silently clearing the thread.
	rec = e.do("PATCH", "/chat/conversations/"+s.conversationID, e.wsA, map[string]any{"title": "  "})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	// Activity moves a thread to the top of the list.
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "atividade"}), http.StatusOK)

	rec = e.do("GET", "/chat/conversations", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	items := decode[struct {
		Items []map[string]any `json:"items"`
		Limit int              `json:"limit"`
	}](t, rec)
	if len(items.Items) != 2 {
		t.Fatalf("list returned %d conversations, want 2", len(items.Items))
	}
	if items.Items[0]["id"] != s.conversationID {
		t.Fatalf("most recently active thread is not first: %v", items.Items[0]["id"])
	}
	if items.Limit != 50 {
		t.Fatalf("default limit = %d, want 50", items.Limit)
	}

	// Soft delete removes it from the API and keeps the messages, so a
	// restore stays one UPDATE away.
	wantStatus(t, e.do("DELETE", "/chat/conversations/"+newer, e.wsA, nil), http.StatusNoContent)
	rec = e.do("GET", "/chat/conversations/"+newer, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	var remaining int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.messages WHERE conversation_id = $1`, s.conversationID).Scan(&remaining); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("messages in the surviving thread = %d, want 2", remaining)
	}
}

/* ── conversations: the agent filter ─────────────────────────────────── */

type conversationPage struct {
	Items  []map[string]any `json:"items"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
	Total  *int64           `json:"total"`
}

// conversations reads one page of the list, `query` being the raw query
// string (without the leading "?").
func (e *env) conversations(ws uuid.UUID, query string) conversationPage {
	e.t.Helper()
	path := "/chat/conversations"
	if query != "" {
		path += "?" + query
	}
	rec := e.do("GET", path, ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[conversationPage](e.t, rec)
}

// newAgent adds another agent onto an existing provider.
func (e *env) newAgent(ws uuid.UUID, providerID, name string) string {
	e.t.Helper()
	rec := e.do("POST", "/chat/agents", ws, map[string]any{
		"provider_id": providerID,
		"name":        name,
		"model":       "test-model",
	})
	wantStatus(e.t, rec, http.StatusCreated)
	return decode[map[string]any](e.t, rec)["id"].(string)
}

// newConversation opens a thread on an agent.
func (e *env) newConversation(ws uuid.UUID, agentID string) string {
	e.t.Helper()
	rec := e.do("POST", "/chat/conversations", ws, map[string]any{"agent_id": agentID})
	wantStatus(e.t, rec, http.StatusCreated)
	return decode[map[string]any](e.t, rec)["id"].(string)
}

// totalOf dereferences the envelope's total, failing when the field is
// absent — an omitted total is itself a regression, not a zero.
func totalOf(t *testing.T, page conversationPage) int64 {
	t.Helper()
	if page.Total == nil {
		t.Fatalf("list envelope carried no total")
	}
	return *page.Total
}

func idsOf(page conversationPage) []string {
	out := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, it["id"].(string))
	}
	return out
}

// TestConversationsFilteredByAgent is the capability the agent view needed and the
// module did not have: listing the threads that belong to one agent.
//
// It also pins what must NOT change — an unfiltered list still returns the
// whole workspace, in the same activity order.
func TestConversationsFilteredByAgent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA) // provider + agent + one conversation

	second := e.newAgent(e.wsA, s.providerID, "Segundo agente")
	onFirst := e.newConversation(e.wsA, s.agentID)
	onSecond := e.newConversation(e.wsA, second)

	// Unfiltered: everything in the workspace, and a total that agrees.
	all := e.conversations(e.wsA, "")
	if len(all.Items) != 3 {
		t.Fatalf("unfiltered list = %d conversations, want 3", len(all.Items))
	}
	if got := totalOf(t, all); got != 3 {
		t.Fatalf("unfiltered total = %d, want 3", got)
	}

	// Filtered: only the agent's own, and only those counted.
	first := e.conversations(e.wsA, "agent_id="+s.agentID)
	got := idsOf(first)
	if len(got) != 2 {
		t.Fatalf("agent filter returned %v, want the 2 threads of that agent", got)
	}
	for _, id := range got {
		if id == onSecond {
			t.Fatalf("the other agent's conversation leaked into the filtered list")
		}
	}
	if !contains(got, s.conversationID) || !contains(got, onFirst) {
		t.Fatalf("filtered list = %v, want %v and %v", got, s.conversationID, onFirst)
	}
	if got := totalOf(t, first); got != 2 {
		t.Fatalf("filtered total = %d, want 2 — the total must respect the filter", got)
	}

	// The other side of the same coin.
	other := e.conversations(e.wsA, "agent_id="+second)
	if ids := idsOf(other); len(ids) != 1 || ids[0] != onSecond {
		t.Fatalf("second agent's filter = %v, want [%s]", ids, onSecond)
	}

	// Activity ordering survives the filter: a turn on the oldest thread
	// moves it to the front of its agent's list, exactly as unfiltered.
	e.send(e.wsA, s.conversationID, "atividade")
	first = e.conversations(e.wsA, "agent_id="+s.agentID)
	if ids := idsOf(first); ids[0] != s.conversationID {
		t.Fatalf("filtered ordering ignores activity: %v", ids)
	}

	// An agent id that names nothing is an empty page, not an error: this
	// is a filter over a collection, and no match is a valid answer.
	none := e.conversations(e.wsA, "agent_id="+uuid.NewString())
	if got := totalOf(t, none); len(none.Items) != 0 || got != 0 {
		t.Fatalf("unknown agent filter = %d items / total %d, want 0 / 0", len(none.Items), got)
	}

	// A malformed id is refused rather than silently ignored — ignoring it
	// would answer a narrow question with the whole workspace.
	wantErrorCode(t, e.do("GET", "/chat/conversations?agent_id=not-a-uuid", e.wsA, nil),
		http.StatusBadRequest, "invalid")
}

// TestAgentFilterIsWorkspaceScoped is the mutation target of this batch.
//
// The id in ?agent_id= arrives from a URL. It must be able to NARROW what
// the workspace already owns and nothing else: pointing it at another
// workspace's agent has to return that agent's rows to nobody, and must not
// fall back to listing the caller's own threads either.
func TestAgentFilterIsWorkspaceScoped(t *testing.T) {
	e := newEnv(t)

	a := e.seed(e.wsA)
	b := e.seed(e.wsB)

	// wsB naming wsA's agent sees nothing — neither wsA's thread nor, by
	// way of a dropped predicate, its own.
	leak := e.conversations(e.wsB, "agent_id="+a.agentID)
	if len(leak.Items) != 0 {
		t.Fatalf("wsB filtering by wsA's agent saw %v", idsOf(leak))
	}
	if got := totalOf(t, leak); got != 0 {
		t.Fatalf("cross-workspace total = %d, want 0", got)
	}

	// And the mirror image.
	leak = e.conversations(e.wsA, "agent_id="+b.agentID)
	if len(leak.Items) != 0 || totalOf(t, leak) != 0 {
		t.Fatalf("wsA filtering by wsB's agent saw %v", idsOf(leak))
	}

	// Each workspace still sees exactly its own when it asks properly.
	mine := e.conversations(e.wsB, "agent_id="+b.agentID)
	if ids := idsOf(mine); len(ids) != 1 || ids[0] != b.conversationID {
		t.Fatalf("wsB's own filtered list = %v, want [%s]", ids, b.conversationID)
	}
}

// TestConversationPagingReportsTheTotal covers the other half of the silent
// cut: a page that stops has to say how much it stopped short of.
func TestConversationPagingReportsTheTotal(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	for i := 0; i < 4; i++ {
		e.newConversation(e.wsA, s.agentID)
	}
	const want = 5 // the seeded one plus four

	seen := map[string]bool{}
	for offset := 0; offset < want; offset += 2 {
		page := e.conversations(e.wsA, fmt.Sprintf("agent_id=%s&limit=2&offset=%d", s.agentID, offset))
		if got := totalOf(t, page); got != want {
			t.Fatalf("offset %d: total = %d, want %d on every page — a page-sized total says the list is complete when it is not", offset, got, want)
		}
		if page.Offset != offset || page.Limit != 2 {
			t.Fatalf("offset %d: envelope echoed limit=%d offset=%d", offset, page.Limit, page.Offset)
		}
		for _, id := range idsOf(page) {
			if seen[id] {
				t.Fatalf("offset %d repeated conversation %s", offset, id)
			}
			seen[id] = true
		}
	}
	if len(seen) != want {
		t.Fatalf("walking the pages saw %d conversations, want %d", len(seen), want)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

/* ── agents ──────────────────────────────────────────────────────────── */

// TestAgentDefaultsAndUpdate pins the domain defaults (they live in the
// domain so every entry point agrees) and the PATCH route, which had never
// been exercised either.
func TestAgentDefaultsAndUpdate(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("GET", "/chat/agents/"+s.agentID, e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	agent := decode[map[string]any](t, rec)
	// Compared with a tolerance: temperature is REAL in Postgres and
	// float32 in the domain, so the JSON round trip cannot be expected to
	// land on the exact float64 the constant widens to.
	if got := agent["temperature"].(float64); !within(got, float64(domain.DefaultTemperature), 1e-6) {
		t.Fatalf("temperature = %v, want the domain default %v", got, domain.DefaultTemperature)
	}
	if int(agent["max_tokens"].(float64)) != domain.DefaultMaxTokens {
		t.Fatalf("max_tokens = %v", agent["max_tokens"])
	}
	if int(agent["history_limit"].(float64)) != domain.DefaultHistoryLimit {
		t.Fatalf("history_limit = %v", agent["history_limit"])
	}
	if agent["accent"] != domain.DefaultAccent {
		t.Fatalf("accent = %v", agent["accent"])
	}

	// A blank model inherits the provider's default rather than being
	// stored empty, which is why the "provider default" branch in the UI is
	// unreachable.
	rec = e.do("POST", "/chat/agents", e.wsA, map[string]any{
		"provider_id": s.providerID, "name": "Sem modelo",
	})
	wantStatus(t, rec, http.StatusCreated)
	if got := decode[map[string]any](t, rec)["model"]; got != "test-model" {
		t.Fatalf("model = %v, want the provider default", got)
	}

	// PATCH is partial: an omitted field is untouched.
	rec = e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA, map[string]any{"name": "Renomeado"})
	wantStatus(t, rec, http.StatusOK)
	updated := decode[map[string]any](t, rec)
	if updated["name"] != "Renomeado" {
		t.Fatalf("name = %v", updated["name"])
	}
	if updated["system_prompt"] != "você é o agente de testes do João" {
		t.Fatalf("PATCH wiped the system prompt: %v", updated["system_prompt"])
	}

	// Out-of-range values are refused by the domain, not by the database.
	for _, body := range []map[string]any{
		{"temperature": 5},
		{"max_tokens": 0},
		{"history_limit": 0},
		{"history_limit": 500},
	} {
		rec = e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA, body)
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	}

	// Editing the agent changes future turns without rewriting history.
	rec = e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"system_prompt": "novo prompt"})
	wantStatus(t, rec, http.StatusOK)
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "depois da edição"}), http.StatusOK)
	if got := e.llm.lastRequest.Messages[0].Content; got != "novo prompt" {
		t.Fatalf("system prompt on the wire = %q", got)
	}
}

// TestAgentNameUniquePerWorkspace: the unique index is partial (active rows
// only), so the same name becomes available again after a delete, and is
// never shared across workspaces.
func TestAgentNameUniquePerWorkspace(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("GET", "/chat/agents/"+s.agentID, e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	name := decode[map[string]any](t, rec)["name"].(string)

	rec = e.do("POST", "/chat/agents", e.wsA, map[string]any{
		"provider_id": s.providerID, "name": strings.ToUpper(name), "model": "test-model",
	})
	wantErrorCode(t, rec, http.StatusConflict, "conflict")

	// Another workspace may use the same name.
	other := e.seed(e.wsB)
	rec = e.do("POST", "/chat/agents", e.wsB, map[string]any{
		"provider_id": other.providerID, "name": name, "model": "test-model",
	})
	wantStatus(t, rec, http.StatusCreated)
}

/* ── transport contract ──────────────────────────────────────────────── */

// TestWireContract locks the shapes every client depends on: the list
// envelope, the id validation, and the error body.
func TestWireContract(t *testing.T) {
	e := newEnv(t)

	rec := e.do("GET", "/chat/ping", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)

	rec = e.do("GET", "/chat/agents?limit=7&offset=3", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	env := decode[struct {
		Items  []map[string]any `json:"items"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}](t, rec)
	if env.Limit != 7 || env.Offset != 3 {
		t.Fatalf("envelope = limit %d offset %d", env.Limit, env.Offset)
	}
	if env.Items == nil {
		t.Fatal("items is null; the envelope must always carry an array")
	}

	// Above the ceiling falls back to the default page rather than
	// honouring an unbounded read.
	rec = e.do("GET", "/chat/agents?limit=9000", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decode[struct {
		Limit int `json:"limit"`
	}](t, rec).Limit; got != 50 {
		t.Fatalf("limit = %d for an out-of-range request, want the default 50", got)
	}

	rec = e.do("GET", "/chat/agents/not-a-uuid", e.wsA, nil)
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	rec = e.do("POST", "/chat/agents", e.wsA, "not an object")
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
}

/* ── helpers ─────────────────────────────────────────────────────────── */

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// within compares floats that came back through JSON, where exact equality
// is not a reasonable expectation.
func within(got, want, epsilon float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < epsilon
}

// nearly is within at the tolerance money arithmetic deserves: the costs
// here are sums of products of exact integers and exact rates.
func nearly(got, want float64) bool { return within(got, want, 1e-12) }
