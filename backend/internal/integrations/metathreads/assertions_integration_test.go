//go:build integration

package metathreads

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	mtapi "github.com/corsi/backend/internal/integrations/metathreads/adapters/api"
	mtapp "github.com/corsi/backend/internal/integrations/metathreads/app"
	mtdomain "github.com/corsi/backend/internal/integrations/metathreads/domain"
	mttools "github.com/corsi/backend/internal/integrations/metathreads/tools"
	threadsports "github.com/corsi/backend/internal/threads/ports"
	threadstools "github.com/corsi/backend/internal/threads/tools"
)

/* ── 1. the catalogue ────────────────────────────────────────────────── */

// Six capabilities, and every one of them a READ.
//
// The count check makes this a gate rather than a spot check: a tool added
// without a line here fails, which is the moment to decide whether an agent
// should be able to do that at all. For this integration that moment
// matters more than usual — Meta's API supports publishing, and the whole
// design is arranged so adding it would be a deliberate act.
func TestEveryMetaThreadsToolIsRegisteredAsARead(t *testing.T) {
	e := newEnv(t)

	want := map[chatdomain.ToolName]bool{
		mttools.ProfileGetTool: true, mttools.ProfileInsightsTool: true,
		mttools.PostListTool: true, mttools.PostGetTool: true,
		mttools.PostInsightsTool: true, mttools.PostSearchTool: true,
	}
	got := map[chatdomain.ToolName]chatdomain.ToolEffect{}
	for _, d := range e.reg.Definitions() {
		if d.Name.Namespace() == "meta_threads" {
			got[d.Name] = d.Effect
		}
	}
	for name := range want {
		effect, ok := got[name]
		if !ok {
			t.Fatalf("%s is not in the registry", name)
		}
		if effect != chatdomain.EffectRead {
			t.Errorf("%s declares effect %q; this integration ships no writes", name, effect)
		}
		if _, resolvable := e.reg.Lookup(name); !resolvable {
			t.Errorf("%s does not resolve to an executor", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("registry has %d meta_threads tools, want %d: %v", len(got), len(want), got)
	}
}

// The two namespaces must stay apart. `threads.*` is our pipeline and
// `meta_threads.*` is Meta's network, and a tool that landed in the wrong
// one would make the whole distinction unenforceable.
func TestTheTwoThreadsNamespacesDoNotOverlap(t *testing.T) {
	e := newEnv(t)
	for _, d := range e.reg.Definitions() {
		switch d.Name.Namespace() {
		case "threads":
			if !strings.HasPrefix(string(d.Name), "threads.thread.") {
				t.Errorf("%s claims the internal namespace and is not a thread capability", d.Name)
			}
		case "meta_threads":
			if !strings.HasPrefix(string(d.Name), "meta_threads.") {
				t.Errorf("%s is malformed", d.Name)
			}
			// And an external capability must never be a write.
			if d.Effect != chatdomain.EffectRead {
				t.Errorf("%s is a write on an external system", d.Name)
			}
		}
	}
}

// No verb that changes anything on Meta may exist, under any spelling.
func TestNoPublishingCapabilityExistsAtAll(t *testing.T) {
	e := newEnv(t)
	forbidden := []string{"publish", "post.create", "reply", "delete", "repost", "quote", "schedule"}
	for _, d := range e.reg.Definitions() {
		if d.Name.Namespace() != "meta_threads" {
			continue
		}
		for _, f := range forbidden {
			if strings.Contains(string(d.Name), f) {
				t.Errorf("%s looks like a write capability on Meta Threads", d.Name)
			}
		}
	}
}

func TestMetaThreadsToolDefinitionsAreValid(t *testing.T) {
	for _, tool := range mttools.New(nil) {
		if err := tool.Definition().Validate(); err != nil {
			t.Errorf("%s: %v", tool.Definition().Name, err)
		}
	}
}

/* ── 2. authorization ────────────────────────────────────────────────── */

// Registered is not authorized, asserted through the real turn loop.
func TestAnUnauthorizedAgentCannotReadMetaThreads(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	_, conv := e.newAgent(e.wsA, "unauthorized")

	e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.PostListTool, Arguments: `{"limit":5}`})
	sink := e.turn(e.wsA, conv, "veja meus posts")

	ev, ok := sink.finished(mttools.PostListTool)
	if !ok {
		t.Fatal("no terminal tool event")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Errorf("error code = %q, want %q", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}
	// And nothing was fetched from Meta at all.
	for _, r := range e.meta.seen() {
		if strings.Contains(r.Path, "/me/threads") {
			t.Fatal("an unauthorized agent's call still reached Meta")
		}
	}
}

// Every new capability starts denied on every existing agent.
func TestEveryMetaThreadsToolStartsUnauthorized(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "existing")

	report, err := e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agentID)
	if err != nil {
		t.Fatalf("agent tools: %v", err)
	}
	seen := 0
	for _, item := range report.Items {
		if item.Name.Namespace() != "meta_threads" {
			continue
		}
		seen++
		if item.Authorized {
			t.Errorf("%s was authorized without anyone granting it", item.Name)
		}
	}
	if seen != len(allMetaTools) {
		t.Errorf("the report lists %d meta_threads tools, want %d", seen, len(allMetaTools))
	}
}

