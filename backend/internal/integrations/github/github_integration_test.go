//go:build integration

// GitHub Integration v1 — the end-to-end suite.
//
// ── What is real here ──────────────────────────────────────────────────
// Postgres, both migration timelines, the repositories, the application
// service, the sealer, the HTTP management API, the tool implementations,
// the Agents tool registry, the authorization store, the schema validator,
// the tool execution loop, the audit trail, and — the part that matters
// most — the REAL GitHub HTTP client, pointed at a fake server. The URL it
// builds, the headers it sends, the statuses it maps and the JSON it
// decodes are all production code exercised over a real socket.
//
// What is not real: GitHub itself, and the LLM gateway. Both are declared
// unverified boundaries. The LLM is the same kind of scripted fake the chat
// suite uses; GitHub is an httptest server serving fixtures written from
// the published API reference rather than captured from a live account, so
// nothing private is in this file.
//
// ── Why this suite lives in the integration package ────────────────────
// Because the thing under test is the composition: an agent reaching a
// repository through a tool. That crosses Agents and GitHub, and the only
// place allowed to know both is the composition root. This file plays that
// role — it wires what cmd/corsi wires. Putting it in the chat package
// instead would mean the Agents suite naming GitHub, which is the one thing
// the whole arrangement exists to prevent.
//
//	Run with:  TEST_POSTGRES_DSN=postgres://corsi:corsi@localhost:5432/postgres?sslmode=disable \
//	           go test -tags=integration ./internal/integrations/github/
package github

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

	chathttpapi "github.com/corsi/backend/internal/chat/adapters/httpapi"
	chatreferences "github.com/corsi/backend/internal/chat/adapters/references"
	chatrepo "github.com/corsi/backend/internal/chat/adapters/repo"
	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	ghrepo "github.com/corsi/backend/internal/integrations/github/adapters/repo"
	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── fixtures that are not credentials ───────────────────────────────── */

// testToken is a literal in a test file and could never authenticate
// against anything. Nothing in this suite reads a real credential from the
// environment, and the live-boundary suite that would is in a separate file
// behind its own tag.
const testToken = "ghp_fixture_not_a_real_token_000000000000"

// testSecretsKey is exactly 32 bytes before encoding, which AES-256 needs.
var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

/* ── the throwaway database ──────────────────────────────────────────── */

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping github integration test")
	}
	return v
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// freshDatabase creates an empty database and returns its DSN.
//
// A dedicated database rather than dropping schemas in the shared one: this
// suite applies BOTH the chat and the github timelines, and `go test ./...`
// runs the chat package concurrently. Sharing would mean one suite's
// `migrate down` wiping the other's tables mid-run. The entrypoint test
// makes the same choice for the same reason.
func freshDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin := adminDSN(t)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	name := fmt.Sprintf("corsi_github_%d", time.Now().UnixNano())
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

	dsn, err := withDatabase(admin, name)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	return dsn
}

