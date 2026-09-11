// Package domain holds the Meta Threads integration's own entities and
// rules.
//
// ══════════════════════════════════════════════════════════════════════
//
//	Meta Threads is NOT C.O.R.S.I. Threads
//
// ══════════════════════════════════════════════════════════════════════
//
//	internal/threads          C.O.R.S.I. Threads. OUR bounded context.
//	                          Ideas and drafts we own and write.
//	                          Tools: threads.thread.*
//
//	this package              Meta's social network. THEIR system.
//	                          Posts the operator already published, and
//	                          the metrics Meta reports about them.
//	                          Tools: meta_threads.*
//
// This package stores no content. The only row it owns is a CREDENTIAL.
// Everything about a post is fetched from Meta at the moment it is asked
// for, and is never written down — see the note on why in ports.go.
//
// ── What "domain" means for an integration ─────────────────────────────
// Not business rules — an integration has none, and the architecture says
// so. What lives here is the small amount of state only this integration
// can own: which Meta Threads account is linked, what its token may do,
// and when that token dies. Everything about what a post MEANS belongs to
// whoever asked for it, which in this build is a model reasoning in a
// conversation.
package domain

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

/* ── scopes ──────────────────────────────────────────────────────────── */

// Scope is a Meta Threads OAuth permission, spelled exactly as Meta spells
// it. These strings travel in an authorization URL and come back in a token
// grant, so they are identifiers and not labels.
type Scope string

const (
	// ScopeBasic is required for every call to every Threads API endpoint.
	// Without it there is no connection at all.
	ScopeBasic Scope = "threads_basic"
	// ScopeInsights is required for GET on both insights endpoints —
	// per-post and per-account.
	ScopeInsights Scope = "threads_manage_insights"
	// ScopeKeywordSearch is required for GET /keyword_search.
	//
	// ── The subtlety that decides this whole sprint ────────────────────
	// Holding this scope does NOT mean search covers Meta Threads. Meta's
	// documentation is explicit: without APP REVIEW approval for this
	// permission "the search will be performed only on posts owned by the
	// authenticated user", and after approval "public posts will be
	// searchable". Same endpoint, same scope, two different corpora, and
	// nothing in the response says which one answered.
	//
	// So this constant buys the operator's own archive — searchable by
	// keyword, which is what "já falei sobre X?" needs — and global
	// discovery stays gated behind a review this codebase cannot perform.
	// The tool says so in its own description rather than letting a model
	// infer completeness from an empty result.
	ScopeKeywordSearch Scope = "threads_keyword_search"
)

// ReadScopes is exactly what this integration ever requests.
//
// ── Why the write scopes are not here, and not commented out ───────────
// `threads_content_publish`, `threads_manage_replies` and `threads_delete`
// are real permissions this integration could ask for and does not. This
// sprint is INTELLIGENCE, not publishing.
//
// A scope that exists in the code as a disabled constant is one a later
// change can enable in a single character. The honest way to ship
// read-only is for the value not to exist — the same rule the GitHub
// integration follows for its write verbs — and RequestedScopes below is
// the only list any authorization URL can be built from.
var ReadScopes = []Scope{ScopeBasic, ScopeInsights, ScopeKeywordSearch}

func (s Scope) String() string { return string(s) }

// Valid reports whether s is a scope this integration knows and requests.
// A scope Meta invents tomorrow is not valid here until somebody decides
// it should be.
func (s Scope) Valid() bool {
	for _, known := range ReadScopes {
		if s == known {
			return true
		}
	}
	return false
}

// RequestedScopes is the scope parameter of an authorization URL, in a
// stable order so two identical requests produce an identical URL.
func RequestedScopes() []string {
	out := make([]string, len(ReadScopes))
	for i, s := range ReadScopes {
		out[i] = string(s)
	}
	sort.Strings(out)
	return out
}

/* ── the connection ──────────────────────────────────────────────────── */

// Connection is one linked Meta Threads account, scoped to a workspace.
//
// TokenCipher is the sealed credential and is json:"-": it must never reach
// a response body, a log line, or an audit row. TokenHint is the
// display-safe remnant that lets a person confirm which token is stored.
// The plaintext exists in exactly two places — the exchange that obtained
// it, and the HTTP client that spends it — and is never a field here.
type Connection struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`

	TokenCipher []byte `json:"-"`
	TokenHint   string `json:"token_hint"`
	// TokenExpiresAt is what the token endpoint reported, stamped at
	// exchange. Never recomputed.
	TokenExpiresAt time.Time `json:"token_expires_at"`

	// Scopes are what the grant came back with. They say what the
	// CREDENTIAL can do, which is a different question from what an agent
	// is allowed to ask for — that one is a grant in chat.agent_tools.
	Scopes []Scope `json:"scopes"`

	AccountID         string `json:"account_id"`
	Username          string `json:"username"`
	DisplayName       string `json:"display_name,omitempty"`
	ProfilePictureURL string `json:"profile_picture_url,omitempty"`

	APIBaseURL string `json:"-"`

	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// HasScope reports whether the stored credential carries one.
func (c *Connection) HasScope(s Scope) bool {
	for _, held := range c.Scopes {
		if held == s {
			return true
		}
	}
	return false
}

// Expired reports whether the token is past its declared lifetime.
//
// Compared against a clock the caller supplies rather than time.Now(), so
// the rule is testable without waiting sixty days.
func (c *Connection) Expired(now time.Time) bool {
	return !now.Before(c.TokenExpiresAt)
}

// Refreshable reports whether Meta would accept a refresh right now.
//
// Two conditions, and both are Meta's: a long-lived token must be at least
// 24 hours old, and it must not have expired. A product that refreshed
// eagerly on every page load would be refused for the first day of every
// connection's life and would have no way to explain why.
func (c *Connection) Refreshable(now time.Time) bool {
	if c.Expired(now) {
		return false
	}
	return !now.Before(c.CreatedAt.Add(MinTokenAgeBeforeRefresh))
}

const (
	// MinTokenAgeBeforeRefresh is Meta's rule: a long-lived token must be
	// "at least 24 hours old but not expired" to be refreshed.
	MinTokenAgeBeforeRefresh = 24 * time.Hour
	// LongLivedTokenLifetime is what Meta documents, kept here so a caller
	// can sanity-check an `expires_in` that arrives absurd. It is NOT used
	// to compute the expiry — that comes from the response.
	LongLivedTokenLifetime = 60 * 24 * time.Hour
)

// TokenHintOf reduces a token to the display-safe remnant.
//
// Last four characters, prefixed. Enough to tell two credentials apart on
// screen and useless to anybody who obtains it. A token shorter than the
// window yields a hint with no characters of it at all rather than most of
// a short secret.
func TokenHintOf(token string) string {
	t := strings.TrimSpace(token)
	const window = 4
	if len([]rune(t)) <= window {
		return "…"
	}
	r := []rune(t)
	return "…" + string(r[len(r)-window:])
}
