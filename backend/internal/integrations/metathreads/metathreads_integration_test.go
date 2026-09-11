//go:build integration

// Meta Threads × Content Agent — the intelligence suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/integrations/metathreads/...
//
// The sentences this suite has to make convincing:
//
//	An agent granted the meta_threads.* capabilities can read the
//	operator's REAL published history and the metrics Meta reports about
//	it — and an agent without those grants cannot. Reading that history
//	changes nothing: not the posts, not Meta, and above all not the
//	internal C.O.R.S.I. Threads pipeline.
//
// What is REAL here: the meta_threads schema, domain, repository, service,
// HTTP client, management routes and tools; the whole C.O.R.S.I. Threads
// module beside it; the Agents tool registry, authorization store, schema
// validator, four-gate executor, turn loop and audit trail; and Postgres.
//
// What is faked: Meta itself, and the LLM. Meta is faked as an httptest
// SERVER speaking its documented wire format — the `data`/`paging`
// envelope, the {name, values:[{value}]} metric rows, the
// {"error":{...}} failures — so the real client's dialect translation is
// under test, not stubbed out. The LLM is faked because the point is to
// control which tool calls arrive.
package metathreads

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

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	chatrepo "github.com/corsi/backend/internal/chat/adapters/repo"
	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	mtapi "github.com/corsi/backend/internal/integrations/metathreads/adapters/api"
	mthttp "github.com/corsi/backend/internal/integrations/metathreads/adapters/httpapi"
	mtrepo "github.com/corsi/backend/internal/integrations/metathreads/adapters/repo"
	mtapp "github.com/corsi/backend/internal/integrations/metathreads/app"
	mtdomain "github.com/corsi/backend/internal/integrations/metathreads/domain"
	mttools "github.com/corsi/backend/internal/integrations/metathreads/tools"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
	threadsrepo "github.com/corsi/backend/internal/threads/adapters/repo"
	threadsapp "github.com/corsi/backend/internal/threads/app"
	threadstools "github.com/corsi/backend/internal/threads/tools"
)

var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// This suite needs THREE schemas — meta_threads for the credential, threads
// for the internal pipeline it must never write to, and chat for the turn
// loop — so it owns a database, for the reason the other cross-cutting
// suites give.
const privateDBName = "corsi_test_metathreads"

var suiteDSN string

func TestMain(m *testing.M) {
	admin := os.Getenv("TEST_POSTGRES_DSN")
	if admin == "" {
		os.Exit(m.Run())
	}
	d, cleanup, err := createPrivateDB(admin)
	if err != nil {
		panic("metathreads suite: " + err.Error())
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
		`DROP SCHEMA IF EXISTS meta_threads CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_metathreads`,
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
	migrateUp(t, d, "metathreads", "schema_migrations_metathreads")
	migrateUp(t, d, "threads", "schema_migrations_threads")
	migrateUp(t, d, "chat", "schema_migrations_chat")
}

/* ── the fake Meta ───────────────────────────────────────────────────── */

// fakeMeta is an httptest server speaking Meta's documented wire format.
//
// ── Why a server and not a fake ports.API ──────────────────────────────
// Because the thing most likely to be wrong in this integration is the
// DIALECT: the data/paging envelope, the metric rows shaped as
// {name, values:[{value}]}, the ISO timestamps, the error envelope, the
// two-call split for followers_count. A fake at the port would stub exactly
// that layer out and leave it untested until a real token existed.
type fakeMeta struct {
	srv *httptest.Server

	mu sync.Mutex
	// requests records every path+query the client sent, which is how the
	// "no token in the wrong place" and "the right fields were asked for"
	// assertions are made.
	requests []recordedRequest
	// posts is the account's published history, newest first.
	posts []map[string]any
	// insights is post id → metric name → value. A post absent from here
	// returns an empty data array, which is what Meta does for a repost
	// facade.
	insights map[string]map[string]int64
	// tokenResponseExtra lets a test add fields Meta does not send to the
	// token response — used by the scope ratchet to prove that no response
	// shape can influence what this integration believes a credential may
	// do.
	tokenResponseExtra map[string]any
	// failNext makes the next matching call fail with Meta's error shape.
	failStatus int
	failBody   string
	failPath   string
}

type recordedRequest struct {
	Path  string
	Query url.Values
}

func newFakeMeta(t *testing.T) *fakeMeta {
	f := &fakeMeta{insights: map[string]map[string]int64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMeta) URL() string { return f.srv.URL }

func (f *fakeMeta) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		for k, v := range r.PostForm {
			q[k] = v
		}
	}
	f.requests = append(f.requests, recordedRequest{Path: r.URL.Path, Query: q})
}

func (f *fakeMeta) seen() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeMeta) failOn(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPath, f.failStatus, f.failBody = path, status, body
}

