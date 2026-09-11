package domain

import (
	"strings"
	"testing"
	"time"
)

/* ── scopes ──────────────────────────────────────────────────────────── */

// THE read-only guarantee, at the layer that decides what can be asked for.
//
// This integration can never obtain a write permission, because there is no
// constant naming one and RequestedScopes is built from ReadScopes alone. A
// future change that wants publishing has to ADD a value here, which is a
// visible, reviewable act — not a flag flipped somewhere else.
func TestNoWriteScopeCanEverBeRequested(t *testing.T) {
	forbidden := []string{
		"threads_content_publish", "threads_manage_replies", "threads_delete",
		"threads_location_tagging", "threads_share_to_instagram",
	}
	requested := strings.Join(RequestedScopes(), ",")
	for _, w := range forbidden {
		if strings.Contains(requested, w) {
			t.Errorf("the authorization request carries the write scope %q: %s", w, requested)
		}
		if Scope(w).Valid() {
			t.Errorf("%q validates as a scope this integration knows", w)
		}
	}
}

// threads_basic is not optional: Meta refuses every endpoint without it.
func TestBasicIsAlwaysRequested(t *testing.T) {
	var found bool
	for _, s := range RequestedScopes() {
		if s == string(ScopeBasic) {
			found = true
		}
	}
	if !found {
		t.Fatalf("threads_basic is not in %v", RequestedScopes())
	}
}

// The scope string is stable and ordered, so two identical connect flows
// produce an identical authorization URL.
func TestRequestedScopesAreStable(t *testing.T) {
	a, b := RequestedScopes(), RequestedScopes()
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("scope order is not stable: %v vs %v", a, b)
	}
}

/* ── the token lifecycle ─────────────────────────────────────────────── */

func conn(created time.Time, expires time.Time) *Connection {
	return &Connection{CreatedAt: created, TokenExpiresAt: expires}
}

func TestExpiryIsReadFromTheStoredInstant(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	c := conn(now.Add(-48*time.Hour), now.Add(time.Hour))
	if c.Expired(now) {
		t.Error("a token with an hour left reported expired")
	}
	if !c.Expired(now.Add(2 * time.Hour)) {
		t.Error("a token past its expiry reported live")
	}
	// The boundary belongs to the dead side: a token expiring exactly now is
	// one Meta will refuse, and reporting it live would spend a round trip
	// to discover that.
	if !c.Expired(c.TokenExpiresAt) {
		t.Error("a token at the exact instant of expiry reported live")
	}
}

// Meta refuses a refresh before 24 hours. Checking it here means the
// operator is told the rule rather than shown Meta's wording for it.
func TestRefreshableFollowsMetasTwentyFourHourRule(t *testing.T) {
	created := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	c := conn(created, created.Add(60*24*time.Hour))

	if c.Refreshable(created.Add(time.Hour)) {
		t.Error("a one-hour-old token reported refreshable")
	}
	if c.Refreshable(created.Add(23*time.Hour + 59*time.Minute)) {
		t.Error("a token just under 24 hours old reported refreshable")
	}
	if !c.Refreshable(created.Add(24 * time.Hour)) {
		t.Error("a token exactly 24 hours old reported not refreshable")
	}
	// And an expired one is never refreshable, however old.
	dead := conn(created, created.Add(time.Hour))
	if dead.Refreshable(created.Add(48 * time.Hour)) {
		t.Error("an expired token reported refreshable")
	}
}

/* ── scope checks ────────────────────────────────────────────────────── */

func TestHasScopeIsExact(t *testing.T) {
	c := &Connection{Scopes: []Scope{ScopeBasic, ScopeInsights}}
	if !c.HasScope(ScopeInsights) {
		t.Error("a granted scope reported missing")
	}
	if c.HasScope(ScopeKeywordSearch) {
		t.Error("a scope that was never granted reported present")
	}
}

/* ── the token hint ──────────────────────────────────────────────────── */

// The hint has to identify a credential without being one. Four characters
// of a token that is hundreds long is a fingerprint; anything more starts
// being a secret.
func TestTokenHintRevealsOnlyTheTail(t *testing.T) {
	const token = "THQVJXbG9uZ2xpdmVkdG9rZW5leGFtcGxlYTNmOQ"
	hint := TokenHintOf(token)

	if !strings.HasSuffix(hint, token[len(token)-4:]) {
		t.Fatalf("hint = %q, want it to end in the token's last four", hint)
	}
	if len([]rune(hint)) > 5 {
		t.Errorf("hint = %q is longer than a fingerprint", hint)
	}
	if strings.Contains(token, hint[1:]) && len(hint) > 5 {
		t.Errorf("hint = %q carries too much of the token", hint)
	}
}

// A short token yields no characters at all rather than most of a secret.
func TestAShortTokenLeaksNothing(t *testing.T) {
	for _, short := range []string{"", "a", "abcd"} {
		if got := TokenHintOf(short); got != "…" {
			t.Errorf("TokenHintOf(%q) = %q, want a hint with no characters of it", short, got)
		}
	}
}

/* ── the error vocabulary ────────────────────────────────────────────── */

// Not-connected has to be its own kind. Every surface branches on it, and
// collapsing it into not-found would make "connect an account" look like
// "that post does not exist".
func TestNotConnectedIsItsOwnKind(t *testing.T) {
	if NotConnected().Kind == NotFound("x").Kind {
		t.Fatal("not-connected and not-found are the same kind")
	}
}

// A scope refusal names the scope, so the operator knows what to allow on
// the reconnect instead of guessing.
func TestScopeMissingNamesTheScope(t *testing.T) {
	err := ScopeMissing(ScopeInsights)
	if !strings.Contains(err.Message, string(ScopeInsights)) {
		t.Fatalf("the refusal does not name the scope: %s", err.Message)
	}
}