// migrateUp applies one module's timeline against its own version table.
//
// The `-table` equivalent is mandatory here for exactly the reason the
// Makefile and the entrypoint say it is: omitting it makes golang-migrate
// read the finance version table and conclude there is nothing to apply,
// leaving every test below running against a database with no schema.
func migrateUp(t *testing.T, dsn, dir, table string) {
	t.Helper()
	u, err := url.Parse(strings.Replace(dsn, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	if table != "" {
		q.Set("x-migrations-table", table)
	}
	u.RawQuery = q.Encode()

	m, err := migrate.New("file://../../../migrations/"+dir, u.String())
	if err != nil {
		t.Fatalf("migrate.New(%s): %v", dir, err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up %s: %v", dir, err)
	}
}

/* ── the fake GitHub ─────────────────────────────────────────────────── */

// fakeGitHub is an HTTP server that speaks enough of the GitHub REST API
// for this integration, and records what it was asked.
//
// It is an HTTP server rather than a stubbed ports.API on purpose: the
// client is the thing most likely to be wrong, and a test that replaced it
// would prove only that our own abstraction calls our own abstraction.
type fakeGitHub struct {
	srv *httptest.Server

	mu sync.Mutex
	// paths is every path requested, in order. The DENY assertions read it
	// to prove a refused call never reached the network at all.
	paths []string
	// authHeaders is every Authorization header received, so a test can
	// assert the credential travelled and that it travelled nowhere else.
	authHeaders []string

	// Failure switches. Set before a request; every handler consults them.
	status  int    // non-zero overrides the response status
	body    string // body to serve with `status`
	headers map[string]string

	// Fixtures.
	user   map[string]any
	repos  []map[string]any
	commit map[string]any
	// commitsFor maps "owner/name" to a listing, so a test can give two
	// repositories different histories and prove which one was read.
	commitsFor map[string][]map[string]any
	search     map[string]any
	file       map[string]any
	pulls      []map[string]any
	pull       map[string]any
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		headers: map[string]string{},
		user: map[string]any{
			"login": "joaocorsi", "id": 4242, "type": "User",
			"name": "João Corsi", "avatar_url": "https://avatars.invalid/u/4242",
		},
		repos: []map[string]any{
			repoFixture(1, "joaocorsi", "User", "c.o.r.s.i", false),
			repoFixture(2, "joaocorsi", "User", "dotfiles", false),
			repoFixture(3, "acme", "Organization", "internal-api", true),
		},
		commitsFor: map[string][]map[string]any{},
		search:     map[string]any{"total_count": 0, "incomplete_results": false, "items": []any{}},
		pulls:      []map[string]any{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func repoFixture(id int, owner, ownerType, name string, private bool) map[string]any {
	return map[string]any{
		"id":    id,
		"owner": map[string]any{"login": owner, "type": ownerType},
		"name":  name, "full_name": owner + "/" + name,
		"private": private, "default_branch": "main",
		"html_url":    "https://github.com/" + owner + "/" + name,
		"description": name + " description",
		"pushed_at":   time.Now().UTC().Format(time.RFC3339),
		// The detail `github.repository.list` reports so a caller can decide
		// whether it already knows enough. Present in the fixture because
		// GitHub sends them on this endpoint; a fixture without them would
		// let the tool silently stop reading them.
		"language": "Go",
		"size":     2048,
		"archived": false,
		"fork":     false,
	}
}

func commitFixture(sha, message string) map[string]any {
	return map[string]any{
		"sha": sha,
		"commit": map[string]any{
			"message": message,
			"author": map[string]any{
				"name": "João Corsi", "email": "joao@invalid",
				"date": time.Now().UTC().Format(time.RFC3339),
			},
		},
		"author":   map[string]any{"login": "joaocorsi"},
		"html_url": "https://github.com/x/y/commit/" + sha,
	}
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.authHeaders = append(f.authHeaders, r.Header.Get("Authorization"))
	status, body, headers := f.status, f.body, f.headers
	f.mu.Unlock()

	for k, v := range headers {
		w.Header().Set(k, v)
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	enc := json.NewEncoder(w)

	switch {
	case path == "/user":
		_ = enc.Encode(f.user)

	case path == "/user/repos":
		// Honour per_page so the client's pagination stop condition (a page
		// shorter than per_page) behaves the way it does against GitHub.
		_ = enc.Encode(f.repos)

	case path == "/search/code":
		_ = enc.Encode(f.search)

	case strings.HasPrefix(path, "/repos/"):
		rest := strings.TrimPrefix(path, "/repos/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		full := parts[0] + "/" + parts[1]
		tail := parts[2]
		switch {
		case tail == "commits":
			if list, ok := f.commitsFor[full]; ok {
				_ = enc.Encode(list)
				return
			}
			_ = enc.Encode([]map[string]any{commitFixture("aaa1111", "commit in "+full)})
		case strings.HasPrefix(tail, "commits/"):
			if f.commit != nil {
				_ = enc.Encode(f.commit)
				return
			}
			_ = enc.Encode(commitFixture(strings.TrimPrefix(tail, "commits/"), "a commit"))
		case strings.HasPrefix(tail, "contents/"):
			if f.file != nil {
				_ = enc.Encode(f.file)
				return
			}
			content := "package main\n\n// budget lives here\n"
			_ = enc.Encode(map[string]any{
				"type": "file", "path": strings.TrimPrefix(tail, "contents/"),
				"sha": "filesha", "size": len(content), "encoding": "base64",
				"content": base64.StdEncoding.EncodeToString([]byte(content)),
			})
		case tail == "pulls":
			_ = enc.Encode(f.pulls)
		case strings.HasPrefix(tail, "pulls/"):
			if f.pull != nil {
				_ = enc.Encode(f.pull)
				return
			}
			_ = enc.Encode(map[string]any{
				"number": 12, "title": "Add budget gate", "state": "open",
				"user":      map[string]any{"login": "joaocorsi"},
				"head":      map[string]any{"ref": "feat/budget"},
				"base":      map[string]any{"ref": "main"},
				"additions": 120, "deletions": 4, "changed_files": 3, "commits": 2,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// fail makes every subsequent request answer with this status and body.
func (f *fakeGitHub) fail(status int, body string, headers map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
	f.headers = headers
	if f.headers == nil {
		f.headers = map[string]string{}
	}
}

func (f *fakeGitHub) recover() { f.fail(0, "", nil) }

func (f *fakeGitHub) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *fakeGitHub) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = nil
	f.authHeaders = nil
}

// touched reports whether any request path contained `needle`. The DENY
// assertions are all of the form "this never reached the network".
func (f *fakeGitHub) touched(needle string) bool {
	for _, p := range f.requested() {
		if strings.Contains(p, needle) {
			return true
		}
	}
	return false
}

/* ── the fake LLM ────────────────────────────────────────────────────── */

type fakeLLM struct {
	rounds   [][]chatports.StreamEvent
	calls    int
	requests []chatports.CompletionRequest
}

func (f *fakeLLM) Stream(_ context.Context, req chatports.CompletionRequest) (chatports.Stream, error) {
	f.calls++
	f.requests = append(f.requests, req)
	script := []chatports.StreamEvent{
		{Delta: "ok"},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}
	if len(f.rounds) > 0 {
		if f.calls <= len(f.rounds) {
			script = f.rounds[f.calls-1]
		} else {
			script = f.rounds[len(f.rounds)-1]
		}
	}
	return &fakeStream{script: script}, nil
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

// askTool scripts a provider call that requests one tool.
func askTool(id, name, args string) []chatports.StreamEvent {
	return []chatports.StreamEvent{
		{FinishReason: "tool_calls",
			ToolCalls: []chatdomain.ToolCall{{ID: id, Name: chatdomain.ToolName(name), Arguments: args}},
			Usage:     &chatports.Usage{PromptTokens: 30, CompletionTokens: 8}},
	}
}

func answer(text string) []chatports.StreamEvent {
	return []chatports.StreamEvent{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 40, CompletionTokens: 12}},
	}
}

/* ── the environment ─────────────────────────────────────────────────── */

type env struct {
	t   *testing.T
	r   chi.Router
	gh  *fakeGitHub
	llm *fakeLLM

	pool *pgxpool.Pool
	// Two workspaces, always. Every isolation assertion reads wsA's data
	// back as wsB.
	wsA uuid.UUID
	wsB uuid.UUID
}

// newEnv wires exactly what cmd/corsi wires, with two substitutions: the
// LLM gateway and GitHub's own server.
func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := freshDatabase(t)
	migrateUp(t, dsn, "chat", "schema_migrations_chat")
	migrateUp(t, dsn, "github", "schema_migrations_github")

	ctx := context.Background()
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: dsn, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gh := newFakeGitHub(t)

	root := chi.NewRouter()
	testWorkspace := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id, err := uuid.Parse(req.Header.Get("X-Test-Workspace-Id"))
			if err != nil {
				http.Error(w, "test header missing or invalid", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), id)))
		})
	}

	// The integration, with its REAL HTTP client. It reaches the fake
	// through the api_base_url stored on the connection, which is the same
	// field a GitHub Enterprise deployment would set — so nothing about the
	// production path is bypassed to make this testable.
	ghMod := New(Deps{
		Pool: pool, Logger: log, Sealer: sealer,
		WorkspaceMiddleware: testWorkspace,
	})
	ghMod.Register(root)

	// Agents, wired the way main.go wires it: the integration's tools handed
	// over as values satisfying a port Agents declared.
	llm := &fakeLLM{}
	registry := chattools.MustNew(chattools.Options{Extra: ghMod.Tools()})
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		llm, sealer, registry,
		chatreferences.MustNew(), log)
	chatHandler := chathttpapi.NewHandler(chatSvc, log)
	root.Route("/chat", func(r chi.Router) {
		r.Use(testWorkspace)
		chatHandler.Mount(r)
	})

	return &env{t: t, r: root, gh: gh, llm: llm, pool: pool, wsA: uuid.New(), wsB: uuid.New()}
}

func (e *env) do(method, path string, ws uuid.UUID, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var br io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		br = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, br)
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
		t.Fatal("the error message is empty; the contract requires an actionable one")
	}
}

/* ── fixtures ────────────────────────────────────────────────────────── */

func (e *env) connect(ws uuid.UUID) map[string]any {
	e.t.Helper()
	rec := e.do("POST", "/integrations/github/connection", ws, map[string]any{
		"token": testToken, "api_base_url": e.gh.srv.URL,
	})
	wantStatus(e.t, rec, http.StatusCreated)
	return decode[map[string]any](e.t, rec)
}

func (e *env) authorizeRepos(ws uuid.UUID, names ...string) statusBody {
	e.t.Helper()
	rec := e.do("PUT", "/integrations/github/repositories", ws,
		map[string]any{"repositories": names})
	wantStatus(e.t, rec, http.StatusOK)
	return decode[statusBody](e.t, rec)
}

type statusBody struct {
	Connected  bool `json:"connected"`
	Connection *struct {
		AccountLogin string `json:"account_login"`
		TokenHint    string `json:"token_hint"`
		AuthKind     string `json:"auth_kind"`
	} `json:"connection"`
	Repositories []struct {
		FullName  string `json:"full_name"`
		Owner     string `json:"owner"`
		Name      string `json:"name"`
		Private   bool   `json:"private"`
		OwnerType string `json:"owner_type"`
	} `json:"repositories"`
	Organizations []string `json:"organizations"`
}