// An agent granted the INTERNAL pipeline does not thereby get the EXTERNAL
// history. This is the grant-level expression of the whole separation.
func TestThreadGrantsDoNotConferMetaThreadsAccess(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allThreadTools...)

	e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.PostListTool, Arguments: `{}`})
	ev, _ := e.turn(e.wsA, conv, "veja meus posts").finished(mttools.PostListTool)
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("error code = %q; internal grants reached the external system", ev.ErrorCode)
	}
}

// And the reverse: an agent that may read Meta cannot write our pipeline.
func TestMetaGrantsDoNotConferInternalWrites(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "reader")
	e.authorize(e.wsA, agentID, allMetaTools...)

	e.llm.scriptCalls(chatdomain.ToolCall{
		Name: threadstools.ThreadCreateTool, Arguments: `{"title":"t","content":"c"}`,
	})
	ev, _ := e.turn(e.wsA, conv, "salva isso").finished(threadstools.ThreadCreateTool)
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("error code = %q; a Meta reader wrote to the pipeline", ev.ErrorCode)
	}
	if n := len(e.liveThreads(e.wsA)); n != 0 {
		t.Fatalf("%d internal threads exist", n)
	}
}

// An agent literally named Scout, or Content, gets nothing for its name.
func TestNoAgentIsPrivilegedByItsName(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	for _, name := range []string{"Scout", "Content"} {
		_, conv := e.newAgent(e.wsA, name)
		e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.ProfileGetTool, Arguments: `{}`})
		ev, _ := e.turn(e.wsA, conv, "quem sou eu no Threads?").finished(mttools.ProfileGetTool)
		if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
			t.Errorf("an agent called %q was allowed to read Meta; code = %q", name, ev.ErrorCode)
		}
	}
}

/* ── 3. the credential ───────────────────────────────────────────────── */

// The authorization URL can never carry a write scope. This is the
// read-only guarantee at the point where permissions are actually asked
// for, proved through the real service and the real builder.
func TestTheAuthorizationURLRequestsNoWriteScope(t *testing.T) {
	e := newEnv(t)
	raw, err := e.mt.AuthorizationURL("state-123")
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	for _, forbidden := range []string{
		"threads_content_publish", "threads_manage_replies", "threads_delete",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the authorization URL requests %q: %s", forbidden, raw)
		}
	}
	for _, required := range []string{"threads_basic", "threads_manage_insights", "threads_keyword_search"} {
		if !strings.Contains(raw, required) {
			t.Errorf("the authorization URL does not request %q", required)
		}
	}
	if !strings.HasPrefix(raw, mtapi.AuthorizationHost+"/oauth/authorize") {
		t.Errorf("the authorization URL does not point at Meta: %s", raw)
	}
	if !strings.Contains(raw, "state-123") {
		t.Error("the caller's state was dropped")
	}
	// The app SECRET must never appear in a URL a browser will follow.
	if strings.Contains(raw, testAppSecret) {
		t.Fatal("the app secret is in the authorization URL")
	}
}

// The full redemption: code → short-lived → long-lived → profile → sealed.
func TestConnectRedeemsTheCodeAndStoresASealedLongLivedToken(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	if conn.Username != "joaocorsi" {
		t.Errorf("username = %q", conn.Username)
	}
	// The identity came from a real round trip, which is what proves the
	// credential works before it is stored.
	if conn.LastVerifiedAt == nil {
		t.Error("the connection was stored without being verified")
	}
	// The stored expiry is the 60-day one from the LONG-LIVED exchange, not
	// the one-hour short-lived token.
	if d := conn.TokenExpiresAt.Sub(conn.CreatedAt); d < 59*24*time.Hour {
		t.Errorf("stored lifetime = %v; the short-lived token was stored instead of the long-lived one", d)
	}
	if !conn.HasScope(mtdomain.ScopeInsights) || !conn.HasScope(mtdomain.ScopeKeywordSearch) {
		t.Errorf("the granted scopes were lost: %v", conn.Scopes)
	}

	// The three calls happened in the documented order.
	var order []string
	for _, r := range e.meta.seen() {
		order = append(order, r.Path)
	}
	want := []string{"/oauth/access_token", "/access_token", "/v1.0/me"}
	for i, p := range want {
		if i >= len(order) || order[i] != p {
			t.Fatalf("call order = %v, want it to begin %v", order, want)
		}
	}
}

