//go:build integration

package metathreads

import (
	"strings"
	"testing"

	mtdomain "github.com/corsi/backend/internal/integrations/metathreads/domain"
	mttools "github.com/corsi/backend/internal/integrations/metathreads/tools"
)

/*
Scope persistence — the regression for a bug that shipped green.

── What went wrong ────────────────────────────────────────────────────
Connect() filled Connection.Scopes from a `scope` field in Meta's token
response. Meta does not send one: the documented body of
graph.threads.net/oauth/access_token is `access_token` and `user_id`, and
nothing else.

The test fake had invented the field, so the whole suite passed while
every REAL connection stored an empty scope list — and the local gate then
refused insights and keyword search for a credential that had been granted
them. The failure was invisible until a real OAuth happened.

── What these tests hold ──────────────────────────────────────────────
That a realistic Meta response still produces a usable connection, that
the capabilities gated on scopes actually run afterwards, and — the
ratchet — that no response shape can ever influence the answer again.
*/

// The core regression: a response with NO scope field must still produce a
// connection that carries the permissions the flow asked for.
func TestConnectPersistsTheRequestedScopesWithoutAnyScopeFieldFromMeta(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	if len(conn.Scopes) == 0 {
		t.Fatal("a real-shaped Meta response produced a connection with no scopes at all")
	}
	for _, want := range mtdomain.ReadScopes {
		if !conn.HasScope(want) {
			t.Errorf("the stored connection is missing %q", want)
		}
	}
	if len(conn.Scopes) != len(mtdomain.ReadScopes) {
		t.Errorf("stored %d scopes, want the %d this flow requests: %v",
			len(conn.Scopes), len(mtdomain.ReadScopes), conn.Scopes)
	}

	// And they survive the round trip to Postgres, which is where the bug
	// actually showed: the value was fine in memory and empty on disk.
	var stored []string
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT scopes FROM meta_threads.connections WHERE workspace_id = $1`,
		e.wsA).Scan(&stored); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if len(stored) != len(mtdomain.ReadScopes) {
		t.Fatalf("the column holds %v, want %v", stored, mtdomain.RequestedScopes())
	}
}

// The consequence that matters: the three capabilities gated on a scope
// must actually RUN after a connect. This is the test that would have
// caught the bug — it exercises the gate, not the column.
func TestTheScopeGatedToolsRunAfterAConnect(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	// post.insights and profile.insights need threads_manage_insights;
	// post.search needs threads_keyword_search. Before the fix all three
	// refused with scope_missing on a perfectly good credential.
	if out := e.execute(t, e.wsA, mttools.PostInsightsTool,
		map[string]any{"post_id": "p_005"}); out["metrics"] == nil {
		t.Errorf("post.insights returned no metrics: %+v", out)
	}
	if out := e.execute(t, e.wsA, mttools.ProfileInsightsTool,
		map[string]any{}); out["views"] == nil {
		t.Errorf("profile.insights returned no views: %+v", out)
	}
	if out := e.execute(t, e.wsA, mttools.PostSearchTool,
		map[string]any{"query": "AI agents"}); out["posts"] == nil {
		t.Errorf("post.search returned no posts key: %+v", out)
	}
}

// THE RATCHET.
//
// A fake that models a `scope` field must not be able to change what this
// integration believes. The guarantee is structural rather than asserted:
// there is no Scopes field on ports.TokenGrant and no `scope` key in the
// client's response struct, so the value below is decoded into nothing.
//
// If somebody reintroduces that path, this test fails — the connection
// would take Meta's word (here, a deliberately wrong single scope) instead
// of the list the flow requested.
func TestAScopeFieldInTheTokenResponseCannotInfluenceStoredScopes(t *testing.T) {
	e := newEnv(t)

	// Make the fake lie in the most damaging way available: claim only the
	// basic scope, which is what a naive implementation would then store.
	e.meta.tokenResponseExtra = map[string]any{
		"scope":          "threads_basic",
		"permissions":    []string{"threads_basic"},
		"granted_scopes": "threads_basic",
	}

	conn := e.connect(e.wsA)

	for _, want := range mtdomain.ReadScopes {
		if !conn.HasScope(want) {
			t.Fatalf("a `scope` field in the token response suppressed %q — "+
				"the response shape is deciding capabilities again", want)
		}
	}
}

// The authorization URL and the stored scopes come from ONE list. Two
// copies would drift the first time somebody added a permission to the
// request and forgot the persistence, which is a subtler version of the
// same bug.
func TestTheRequestedAndStoredScopesAreTheSameList(t *testing.T) {
	e := newEnv(t)
	raw, err := e.mt.AuthorizationURL("state")
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	conn := e.connect(e.wsA)

	for _, s := range conn.Scopes {
		if !strings.Contains(raw, string(s)) {
			t.Errorf("scope %q was stored but never requested in the authorization URL", s)
		}
	}
	for _, s := range mtdomain.RequestedScopes() {
		if !conn.HasScope(mtdomain.Scope(s)) {
			t.Errorf("scope %q was requested but not stored", s)
		}
	}
	// And still no write permission, on either side.
	for _, forbidden := range []string{
		"threads_content_publish", "threads_manage_replies", "threads_delete",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the authorization URL requests %q", forbidden)
		}
		if conn.HasScope(mtdomain.Scope(forbidden)) {
			t.Errorf("the connection stored %q", forbidden)
		}
	}
}