func (e *env) status(ws uuid.UUID) statusBody {
	e.t.Helper()
	rec := e.do("GET", "/integrations/github/connection", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[statusBody](e.t, rec)
}

type seeded struct {
	agentID        string
	conversationID string
}

func (e *env) seed(ws uuid.UUID) seeded {
	e.t.Helper()
	rec := e.do("POST", "/chat/providers", ws, map[string]any{
		"name": "LiteLLM " + uuid.NewString()[:8], "base_url": "https://gateway.invalid/v1",
		"api_key": "sk-fixture-not-a-real-key", "default_model": "test-model",
	})
	wantStatus(e.t, rec, http.StatusCreated)
	provider := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/agents", ws, map[string]any{
		"provider_id": provider["id"], "name": "Dev " + uuid.NewString()[:8],
		"description": "agente de teste", "system_prompt": "você lê repositórios",
		"model": "test-model",
	})
	wantStatus(e.t, rec, http.StatusCreated)
	agent := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/conversations", ws, map[string]any{"agent_id": agent["id"]})
	wantStatus(e.t, rec, http.StatusCreated)
	conv := decode[map[string]any](e.t, rec)

	return seeded{agentID: agent["id"].(string), conversationID: conv["id"].(string)}
}

type apiToolsReport struct {
	Items []struct {
		Name       string `json:"name"`
		Title      string `json:"title"`
		Effect     string `json:"effect"`
		Internal   bool   `json:"internal"`
		Authorized bool   `json:"authorized"`
	} `json:"items"`
	AuthorizedCount int `json:"authorized_count"`
}

func (e *env) agentTools(ws uuid.UUID, agentID string) apiToolsReport {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID+"/tools", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[apiToolsReport](e.t, rec)
}

func (e *env) authorizeTool(ws uuid.UUID, agentID, name string) {
	e.t.Helper()
	rec := e.do("POST", "/chat/agents/"+agentID+"/tools", ws, map[string]any{"tool_name": name})
	wantStatus(e.t, rec, http.StatusOK)
}

func (e *env) revokeTool(ws uuid.UUID, agentID, name string) {
	e.t.Helper()
	rec := e.do("DELETE", "/chat/agents/"+agentID+"/tools/"+name, ws, nil)
	wantStatus(e.t, rec, http.StatusNoContent)
}

// send drives one turn. `refs` is the `@` selection; nil is a turn without
// one, which must keep behaving exactly as it did before `@` existed.
func (e *env) send(ws uuid.UUID, conversationID, content string, refs []map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	body := map[string]any{"content": content}
	if refs != nil {
		body["references"] = refs
	}
	return e.do("POST", "/chat/conversations/"+conversationID+"/messages", ws, body)
}

func toolRef(name string) []map[string]any {
	return []map[string]any{{"kind": "tool", "id": name}}
}

type apiToolCall struct {
	Round        int     `json:"round"`
	ToolName     string  `json:"tool_name"`
	Arguments    *string `json:"arguments"`
	Result       *string `json:"result"`
	Status       string  `json:"status"`
	ErrorCode    string  `json:"error_code"`
	ErrorMessage string  `json:"error_message"`
}

func (e *env) toolCalls(ws uuid.UUID, conversationID string) []apiToolCall {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/tool-calls", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		Items []apiToolCall `json:"items"`
	}](e.t, rec).Items
}

func (e *env) lastAssistantText(ws uuid.UUID, conversationID string) string {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/messages", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	page := decode[struct {
		Items []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"items"`
	}](e.t, rec)
	last := ""
	for _, m := range page.Items {
		if m.Role == "assistant" {
			last = m.Content
		}
	}
	return last
}

/* ══ connection ══════════════════════════════════════════════════════ */

// The credential is verified before it is stored, so a token that never
// worked cannot become a connected-looking card that fails three layers
// later, mid-conversation.
func TestConnectingVerifiesTheTokenAndStoresTheAccountGitHubReported(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	if conn["account_login"] != "joaocorsi" {
		t.Fatalf("account_login = %v, want the login GET /user reported", conn["account_login"])
	}
	if conn["auth_kind"] != string(domain.AuthPAT) {
		t.Fatalf("auth_kind = %v", conn["auth_kind"])
	}
	if !e.gh.touched("/user") {
		t.Fatal("the token was stored without ever being verified")
	}
}

// The rule with no exception. The token exists in the request that sets it
// and in the client that spends it, and nowhere else a person can read.
func TestTheCredentialNeverLeavesTheBackendInPlaintext(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/integrations/github/connection", e.wsA, map[string]any{
		"token": testToken, "api_base_url": e.gh.srv.URL,
	})
	wantStatus(t, rec, http.StatusCreated)
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatal("the connect response contained the token")
	}

	// Every read surface, not just the one that stored it.
	for _, path := range []string{
		"/integrations/github/connection",
		"/integrations/github/repositories",
		"/integrations/github/activity",
	} {
		r := e.do("GET", path, e.wsA, nil)
		if strings.Contains(r.Body.String(), testToken) {
			t.Fatalf("%s returned the token", path)
		}
	}

	// And what is stored is ciphertext, checked against the column rather
	// than against the API that reads it. An API that happened to omit the
	// field would pass the assertions above while the database held the
	// token in the clear.
	var cipher []byte
	var hint string
	err := e.pool.QueryRow(context.Background(),
		`SELECT token_cipher, token_hint FROM github.connections WHERE workspace_id = $1`,
		e.wsA).Scan(&cipher, &hint)
	if err != nil {
		t.Fatalf("read the stored connection: %v", err)
	}
	if bytes.Contains(cipher, []byte(testToken)) {
		t.Fatal("the stored blob contains the plaintext token")
	}
	if len(cipher) == 0 {
		t.Fatal("no ciphertext was stored")
	}
	if strings.Contains(hint, testToken) || len(hint) > 12 {
		t.Fatalf("token_hint = %q; it must be a short remnant", hint)
	}

	// The credential DID travel to GitHub — otherwise this test would pass
	// on an integration that simply never authenticates.
	if e.gh.authHeaders[0] != "Bearer "+testToken {
		t.Fatal("the credential never reached GitHub; the assertions above prove nothing")
	}
}

// The SEALED blob must not be published either.
//
// ── Why this is a separate test from the one above ─────────────────────
// A mutation test found the gap. Changing `TokenCipher` from `json:"-"` to
// a real field survives every assertion above, because the ciphertext is
// not the plaintext and the plaintext is what those look for. But
// publishing the blob hands out an offline attack surface that becomes a
// live credential the day SECRETS_KEY leaks — and it is exactly the
// mistake `chat.providers` avoids with the same tag.
//
// So the property is stated directly: no response carries the ciphertext,
// under any field name.
func TestTheSealedBlobIsNeverSerialisedEither(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	var cipher []byte
	if err := e.pool.QueryRow(context.Background(),
		`SELECT token_cipher FROM github.connections WHERE workspace_id = $1`,
		e.wsA).Scan(&cipher); err != nil {
		t.Fatalf("read the stored connection: %v", err)
	}
	if len(cipher) == 0 {
		t.Fatal("nothing was sealed; this test would pass vacuously")
	}
	// Base64 is how a []byte reaches JSON in Go, so that is the form to
	// look for — comparing raw bytes against a JSON body would never match
	// even when the field is present.
	encoded := base64.StdEncoding.EncodeToString(cipher)

	for _, path := range []string{
		"/integrations/github/connection",
		"/integrations/github/repositories",
		"/integrations/github/activity",
	} {
		body := e.do("GET", path, e.wsA, nil).Body.String()
		if strings.Contains(body, encoded) {
			t.Fatalf("%s published the sealed credential", path)
		}
		// The field name too: a rename would move the blob rather than
		// remove it, and the encoding check alone would not notice a
		// different serialisation of the same bytes.
		if strings.Contains(body, "token_cipher") {
			t.Fatalf("%s carries a token_cipher field", path)
		}
	}

	// And on the connect response, which is built from the same struct.
	rec := e.do("POST", "/integrations/github/connection", e.wsA, map[string]any{
		"token": testToken, "api_base_url": e.gh.srv.URL,
	})
	if strings.Contains(rec.Body.String(), "token_cipher") {
		t.Fatal("the connect response carries a token_cipher field")
	}
}

// The workspace predicate on `github.repositories`, checked where it is
// actually observable.
//
// ── Why this test exists ───────────────────────────────────────────────
// A mutation that removed `workspace_id` from the repository queries
// survived the end-to-end suite, and correctly so: a connection already
// belongs to exactly one workspace and is resolved by workspace first, so
// scoping by connection_id alone produces the same answers through every
// path that exists today.
//
// That makes the predicate defence in depth — and defence in depth that no
// test can see is defence nobody will keep. The day a second way of
// resolving a connection appears (a shared connection, an admin path, a
// background job that iterates connections), this predicate is what stands
// between it and a cross-workspace read. So it is asserted directly,
// against the repository, with a deliberately mismatched pair that no HTTP
// route can currently produce.
func TestTheRepositoryStoreRefusesAMismatchedWorkspaceAndConnection(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	var connID uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id FROM github.connections WHERE workspace_id = $1`, e.wsA).Scan(&connID); err != nil {
		t.Fatalf("read the connection id: %v", err)
	}

	repos := ghrepo.New(e.pool).Repositories
	ctx := context.Background()

	// The honest pair still works, so a failure below is about the
	// workspace and not about the fixture.
	if _, err := repos.FindByFullName(ctx, e.wsA, connID, "joaocorsi/c.o.r.s.i"); err != nil {
		t.Fatalf("the authorized pair was refused: %v", err)
	}

	// The mismatched one: workspace B naming workspace A's connection.
	if _, err := repos.FindByFullName(ctx, e.wsB, connID, "joaocorsi/c.o.r.s.i"); err == nil {
		t.Fatal("a repository was resolved for a workspace that does not own the connection")
	}
	got, err := repos.ListByConnection(ctx, e.wsB, connID)
	if err != nil {
		t.Fatalf("ListByConnection: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listed %d repositories for a workspace that does not own the connection", len(got))
	}
	n, err := repos.CountByConnection(ctx, e.wsB, connID)
	if err != nil {
		t.Fatalf("CountByConnection: %v", err)
	}
	if n != 0 {
		t.Fatalf("counted %d repositories across the workspace boundary", n)
	}
}