func (f *fakeMeta) serve(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	w.Header().Set("Content-Type", "application/json")

	f.mu.Lock()
	failPath, failStatus, failBody := f.failPath, f.failStatus, f.failBody
	f.mu.Unlock()
	if failPath != "" && strings.Contains(r.URL.Path, failPath) {
		w.WriteHeader(failStatus)
		_, _ = io.WriteString(w, failBody)
		return
	}

	switch {
	case r.URL.Path == "/oauth/access_token":
		// EXACTLY what Meta documents, and nothing else:
		//
		//     { "access_token": "THQVJ...", "user_id": 17841405793187218 }
		//
		// An earlier version of this fake also returned a `scope` field.
		// Meta has never sent one, so the suite was green over fiction
		// while every real connection stored no scopes at all and the local
		// gate refused insights and search. The fake is now the contract.
		//
		// `user_id` is a NUMBER here, as Meta sends it — not a string. The
		// client normalises it, and typing it as a string in the fake would
		// hide that too.
		body := map[string]any{
			"access_token": "SHORT_LIVED_TOKEN_aaaa",
			"user_id":      int64(17841400000000000),
		}
		f.mu.Lock()
		for k, v := range f.tokenResponseExtra {
			body[k] = v
		}
		f.mu.Unlock()
		writeJSON(w, body)
	case r.URL.Path == "/access_token":
		writeJSON(w, map[string]any{
			"access_token": "LONG_LIVED_TOKEN_zzzz", "token_type": "bearer",
			"expires_in": 5184000,
		})
	case r.URL.Path == "/refresh_access_token":
		writeJSON(w, map[string]any{
			"access_token": "REFRESHED_TOKEN_wwww", "token_type": "bearer",
			"expires_in": 5184000,
		})
	case r.URL.Path == "/v1.0/me":
		writeJSON(w, map[string]any{
			"id": "17841400000000000", "username": "joaocorsi",
			"name": "João Corsi", "threads_biography": "building in public",
			"is_verified": false,
		})
	case r.URL.Path == "/v1.0/me/threads":
		f.writePosts(w, r)
	case r.URL.Path == "/v1.0/keyword_search":
		f.writeSearch(w, r)
	case strings.HasSuffix(r.URL.Path, "/insights"):
		f.writeInsights(w, r)
	case strings.HasSuffix(r.URL.Path, "/threads_insights"):
		f.writeAccountInsights(w, r)
	default:
		// A single post read: /v1.0/{id}
		id := strings.TrimPrefix(r.URL.Path, "/v1.0/")
		for _, p := range f.posts {
			if p["id"] == id {
				writeJSON(w, p)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"Unsupported get request. Object with ID does not exist","code":100}}`)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// writePosts serves /me/threads with real cursor pagination, so the
// "read a page, then page" behaviour is exercised rather than assumed.
func (f *fakeMeta) writePosts(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		_, _ = fmt.Sscanf(raw, "%d", &limit)
	}
	start := 0
	if after := r.URL.Query().Get("after"); after != "" {
		_, _ = fmt.Sscanf(after, "cursor-%d", &start)
	}
	end := start + limit
	if end > len(f.posts) {
		end = len(f.posts)
	}
	page := map[string]any{"data": f.posts[start:end]}
	if end < len(f.posts) {
		page["paging"] = map[string]any{
			"cursors": map[string]any{"after": fmt.Sprintf("cursor-%d", end)},
			"next":    "https://graph.threads.net/next",
		}
	} else {
		// Meta returns an `after` cursor on the last page too, WITHOUT a
		// next link. A client that treated the cursor alone as "there is
		// more" would page forever — which is exactly what this reproduces.
		page["paging"] = map[string]any{
			"cursors": map[string]any{"after": fmt.Sprintf("cursor-%d", end)},
		}
	}
	writeJSON(w, page)
}

func (f *fakeMeta) writeSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	author := r.URL.Query().Get("author_username")
	matched := make([]map[string]any, 0)
	for _, p := range f.posts {
		text, _ := p["text"].(string)
		if q != "" && !strings.Contains(strings.ToLower(text), q) {
			continue
		}
		if author != "" && p["username"] != author {
			continue
		}
		matched = append(matched, p)
	}
	writeJSON(w, map[string]any{"data": matched})
}

func (f *fakeMeta) writeInsights(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/"), "/insights")
	metrics := f.insights[id]
	data := make([]map[string]any, 0, len(metrics))
	for _, name := range strings.Split(r.URL.Query().Get("metric"), ",") {
		v, ok := metrics[name]
		if !ok {
			// Absent, exactly as Meta omits a metric it did not measure.
			continue
		}
		data = append(data, map[string]any{
			"name": name, "period": "lifetime",
			"values": []map[string]any{{"value": v}},
		})
	}
	writeJSON(w, map[string]any{"data": data})
}

func (f *fakeMeta) writeAccountInsights(w http.ResponseWriter, r *http.Request) {
	requested := strings.Split(r.URL.Query().Get("metric"), ",")
	// followers_count must arrive in its own call with no window, because
	// Meta refuses since/until for it. Asserted rather than tolerated.
	if len(requested) == 1 && requested[0] == "followers_count" {
		if r.URL.Query().Get("since") != "" || r.URL.Query().Get("until") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"followers_count does not support since/until","code":100}}`)
			return
		}
		writeJSON(w, map[string]any{"data": []map[string]any{{
			"name": "followers_count", "period": "day",
			"total_value": map[string]any{"value": 1240},
		}}})
		return
	}
	data := make([]map[string]any, 0)
	totals := map[string]int64{"views": 48000, "likes": 3100, "replies": 420, "reposts": 180, "quotes": 35, "clicks": 260}
	for _, name := range requested {
		if v, ok := totals[name]; ok {
			data = append(data, map[string]any{
				"name": name, "period": "day",
				"total_value": map[string]any{"value": v},
			})
		}
	}
	writeJSON(w, map[string]any{"data": data})
}