// The token is sealed on disk and never present in plaintext.
func TestTheStoredTokenIsSealedAndNeverRendered(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	var cipher []byte
	var hint string
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT token_cipher, token_hint FROM meta_threads.connections WHERE workspace_id = $1`,
		e.wsA).Scan(&cipher, &hint); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if strings.Contains(string(cipher), "LONG_LIVED_TOKEN") {
		t.Fatal("the token is on disk in plaintext")
	}
	if strings.Contains(hint, "LONG_LIVED_TOKEN") {
		t.Fatalf("the hint carries the token: %q", hint)
	}

	// And the management route renders the hint and nothing else.
	rec := e.do(http.MethodGet, "/integrations/meta-threads/connection", e.wsA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "LONG_LIVED_TOKEN") {
		t.Fatalf("the connection endpoint returned the token: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "token_hint") {
		t.Error("the response carries no hint, so a person cannot tell which credential is stored")
	}
	// The app secret must not be anywhere in it either.
	if strings.Contains(rec.Body.String(), testAppSecret) {
		t.Fatal("the app secret reached a response body")
	}
}

// Not connected is a STATE, not an error: a page asking "am I connected"
// must not have to treat a normal answer as a failure.
func TestNotConnectedIsAHundredAndNotAFourOhFour(t *testing.T) {
	e := newEnv(t)
	rec := e.do(http.MethodGet, "/integrations/meta-threads/connection", e.wsA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["connected"] != false {
		t.Errorf("connected = %v, want false", body["connected"])
	}
}

// Disconnecting destroys the credential rather than orphaning it.
func TestDisconnectDestroysTheStoredCredential(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	rec := e.do(http.MethodDelete, "/integrations/meta-threads/connection", e.wsA, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var cipher []byte
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT token_cipher FROM meta_threads.connections WHERE workspace_id = $1`,
		e.wsA).Scan(&cipher); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if len(cipher) != 0 {
		t.Fatal("a sealed credential survived the disconnect on a row nobody can reach")
	}
	// And every read now refuses.
	if _, err := e.executeErr(e.wsA, mttools.PostListTool, map[string]any{}); err == nil {
		t.Fatal("a disconnected workspace could still read Meta")
	}
}

// Meta refuses a refresh under 24 hours; so does this, with an explanation.
func TestRefreshRefusesBeforeTwentyFourHoursAndSaysWhen(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	rec := e.do(http.MethodPost, "/integrations/meta-threads/connection/refresh", e.wsA, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "24 hours") {
		t.Errorf("the refusal does not explain the rule: %s", rec.Body.String())
	}
	// And nothing was sent to Meta: the rule is checked here.
	for _, r := range e.meta.seen() {
		if r.Path == "/refresh_access_token" {
			t.Fatal("a refresh that could not succeed was still sent upstream")
		}
	}
}

// A deployment with no Meta app refuses honestly instead of half-working.
func TestAnUnconfiguredDeploymentRefusesToStartAnOAuthFlow(t *testing.T) {
	e := newEnv(t)
	bare := mtapp.NewService(nil, nil, nil, mtapp.AppConfig{}, nil)
	if bare.Configured() {
		t.Fatal("an empty app config reported configured")
	}
	if _, err := bare.AuthorizationURL(""); err == nil {
		t.Fatal("an unconfigured deployment produced an authorization URL")
	}
	// And the configured one does work, so the check is not vacuous.
	if !e.mt.Configured() {
		t.Fatal("the test deployment reported unconfigured")
	}
}

/* ── 4. workspace isolation ──────────────────────────────────────────── */

// One workspace's credential is unreachable from another, and the second
// workspace is told it has no connection rather than borrowing the first's.
func TestACredentialIsNeverSharedAcrossWorkspaces(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	if _, err := e.executeErr(e.wsB, mttools.PostListTool, map[string]any{}); err == nil {
		t.Fatal("wsB read Meta through wsA's credential")
	}
	rec := e.do(http.MethodGet, "/integrations/meta-threads/connection", e.wsB, nil)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["connected"] != false {
		t.Errorf("wsB sees connected = %v", body["connected"])
	}
	// wsA still works, so the isolation is not just "everything is broken".
	out := e.execute(t, e.wsA, mttools.PostListTool, map[string]any{"limit": 2})
	if len(out["posts"].([]any)) == 0 {
		t.Fatal("wsA lost its own connection")
	}
}

// No tool accepts a workspace or a token argument. The isolation is
// structural: there is no argument through which a model could name either.
func TestNoToolAcceptsAWorkspaceOrACredentialArgument(t *testing.T) {
	for _, tool := range mttools.New(nil) {
		for name := range tool.Definition().Schema.Properties {
			lower := strings.ToLower(name)
			for _, forbidden := range []string{"workspace", "token", "access", "secret", "credential"} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("%s declares a %q argument", tool.Definition().Name, name)
				}
			}
		}
	}
}

func (e *env) liveThreads(ws uuid.UUID) []any {
	e.t.Helper()
	items, _, err := e.threads.ListThreads(ctxFor(ws), ws, threadsPortsFilter())
	if err != nil {
		e.t.Fatalf("list internal threads: %v", err)
	}
	out := make([]any, 0, len(items))
	for _, i := range items {
		out = append(out, i)
	}
	return out
}

// threadsPortsFilter is the zero filter over the INTERNAL pipeline.
func threadsPortsFilter() threadsports.ThreadFilter { return threadsports.ThreadFilter{} }