// Connecting an account authorizes nothing to read. If it did, the
// two-layer model would be decorative.
func TestConnectingAuthorizesNoRepositories(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	st := e.status(e.wsA)
	if !st.Connected {
		t.Fatal("not connected after connecting")
	}
	if len(st.Repositories) != 0 {
		t.Fatalf("connecting authorized %d repositories", len(st.Repositories))
	}
}

func TestAnInvalidTokenIsRefusedActionablyAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.gh.fail(http.StatusUnauthorized, `{"message":"Bad credentials"}`, nil)

	rec := e.do("POST", "/integrations/github/connection", e.wsA, map[string]any{
		"token": testToken, "api_base_url": e.gh.srv.URL,
	})
	wantErrorCode(t, rec, http.StatusConflict, domain.CodeGitHubUnauthorized)
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatal("the refusal quoted the credential")
	}

	// Nothing was written: a card showing an account whose token never
	// worked is worse than no card.
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM github.connections`).Scan(&n); err != nil {
		t.Fatalf("count connections: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d connections were stored for a token GitHub rejected", n)
	}
}

func TestARepositoryTheCredentialCannotSeeCannotBeAuthorized(t *testing.T) {
	// The browser sends names. Writing them straight into the table would
	// let a client authorize something the token cannot reach — a row that
	// grants nothing today and is a claim the interface would repeat.
	e := newEnv(t)
	e.connect(e.wsA)

	rec := e.do("PUT", "/integrations/github/repositories", e.wsA,
		map[string]any{"repositories": []string{"someoneelse/private"}})
	wantStatus(t, rec, http.StatusBadRequest)

	if len(e.status(e.wsA).Repositories) != 0 {
		t.Fatal("a repository outside the credential's reach was authorized")
	}
}

func TestOrganizationsAreDerivedFromWhatIsActuallyAuthorized(t *testing.T) {
	// Not read from GET /user/orgs: a fine-grained PAT does not reliably
	// enumerate organizations, and the page's question is "which owners can
	// C.O.R.S.I. reach", which the authorized set answers exactly.
	e := newEnv(t)
	e.connect(e.wsA)
	st := e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i", "acme/internal-api")

	if len(st.Organizations) != 1 || st.Organizations[0] != "acme" {
		t.Fatalf("organizations = %v, want [acme]", st.Organizations)
	}
	for _, p := range e.gh.requested() {
		if strings.Contains(p, "/orgs") {
			t.Fatal("the page asked GitHub to enumerate organizations")
		}
	}
}

/* ══ workspace isolation ═════════════════════════════════════════════ */

func TestOneWorkspaceCannotSeeOrUseAnothersConnection(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	if st := e.status(e.wsB); st.Connected {
		t.Fatal("workspace B sees workspace A's GitHub connection")
	}
	if len(e.status(e.wsB).Repositories) != 0 {
		t.Fatal("workspace B sees workspace A's authorized repositories")
	}

	// And a tool run under B cannot reach A's repository. This is the
	// assertion that matters: the read above could pass on an API that
	// filters while the tool path does not.
	s := e.seed(e.wsB)
	e.authorizeTool(e.wsB, s.agentID, "github.commit.list")
	e.gh.reset()
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i"}`),
		answer("não consegui"),
	}
	wantStatus(t, e.send(e.wsB, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsB, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v, want one refusal", calls)
	}
	if e.gh.touched("/repos/") {
		t.Fatal("a cross-workspace repository read reached GitHub")
	}
}

/* ══ the catalogue and `@` ═══════════════════════════════════════════ */

// The `@` menu renders what this endpoint returns and invents nothing, so
// "GitHub capabilities appear in @" is a property of this response.
func TestTheAgentCatalogueExposesTheGitHubToolsAndDeniesThemByDefault(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	report := e.agentTools(e.wsA, s.agentID)

	want := []string{
		"github.code.search", "github.commit.get", "github.commit.list",
		"github.file.get", "github.pull_request.get", "github.pull_request.list",
		"github.repository.list",
	}
	byName := map[string]bool{}
	for _, item := range report.Items {
		byName[item.Name] = true
		if strings.HasPrefix(item.Name, "github.") {
			if item.Effect != "read" {
				t.Fatalf("%s declares effect %q; this version is read-only", item.Name, item.Effect)
			}
			if item.Internal {
				t.Fatalf("%s is marked internal", item.Name)
			}
			if !strings.HasPrefix(item.Title, "GitHub · ") {
				t.Fatalf("%s has title %q; the menu groups on this prefix", item.Name, item.Title)
			}
			if item.Authorized {
				t.Fatalf("%s is authorized on a brand new agent", item.Name)
			}
		}
	}
	for _, name := range want {
		if !byName[name] {
			t.Fatalf("the catalogue is missing %s", name)
		}
	}
	if report.AuthorizedCount != 0 {
		t.Fatalf("authorized_count = %d, want 0", report.AuthorizedCount)
	}
}