// seedHistory gives the fake account a believable published archive.
func (f *fakeMeta) seedHistory() {
	mk := func(id, text, day string, views, likes, replies int64) map[string]any {
		f.insights[id] = map[string]int64{"views": views, "likes": likes, "replies": replies, "reposts": 0, "quotes": 0}
		return map[string]any{
			"id": id, "text": text, "media_type": "TEXT_POST",
			"permalink": "https://www.threads.net/@joaocorsi/post/" + id,
			"timestamp": day + "T12:00:00+0000", "username": "joaocorsi",
			"is_quote_post": false, "is_reply": false, "has_replies": replies > 0,
		}
	}
	f.posts = []map[string]any{
		mk("p_005", "AI agents só valem a pena quando você pode revogar o acesso deles.", "2026-08-20", 12000, 830, 41),
		mk("p_004", "Microservices cedo demais são a forma mais cara de adiar uma decisão.", "2026-08-14", 4100, 210, 12),
		mk("p_003", "Todo agente de IA que escreve em produção precisa de um audit trail.", "2026-08-07", 9800, 640, 33),
		mk("p_002", "Ship it e depois conserta não é estratégia, é dívida com juros.", "2026-07-30", 2200, 95, 4),
		mk("p_001", "Comecei a construir meu próprio sistema operacional pessoal.", "2026-07-21", 1500, 60, 2),
	}
	// One post Meta reports NO insights for, which is the repost-facade case
	// and the reason every metric is a pointer.
	f.posts = append(f.posts, map[string]any{
		"id": "p_000", "text": "Repost sem métricas.", "media_type": "REPOST_FACADE",
		"timestamp": "2026-07-01T12:00:00+0000", "username": "joaocorsi",
		"is_quote_post": false, "is_reply": false, "has_replies": false,
	})
}

/* ── the environment ─────────────────────────────────────────────────── */

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	r    chi.Router

	meta *fakeMeta
	// mt is the Meta Threads service the routes and tools share.
	mt *mtapp.Service
	// threads is the INTERNAL pipeline, present so this suite can prove that
	// reading Meta never writes to it.
	threads *threadsapp.Service
	chatSvc *chatapp.Service
	llm     *fakeLLM
	reg     *chattools.Registry

	wsA uuid.UUID
	wsB uuid.UUID
}

const (
	testAppID       = "1234567890"
	testAppSecret   = "meta-app-secret-not-real"
	testRedirectURI = "https://corsi.dev/integrations/meta-threads/callback"
)

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
	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}

	meta := newFakeMeta(t)
	meta.seedHistory()

	// The REAL client, pointed at the fake server. Everything below the
	// socket is production code.
	mtSvc := mtapp.NewService(mtrepo.New(pool).Connections, mtapi.New(log), sealer,
		mtapp.AppConfig{
			AppID: testAppID, AppSecret: testAppSecret,
			RedirectURI: testRedirectURI, BaseURL: meta.URL(),
		}, log)

	threadsSvc := threadsapp.NewService(threadsrepo.New(pool).Threads, log)

	// Both providers in one catalogue, exactly as cmd/corsi builds it.
	registry := chattools.MustNew(chattools.Options{
		Extra: append(threadstools.New(threadsSvc), mttools.New(mtSvc)...),
	})
	llm := newFakeLLM()
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		llm, sealer, registry, nil, log)

	root := chi.NewRouter()
	// Wired exactly as metathreads.Register wires it: the platform
	// callbacks OUTSIDE the workspace middleware, everything else behind
	// it. A harness that put them on the same side would test a router
	// production does not have — and would hide the 401 that mounting the
	// callbacks behind the guard causes.
	mtHandler := mthttp.NewHandler(mtSvc, log)
	root.Route("/integrations/meta-threads", func(r chi.Router) {
		mtHandler.MountPublic(r)
		r.Group(func(r chi.Router) {
			r.Use(requireTestWorkspace)
			mtHandler.Mount(r)
		})
	})

	return &env{
		t: t, pool: pool, r: root, meta: meta, mt: mtSvc, threads: threadsSvc,
		chatSvc: chatSvc, llm: llm, reg: registry,
		wsA: uuid.New(), wsB: uuid.New(),
	}
}