// The production registry has no diagnostics in it, so a real deployment
// offers seven real capabilities and nothing that only looks like one.
func TestTheProductionCatalogueContainsNoDiagnostics(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	for _, item := range e.agentTools(e.wsA, s.agentID).Items {
		if item.Internal {
			t.Fatalf("%s is a diagnostic and is in the production catalogue", item.Name)
		}
	}
}

/* ══ the conversation ════════════════════════════════════════════════ */

// Scenario 1: "@GitHub quais foram os últimos commits?"
func TestAnAgentAnswersWithCommitsItReadThroughTheTool(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	e.gh.commitsFor["joaocorsi/c.o.r.s.i"] = []map[string]any{
		commitFixture("f00d123", "feat: budget gate per provider call"),
		commitFixture("beef456", "fix: tool round ceiling"),
	}

	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i","limit":5}`),
		answer("Os últimos commits são f00d123 e beef456."),
	}

	// With `@`, which is the shape the scenario describes.
	wantStatus(t, e.send(e.wsA, s.conversationID, "quais foram os últimos commits?",
		toolRef("github.commit.list")), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want one", calls)
	}
	if calls[0].Status != "ok" {
		t.Fatalf("the call failed: %+v", calls[0])
	}
	// The tool result carried the real fixture data, which is what makes
	// this an end-to-end read rather than a successful no-op.
	if calls[0].Result == nil || !strings.Contains(*calls[0].Result, "f00d123") {
		t.Fatalf("the result does not contain the commit the fake served: %v", calls[0].Result)
	}
	if !strings.Contains(*calls[0].Result, "budget gate") {
		t.Fatalf("the commit message did not survive: %v", *calls[0].Result)
	}
	if !e.gh.touched("/repos/joaocorsi/c.o.r.s.i/commits") {
		t.Fatalf("the commits endpoint was never called: %v", e.gh.requested())
	}
	// The turn made two provider calls: ask, then answer.
	if e.llm.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", e.llm.calls)
	}
	if got := e.lastAssistantText(e.wsA, s.conversationID); !strings.Contains(got, "f00d123") {
		t.Fatalf("the assistant answer does not use the tool result: %q", got)
	}
}

// Scenario 2: "procure onde implementamos budget" — search, then open.
func TestAnAgentSearchesThenOpensTheFileItFound(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	e.gh.search = map[string]any{
		"total_count": 1, "incomplete_results": false,
		"items": []any{map[string]any{
			"path":       "internal/chat/app/budget.go",
			"html_url":   "https://github.com/joaocorsi/c.o.r.s.i/blob/main/internal/chat/app/budget.go",
			"repository": map[string]any{"full_name": "joaocorsi/c.o.r.s.i"},
			"text_matches": []any{
				map[string]any{"fragment": "func (s *Service) budgetPreflight("},
			},
		}},
	}

	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.code.search")
	e.authorizeTool(e.wsA, s.agentID, "github.file.get")
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.code.search", `{"query":"budget"}`),
		askTool("c2", "github.file.get",
			`{"repository":"joaocorsi/c.o.r.s.i","path":"internal/chat/app/budget.go"}`),
		answer("O budget é aplicado em internal/chat/app/budget.go, na budgetPreflight."),
	}

	wantStatus(t, e.send(e.wsA, s.conversationID, "procure onde implementamos budget", nil),
		http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want a search and a read", calls)
	}
	if calls[0].ToolName != "github.code.search" || calls[0].Status != "ok" {
		t.Fatalf("first call = %+v", calls[0])
	}
	if calls[0].Round != 1 || calls[1].Round != 2 {
		t.Fatalf("rounds = %d and %d, want 1 and 2", calls[0].Round, calls[1].Round)
	}
	if calls[1].ToolName != "github.file.get" || calls[1].Status != "ok" {
		t.Fatalf("second call = %+v", calls[1])
	}
	if calls[1].Result == nil || !strings.Contains(*calls[1].Result, "budget lives here") {
		t.Fatalf("the file content did not reach the model: %v", calls[1].Result)
	}
	if !e.gh.touched("/search/code") || !e.gh.touched("/contents/") {
		t.Fatalf("the expected endpoints were not called: %v", e.gh.requested())
	}
}

// Scenario 3: the model names a repository nobody authorized.
func TestAnUnauthorizedRepositoryIsDeniedBeforeGitHubIsCalled(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	// Only one repository is authorized, of the three the token can see.
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")
	e.gh.reset()
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"acme/internal-api"}`),
		answer("não tenho acesso a esse repositório"),
	}

	wantStatus(t, e.send(e.wsA, s.conversationID, "commits do internal-api?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v, want one refusal", calls)
	}
	// The refusal has to be actionable, and it must not be a 404-shaped
	// answer that teaches the model to try spelling variations.
	if !strings.Contains(calls[0].ErrorMessage, "not authorized") {
		t.Fatalf("error message = %q", calls[0].ErrorMessage)
	}
	// The property that matters: DENY happened here, not at GitHub. A
	// credential with access to `acme/internal-api` would have succeeded.
	if e.gh.touched("acme/internal-api") {
		t.Fatalf("an unauthorized repository reached GitHub: %v", e.gh.requested())
	}
}

// The same refusal for every shape a model might invent, none of which may
// reach the network.
func TestNoRepositoryStringOutsideTheAuthorizedSetEverReachesGitHub(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	for _, attempt := range []string{
		"acme/internal-api",
		"joaocorsi/c.o.r.s.i/../../acme/internal-api",
		"../../etc/passwd",
		"https://github.com/acme/internal-api",
		"joaocorsi/c.o.r.s.i%2f..%2fdotfiles",
		strings.Repeat("a", 200) + "/b",
	} {
		e.gh.reset()
		conv := e.do("POST", "/chat/conversations", e.wsA,
			map[string]any{"agent_id": s.agentID})
		wantStatus(t, conv, http.StatusCreated)
		cid := decode[map[string]any](t, conv)["id"].(string)

		e.llm.calls = 0
		e.llm.rounds = [][]chatports.StreamEvent{
			askTool("c1", "github.commit.list", `{"repository":`+quote(attempt)+`}`),
			answer("recusado"),
		}
		wantStatus(t, e.send(e.wsA, cid, "commits?", nil), http.StatusOK)

		calls := e.toolCalls(e.wsA, cid)
		if len(calls) != 1 || calls[0].Status == "ok" {
			t.Fatalf("%q: calls = %+v, want a refusal", attempt, calls)
		}
		if e.gh.touched("/repos/") {
			t.Fatalf("%q reached GitHub: %v", attempt, e.gh.requested())
		}
	}
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// Case folding is a convenience, not a hole: it resolves to the SAME stored
// row, and the request is built from that row's columns.
func TestARepositoryNamedInADifferentCaseResolvesToTheStoredRow(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	e.gh.reset()
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"JoaoCorsi/C.O.R.S.I"}`),
		answer("ok"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status != "ok" {
		t.Fatalf("calls = %+v", calls)
	}
	// The URL carries the STORED spelling, not the model's.
	if !e.gh.touched("/repos/joaocorsi/c.o.r.s.i/commits") {
		t.Fatalf("requests = %v", e.gh.requested())
	}
	// And the result names the stored repository, so the transcript records
	// what was read rather than what was typed.
	if !strings.Contains(*calls[0].Result, `"joaocorsi/c.o.r.s.i"`) {
		t.Fatalf("the result echoes the model's spelling: %v", *calls[0].Result)
	}
}

// Scenario 4: the tool grant is revoked; the next turn cannot use it.
func TestRevokingAToolGrantStopsTheNextTurn(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i"}`),
		answer("aqui estão"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)
	if calls := e.toolCalls(e.wsA, s.conversationID); calls[0].Status != "ok" {
		t.Fatalf("the first turn already failed: %+v", calls[0])
	}

	e.revokeTool(e.wsA, s.agentID, "github.commit.list")

	e.gh.reset()
	e.llm.calls = 0
	conv := e.do("POST", "/chat/conversations", e.wsA, map[string]any{"agent_id": s.agentID})
	cid := decode[map[string]any](t, conv)["id"].(string)
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c2", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i"}`),
		answer("não posso"),
	}
	wantStatus(t, e.send(e.wsA, cid, "e agora?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, cid)
	if len(calls) != 1 || calls[0].ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("calls = %+v, want tool_not_authorized", calls)
	}
	if e.gh.touched("/repos/") {
		t.Fatal("a revoked tool still reached GitHub")
	}
}

// Scenario 5: the repository authorization is revoked while the TOOL grant
// survives. Two separate decisions, and the second one is not the first.
func TestRevokingARepositoryDeniesEvenAnAgentThatStillHoldsTheToolGrant(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i", "joaocorsi/dotfiles")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	// The operator narrows the authorized set. The agent's grant is
	// untouched.
	e.authorizeRepos(e.wsA, "joaocorsi/dotfiles")

	if report := e.agentTools(e.wsA, s.agentID); report.AuthorizedCount != 1 {
		t.Fatalf("the tool grant was disturbed: %d", report.AuthorizedCount)
	}

	e.gh.reset()
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i"}`),
		answer("perdi o acesso"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v, want a refusal", calls)
	}
	if !strings.Contains(calls[0].ErrorMessage, "not authorized") {
		t.Fatalf("error = %q", calls[0].ErrorMessage)
	}
	if e.gh.touched("c.o.r.s.i") {
		t.Fatal("a revoked repository reached GitHub")
	}
}

// Disconnecting removes the credential and, with it, every authorization
// that depended on it.
func TestDisconnectingMakesEveryToolRefuseAndDropsTheAuthorizations(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	wantStatus(t, e.do("DELETE", "/integrations/github/connection", e.wsA, nil),
		http.StatusNoContent)

	if st := e.status(e.wsA); st.Connected || len(st.Repositories) != 0 {
		t.Fatalf("still connected after disconnect: %+v", st)
	}
	// The cascade actually fired: no orphan authorizations survive.
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM github.repositories`).Scan(&n); err != nil {
		t.Fatalf("count repositories: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d repository authorizations outlived the connection", n)
	}

	e.gh.reset()
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i"}`),
		answer("desconectado"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v, want a refusal", calls)
	}
	if !strings.Contains(strings.ToLower(calls[0].ErrorMessage), "no github connection") {
		t.Fatalf("error = %q, want it to name the missing connection", calls[0].ErrorMessage)
	}
	if e.gh.touched("/repos/") {
		t.Fatal("a disconnected integration still reached GitHub")
	}
}

/* ══ GitHub's own failures ═══════════════════════════════════════════ */