func ctxFor(ws uuid.UUID) context.Context {
	return workspace.WithWorkspaceID(context.Background(), ws)
}

func (e *env) do(method, path string, ws uuid.UUID, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal: %v", err)
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

// connect runs the real OAuth redemption against the fake Meta.
func (e *env) connect(ws uuid.UUID) *mtdomain.Connection {
	e.t.Helper()
	conn, err := e.mt.Connect(ctxFor(ws), mtapp.ConnectInput{
		WorkspaceID: ws, Code: "AUTH_CODE_FROM_META", RedirectURI: testRedirectURI,
	})
	if err != nil {
		e.t.Fatalf("connect: %v", err)
	}
	return conn
}

func (e *env) newAgent(ws uuid.UUID, name string) (agentID, conversationID uuid.UUID) {
	e.t.Helper()
	ctx := ctxFor(ws)
	provider, err := e.chatSvc.CreateProvider(ctx, chatapp.CreateProviderInput{
		WorkspaceID: ws, Name: "fixture-" + name, BaseURL: "https://gateway.invalid",
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

func (e *env) authorize(ws, agentID uuid.UUID, names ...chatdomain.ToolName) {
	e.t.Helper()
	for _, n := range names {
		if err := e.chatSvc.AuthorizeTool(ctxFor(ws), ws, agentID, n); err != nil {
			e.t.Fatalf("authorize %s: %v", n, err)
		}
	}
}

// allMetaTools is the read set a content agent is given.
var allMetaTools = []chatdomain.ToolName{
	mttools.ProfileGetTool, mttools.ProfileInsightsTool, mttools.PostListTool,
	mttools.PostGetTool, mttools.PostInsightsTool, mttools.PostSearchTool,
}

var allThreadTools = []chatdomain.ToolName{
	threadstools.ThreadListTool, threadstools.ThreadGetTool, threadstools.ThreadCreateTool,
	threadstools.ThreadUpdateTool, threadstools.ThreadDeleteTool,
}

/* ── direct tool execution ───────────────────────────────────────────── */

func (e *env) execute(t *testing.T, ws uuid.UUID, name chatdomain.ToolName, args map[string]any) map[string]any {
	t.Helper()
	out, err := e.executeErr(ws, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (e *env) executeErr(ws uuid.UUID, name chatdomain.ToolName, args map[string]any) (map[string]any, error) {
	tool, ok := e.reg.Lookup(name)
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
	f.streamCalls, f.requests = 0, nil
	f.rounds = [][]chatports.StreamEvent{{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}}
}

// scriptCalls scripts a turn that asks for several tools in sequence, one
// per round, then answers in prose. This is what a real intelligence turn
// looks like: list, then insights, then reason.
func (f *fakeLLM) scriptCalls(calls ...chatdomain.ToolCall) {
	f.streamCalls, f.requests = 0, nil
	f.rounds = nil
	for i, c := range calls {
		call := c
		call.ID = fmt.Sprintf("call_%d", i+1)
		f.rounds = append(f.rounds, []chatports.StreamEvent{{
			FinishReason: "tool_calls",
			Usage:        &chatports.Usage{PromptTokens: 20, CompletionTokens: 10},
			ToolCalls:    []chatdomain.ToolCall{call},
		}})
	}
	f.rounds = append(f.rounds, []chatports.StreamEvent{
		{Delta: "pronto"},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 30, CompletionTokens: 8}},
	})
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

// requireTestWorkspace stands in for the production workspace middleware.
//
// It is applied to the GUARDED group only. Meta's platform callbacks carry
// no such header and must reach their handlers without one — that split is
// the property TestTheCallbacksWorkWithNoWorkspaceHeader exists to hold.
func requireTestWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(req.Header.Get("X-Test-Workspace-Id"))
		if err != nil {
			http.Error(w, "test header missing or invalid", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), id)))
	})
}

// newConversation opens another thread for an existing agent.
func (e *env) newConversation(ws, agentID uuid.UUID) uuid.UUID {
	e.t.Helper()
	conv, err := e.chatSvc.CreateConversation(ctxFor(ws), chatapp.CreateConversationInput{
		WorkspaceID: ws, AgentID: agentID, Title: "t2",
	})
	if err != nil {
		e.t.Fatalf("create conversation: %v", err)
	}
	return conv.ID
}