// Scenario 6: the token was revoked at GitHub after it was stored.
func TestARevokedTokenBecomesAnActionableToolErrorWithNoCredentialInIt(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	e.gh.fail(http.StatusUnauthorized, `{"message":"Bad credentials"}`, nil)
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i"}`),
		answer("o token expirou"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(calls[0].ErrorMessage, "Reconnect") {
		t.Fatalf("the error does not say what to do: %q", calls[0].ErrorMessage)
	}
	// The audit row is the thing most likely to outlive everyone's memory.
	for _, c := range calls {
		if c.Arguments != nil && strings.Contains(*c.Arguments, testToken) {
			t.Fatal("the credential is in the audit trail arguments")
		}
		if c.Result != nil && strings.Contains(*c.Result, testToken) {
			t.Fatal("the credential is in the audit trail result")
		}
		if strings.Contains(c.ErrorMessage, testToken) {
			t.Fatal("the credential is in the audit trail error message")
		}
	}
	// The turn survived: a failed tool call is a fact about the
	// conversation, not a 500.
	if e.llm.calls != 2 {
		t.Fatalf("provider calls = %d; the turn did not continue after the failure", e.llm.calls)
	}
}

// Scenario 7: a rate limit must be distinguishable from everything else,
// because it is the only upstream failure that clears by waiting.
func TestARateLimitIsItsOwnActionableError(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.code.search")

	reset := strconv.FormatInt(time.Now().Add(45*time.Second).Unix(), 10)
	e.gh.fail(http.StatusForbidden, `{"message":"API rate limit exceeded"}`, map[string]string{
		"X-RateLimit-Remaining": "0",
		"X-RateLimit-Reset":     reset,
		"X-RateLimit-Resource":  "code_search",
	})
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.code.search", `{"query":"budget"}`),
		answer("atingi o limite"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "procure budget", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v", calls)
	}
	msg := calls[0].ErrorMessage
	if !strings.Contains(msg, "rate limit") {
		t.Fatalf("a rate limit was reported as %q", msg)
	}
	// Distinguishable from a revoked token, which is the whole point.
	if strings.Contains(msg, "Reconnect") {
		t.Fatalf("a rate limit reads as an auth failure: %q", msg)
	}
	if !strings.Contains(msg, "seconds") {
		t.Fatalf("the message does not say when it clears: %q", msg)
	}
}

// The management API maps the same failures onto HTTP, and 401/429 are not
// what a naive mapping would produce. See httpapi.writeErr.
func TestTheManagementAPIMapsGitHubFailuresOntoTheRightStatuses(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	e.gh.fail(http.StatusUnauthorized, `{"message":"Bad credentials"}`, nil)
	rec := e.do("GET", "/integrations/github/repositories", e.wsA, nil)
	// 409, not 401: answering 401 would tell the browser to re-authenticate
	// against C.O.R.S.I., which is the wrong action entirely.
	wantErrorCode(t, rec, http.StatusConflict, domain.CodeGitHubUnauthorized)

	e.gh.fail(http.StatusForbidden, `{"message":"rate limited"}`,
		map[string]string{"X-RateLimit-Remaining": "0"})
	rec = e.do("GET", "/integrations/github/repositories", e.wsA, nil)
	wantErrorCode(t, rec, http.StatusTooManyRequests, domain.CodeGitHubRateLimited)

	e.gh.fail(http.StatusBadGateway, "", nil)
	rec = e.do("GET", "/integrations/github/repositories", e.wsA, nil)
	wantStatus(t, rec, http.StatusBadGateway)

	e.gh.recover()
	rec = e.do("GET", "/integrations/github/repositories", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
}

func TestAWorkspaceWithNoConnectionGetsAConflictAndNotAnEmptyList(t *testing.T) {
	// An empty list would be a true sentence about a false premise: it
	// reads as "you have no repositories" rather than "you have no GitHub".
	e := newEnv(t)
	rec := e.do("GET", "/integrations/github/repositories", e.wsA, nil)
	wantErrorCode(t, rec, http.StatusConflict, domain.CodeGitHubNotConnected)
}

/* ══ result limits ═══════════════════════════════════════════════════ */

// Scenario 8: a large result is bounded, and the bound is declared.
func TestALargeResultIsBoundedAndSaysSo(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	// Fifty commits with long messages: comfortably past the tool budget
	// once serialised.
	big := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		big = append(big, commitFixture(fmt.Sprintf("sha%040d", i),
			strings.Repeat("uma mensagem de commit bem longa ", 20)))
	}
	e.gh.commitsFor["joaocorsi/c.o.r.s.i"] = big

	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i","limit":50}`),
		answer("resumo"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status != "ok" {
		t.Fatalf("calls = %+v; a large result must be bounded, not refused", calls)
	}
	result := *calls[0].Result
	// Below the ceiling Agents enforces, which is what keeps this an answer
	// rather than an execution failure.
	if len(result) > chatdomain.MaxToolResultBytes {
		t.Fatalf("result is %d bytes, above the Agents ceiling of %d",
			len(result), chatdomain.MaxToolResultBytes)
	}
	// And the cut is stated. A list that was truncated silently is
	// indistinguishable from a complete one.
	if !strings.Contains(result, `"truncated":true`) {
		t.Fatalf("the result was cut and does not declare it: %s", result[:min(400, len(result))])
	}
	if !strings.Contains(result, "truncated_note") {
		t.Fatal("the truncation carries no instruction the model can act on")
	}
}

func TestAFileLargerThanTheCeilingComesBackTruncatedAndLabelled(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	content := strings.Repeat("x", domain.MaxFileBytes+8000)
	e.gh.file = map[string]any{
		"type": "file", "path": "huge.txt", "sha": "s", "size": len(content),
		"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)),
	}

	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.file.get")
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.file.get", `{"repository":"joaocorsi/c.o.r.s.i","path":"huge.txt"}`),
		answer("li o começo"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "abra huge.txt", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status != "ok" {
		t.Fatalf("calls = %+v", calls)
	}
	result := *calls[0].Result
	if len(result) > chatdomain.MaxToolResultBytes {
		t.Fatalf("result is %d bytes", len(result))
	}
	if !strings.Contains(result, `"truncated":true`) {
		t.Fatal("a cut file does not declare it")
	}
	// The real size survives, so the model can say how much it did not see.
	if !strings.Contains(result, strconv.Itoa(len(content))) {
		t.Fatal("the file's real size was lost")
	}
}

/* ══ compatibility ═══════════════════════════════════════════════════ */

// Scenario 10: an installation with no GitHub connection behaves exactly as
// it did before this batch.
func TestAnAgentWithNoGitHubConnectionIsUnaffected(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// A turn with no tools declares none, and the request body is the one a
	// pre-tools deployment sent.
	wantStatus(t, e.send(e.wsA, s.conversationID, "olá", nil), http.StatusOK)
	if len(e.llm.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(e.llm.requests))
	}
	if len(e.llm.requests[0].Tools) != 0 {
		t.Fatalf("a tool-less agent declared %d tools", len(e.llm.requests[0].Tools))
	}
	if len(e.toolCalls(e.wsA, s.conversationID)) != 0 {
		t.Fatal("a turn with no tools produced audit rows")
	}
}

// A granted GitHub tool with no connection behind it fails with a sentence
// that names the missing piece, rather than looking like a broken tool.
func TestAGrantedGitHubToolWithNoConnectionRefusesActionably(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.repository.list")

	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.repository.list", `{}`),
		answer("não há integração"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "quais repos?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status == "ok" {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(strings.ToLower(calls[0].ErrorMessage), "no github connection") {
		t.Fatalf("error = %q", calls[0].ErrorMessage)
	}
}

// Scenario 9: a turn WITHOUT `@` still declares every authorized tool. The
// selection narrows; its absence changes nothing.
func TestATurnWithoutASelectionDeclaresEveryAuthorizedTool(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")
	e.authorizeTool(e.wsA, s.agentID, "github.code.search")

	e.llm.rounds = [][]chatports.StreamEvent{answer("nada a fazer")}
	wantStatus(t, e.send(e.wsA, s.conversationID, "olá", nil), http.StatusOK)

	declared := e.llm.requests[0].Tools
	if len(declared) != 2 {
		t.Fatalf("declared %d tools, want both grants", len(declared))
	}
}

// And a turn WITH `@` narrows to the selection — never widens past it.
func TestASelectionNarrowsAndCannotWiden(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")
	e.authorizeTool(e.wsA, s.agentID, "github.code.search")

	e.llm.rounds = [][]chatports.StreamEvent{answer("ok")}
	wantStatus(t, e.send(e.wsA, s.conversationID, "olá", toolRef("github.commit.list")),
		http.StatusOK)

	declared := e.llm.requests[0].Tools
	if len(declared) != 1 || declared[0].Name != "github.commit.list" {
		t.Fatalf("declared = %+v, want only the selected tool", declared)
	}

	// Selecting something the agent was never granted refuses the turn
	// rather than granting it.
	rec := e.send(e.wsA, s.conversationID, "e agora", toolRef("github.file.get"))
	if rec.Code == http.StatusOK {
		t.Fatal("a selection granted a tool the agent does not hold")
	}
}

/* ══ audit ═══════════════════════════════════════════════════════════ */

// The existing audit trail remains the source of truth, and what it records
// about a GitHub call is a recognisable account of it.
func TestTheAuditTrailRecordsTheGitHubCallWithoutAnyCredential(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	e.gh.commitsFor["joaocorsi/c.o.r.s.i"] = []map[string]any{
		commitFixture("f00d123", "feat: something"),
	}
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.commit.list", `{"repository":"joaocorsi/c.o.r.s.i","limit":3}`),
		answer("pronto"),
	}
	wantStatus(t, e.send(e.wsA, s.conversationID, "commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	c := calls[0]
	if c.ToolName != "github.commit.list" || c.Round != 1 || c.Status != "ok" {
		t.Fatalf("call = %+v", c)
	}
	if c.Arguments == nil || !strings.Contains(*c.Arguments, "joaocorsi/c.o.r.s.i") {
		t.Fatalf("arguments = %v; the audit must record what was asked", c.Arguments)
	}
	if c.Result == nil {
		t.Fatal("the audit recorded no result for a successful call")
	}

	// The credential must be absent from every column, and this is checked
	// against the database rather than the API so a filtering read cannot
	// make it pass.
	rows, err := e.pool.Query(context.Background(),
		`SELECT coalesce(arguments,''), coalesce(result,''), error_message FROM chat.tool_calls`)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var args, result, msg string
		if err := rows.Scan(&args, &result, &msg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, field := range []string{args, result, msg} {
			if strings.Contains(field, testToken) {
				t.Fatal("a credential reached the audit trail")
			}
		}
	}
}

/* ── the repository listing, as evidence ─────────────────────────────── */

// listRepositories drives one turn that calls github.repository.list and
// returns the serialised result, which is what the model actually reads.
func (e *env) listRepositories(s seeded, args string) string {
	e.t.Helper()
	e.authorizeTool(e.wsA, s.agentID, "github.repository.list")
	e.llm.rounds = [][]chatports.StreamEvent{
		askTool("c1", "github.repository.list", args),
		answer("são esses"),
	}
	wantStatus(e.t, e.send(e.wsA, s.conversationID, "quais repositórios?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Status != "ok" {
		e.t.Fatalf("the listing call did not succeed: %+v", calls)
	}
	if calls[0].Result == nil {
		e.t.Fatal("the listing returned no result")
	}
	return *calls[0].Result
}

// The listing has to carry enough for a reader to decide whether it can say
// something true about a repository or has to open it. Before this batch it
// returned a name, a visibility, a branch and a description — nothing about
// what a repository IS — and the model filled the gap by guessing.
func TestTheRepositoryListingCarriesTheDetailAJudgementNeeds(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	got := e.listRepositories(e.seed(e.wsA), `{}`)

	for _, want := range []string{
		"joaocorsi/c.o.r.s.i", // the name, which is also the next call's key
		"Go",                  // language
		"2.0 MB",              // size, from the fixture's 2048 KB
		"main",                // default branch
		"public",              // visibility
		"c.o.r.s.i description",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the listing does not carry %q: %s", want, got)
		}
	}
	// The push date, as a date. The fixture stamps today.
	if !strings.Contains(got, time.Now().UTC().Format("2006-01-02")) {
		t.Fatalf("the listing carries no last-push date: %s", got)
	}
	// And the legend, so the positions mean something without six field
	// names repeated on every row.
	if !strings.Contains(got, "owner/name | language | size") {
		t.Fatalf("the listing has no format legend: %s", got)
	}
}

// The point of the compact form: more evidence must not cost more tokens.
// A JSON object per repository with these six fields runs about 170
// characters; a line runs about half that.
func TestTheEnrichedListingStaysCompact(t *testing.T) {
	e := newEnv(t)
	e.gh.repos = nil
	for i := 1; i <= 32; i++ {
		e.gh.repos = append(e.gh.repos,
			repoFixture(i, "joaocorsi", "User", "repo-"+strconv.Itoa(i), false))
	}
	e.connect(e.wsA)
	names := make([]string, 0, 32)
	for i := 1; i <= 32; i++ {
		names = append(names, "joaocorsi/repo-"+strconv.Itoa(i))
	}
	e.authorizeRepos(e.wsA, names...)

	got := e.listRepositories(e.seed(e.wsA), `{}`)
	if !strings.Contains(got, "repo-32") {
		t.Fatalf("not every authorized repository was listed: %s", got)
	}
	// Measured, not guessed: the ceiling is what the four-field version cost
	// for the same set, with room for longer descriptions.
	const ceiling = 5000
	if len(got) > ceiling {
		t.Fatalf("32 repositories serialised to %d characters, over the %d ceiling; "+
			"the compact form is the reason the extra fields are affordable",
			len(got), ceiling)
	}
}

// A fork is somebody else's work and an archive is a finished thing. Both
// change what the repository MEANS, so both are stated rather than left to
// be inferred from a stale push date.
func TestForkAndArchivedAreStatedAndTheOrdinaryCaseIsSilent(t *testing.T) {
	e := newEnv(t)
	fork := repoFixture(9, "joaocorsi", "User", "someone-elses", false)
	fork["fork"], fork["archived"] = true, true
	e.gh.repos = []map[string]any{
		repoFixture(1, "joaocorsi", "User", "c.o.r.s.i", false),
		fork,
	}
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i", "joaocorsi/someone-elses")

	got := e.listRepositories(e.seed(e.wsA), `{}`)
	if !strings.Contains(got, "public, fork, archived") {
		t.Fatalf("a fork of an archived repository was not declared: %s", got)
	}
	// And the ordinary repository pays nothing for the flags it does not
	// have: a line reading "not a fork" on every row is characters spent to
	// say the common case.
	if strings.Contains(got, "not a fork") || strings.Contains(got, "not archived") {
		t.Fatalf("the ordinary case is being spelled out: %s", got)
	}
}

// The enrichment is an improvement on top of the listing, not a dependency
// of it. GitHub being unreachable must leave the authorized set complete —
// and must say the detail is missing rather than showing "-" as a fact.
func TestTheListingSurvivesGitHubBeingUnreachable(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")

	// Only after the authorization is stored: from here on GitHub refuses.
	e.gh.status, e.gh.body = http.StatusServiceUnavailable, `{"message":"down"}`

	got := e.listRepositories(e.seed(e.wsA), `{}`)
	if !strings.Contains(got, "joaocorsi/c.o.r.s.i") {
		t.Fatalf("the authorized set was lost with the enrichment: %s", got)
	}
	if !strings.Contains(got, "could not be reached") {
		t.Fatalf("missing detail was shown as if it were a fact: %s", got)
	}
}

// The name is the one value that has to survive the round trip: the caller
// reads it here and sends it back as the next call's `repository` argument.
func TestTheListedNameIsAcceptedByAnotherTool(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.authorizeRepos(e.wsA, "joaocorsi/c.o.r.s.i")
	e.gh.commitsFor["joaocorsi/c.o.r.s.i"] = []map[string]any{
		commitFixture("cafe001", "chore: something"),
	}

	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, "github.repository.list")
	e.authorizeTool(e.wsA, s.agentID, "github.commit.list")

	listed := e.listRepositories(s, `{}`)

	// Read the name back out the way a caller would: take the first field of
	// the first line, as the legend says to.
	var payload struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(listed), &payload); err != nil {
		t.Fatalf("the listing is not decodable: %v (%s)", err, listed)
	}
	if len(payload.Repositories) == 0 {
		t.Fatalf("the listing is empty: %s", listed)
	}
	name, _, ok := strings.Cut(payload.Repositories[0], " | ")
	if !ok || name == "" {
		t.Fatalf("the first field of a line is not the name: %q", payload.Repositories[0])
	}

	// The fake indexes its rounds by the process-wide call counter, so a
	// second tool-using turn has to say where its script begins. Without the
	// padding the turn silently replays the previous answer, never calls the
	// tool, and the assertion below would be passing on the first turn's
	// result.
	pad := make([][]chatports.StreamEvent, e.llm.calls)
	for i := range pad {
		pad[i] = answer("já respondido")
	}
	e.llm.rounds = append(pad,
		askTool("c2", "github.commit.list", `{"repository":"`+name+`"}`),
		answer("li os commits"))
	wantStatus(t, e.send(e.wsA, s.conversationID, "e os commits?", nil), http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	last := calls[len(calls)-1]
	if last.Status != "ok" {
		t.Fatalf("the name read out of the listing was refused: %+v", last)
	}
	if last.Result == nil || !strings.Contains(*last.Result, "cafe001") {
		t.Fatalf("the follow-up read nothing: %v", last.Result)
	}
}
