// Package ports declares the Meta Threads integration's boundaries: the
// normalized contract it produces, and the storage it requires.
//
// ── Why the contract lives here and not in the client ──────────────────
// Because the contract belongs to the boundary, not to the adapter that
// satisfies it. The types below carry no Meta dialect a consumer would
// otherwise have to learn: no `data`/`paging` envelope, no comma-separated
// `fields` parameter, no metric rows shaped as `{name, values:[{value}]}`.
// Swapping the adapter changes nothing above this line.
//
// ── What is deliberately absent ────────────────────────────────────────
// Every write. There is no Publish, no Reply, no Delete, no Repost, no
// Quote — not commented out, not behind a flag, not a struct with an unused
// field. Meta's API supports all of them and this integration does not: the
// honest way to ship read-only is for the verbs not to exist, so no later
// change can reach one by accident.
//
// ── Why no post is ever stored ─────────────────────────────────────────
// There is no PostRepo in this file and there must not be one. A published
// post belongs to Meta and its metrics move: views and likes on a
// three-day-old post are different this afternoon. A local copy would be a
// snapshot that starts lying immediately, and worse, it would blur the line
// this integration exists to hold — external evidence read fresh, versus
// C.O.R.S.I. Threads, the internal state we own and write.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/metathreads/domain"
)

/* ── storage ─────────────────────────────────────────────────────────── */

// ConnectionRepo stores the linked account. Every method takes a workspace
// id and filters on it server-side; soft-deleted rows are never returned.
type ConnectionRepo interface {
	// Upsert writes the workspace's single connection, replacing whatever
	// was there. Connecting a second account is replacing the first, which
	// is the only reading the one-per-workspace index permits.
	Upsert(ctx context.Context, c *domain.Connection) error
	// FindByWorkspace returns the live connection, or domain.NotConnected —
	// a typed refusal rather than a nil, because "no Meta Threads here" is
	// an answer the whole stack branches on.
	FindByWorkspace(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, error)
	// TouchVerified stamps last_verified_at after a round trip proved the
	// credential still works.
	TouchVerified(ctx context.Context, workspaceID, id uuid.UUID, at time.Time) error
	// ReplaceToken writes a refreshed credential and its new expiry, leaving
	// the account identity alone. Separate from Upsert because a refresh is
	// not a reconnection: nothing about who is linked changed.
	ReplaceToken(ctx context.Context, workspaceID, id uuid.UUID, cipher []byte, hint string, expiresAt time.Time) error
	// Disconnect soft-deletes the connection AND overwrites the sealed
	// token, because a credential nobody can reach is still a credential.
	Disconnect(ctx context.Context, workspaceID uuid.UUID) error
	// DisconnectByAccountID does the same for every live connection holding
	// one Meta account, and reports how many it removed.
	//
	// ── Why this is not a workspace-scoped call ────────────────────────
	// Because the caller is Meta, not a person, and a platform callback
	// names an APP-SCOPED USER — never a workspace of ours. A deauthorized
	// or deletion-requesting user is asking about themselves everywhere
	// they are linked, and answering for only one workspace would leave a
	// live credential for an account that revoked it.
	//
	// ── Why this is still narrow ───────────────────────────────────────
	// The id is an EXACT match, and it arrives inside an HMAC signature
	// that only Meta and this deployment can produce — see
	// domain.ParseSignedRequest. There is no pattern, no prefix and no
	// caller-chosen predicate: an account with no row removes nothing and
	// reports zero.
	DisconnectByAccountID(ctx context.Context, accountID string) (int64, error)
}

/* ── the normalized contract ─────────────────────────────────────────── */

// Credentials are the resolved, decrypted access details for one call.
//
// Passed per request rather than held on the client, so one client instance
// serves every workspace without a cache keyed by something a refactor
// could get wrong. Same choice as the LLM adapter and the GitHub client.
type Credentials struct {
	BaseURL string
	Token   string
}

// Profile is the connected account, as Meta reports it.
//
// The fields are exactly what `GET /me` documents for this permission set:
// id, username, name, threads_profile_picture_url, threads_biography,
// is_verified. Nothing is derived and nothing is invented.
type Profile struct {
	ID                string `json:"id"`
	Username          string `json:"username"`
	Name              string `json:"name,omitempty"`
	Biography         string `json:"biography,omitempty"`
	ProfilePictureURL string `json:"profile_picture_url,omitempty"`
	IsVerified        bool   `json:"is_verified"`
}

// Post is one published Threads post, normalized.
//
// ── Why this subset of Meta's fields ───────────────────────────────────
// Meta documents well over twenty fields on a media object, including
// several about rendering (thumbnail_url, alt_text, gif_url,
// is_spoiler_media) and several about post mechanics that only matter to a
// client that draws them. What survives here is what a reasoning step needs
// to answer a question about a body of work: when it went out, what it
// said, what kind of thing it was, whether it was original, and where to
// find it.
type Post struct {
	ID   string    `json:"id"`
	Text string    `json:"text,omitempty"`
	Type string    `json:"media_type,omitempty"`
	Link string    `json:"permalink,omitempty"`
	At   time.Time `json:"timestamp,omitzero"`
	// Username is whose post this is. Present because keyword search can
	// return other people's posts once the app holds advanced access, and a
	// result set that mixed authors without saying so would let a model
	// report a stranger's post as the operator's own.
	Username string `json:"username,omitempty"`
	// IsReply and IsQuote separate original posting from conversation. "What
	// do I actually publish" and "what do I reply to" are different
	// questions, and a list that merged them answers neither.
	IsReply bool `json:"is_reply"`
	IsQuote bool `json:"is_quote_post"`
	// HasReplies is Meta's own flag. Not a count — the count is an insight.
	HasReplies bool `json:"has_replies"`
	// TopicTag is Meta's topic tag when the post carries one.
	TopicTag string `json:"topic_tag,omitempty"`
}

// PostPage is one page of posts plus the cursor that continues it.
//
// ── Why a cursor and not an offset ─────────────────────────────────────
// Because Meta paginates by cursor, and translating to offsets would mean
// this layer inventing a stable ordering the upstream does not promise. The
// cursor is opaque here and is passed straight back.
type PostPage struct {
	Posts []Post `json:"posts"`
	// NextCursor is empty when Meta reported no further page.
	NextCursor string `json:"next_cursor,omitempty"`
}

// PostQuery bounds a listing of the connected account's own posts.
type PostQuery struct {
	// Limit is how many posts to return. The adapter clamps it.
	Limit int
	// After continues a previous page. Opaque.
	After string
	// Since and Until bound the window. Zero means unbounded on that side.
	Since time.Time
	Until time.Time
}

// SearchQuery is a keyword or topic-tag search.
//
// ── What this can and cannot reach ─────────────────────────────────────
// The corpus is decided by Meta, not by this struct. Without app-review
// approval for `threads_keyword_search` the search covers ONLY the
// authenticated user's own posts; with approval it covers public posts.
// Nothing in the response distinguishes the two, so no field here pretends
// to. See the tool description, which tells the model plainly.
type SearchQuery struct {
	Query string
	// Mode is KEYWORD or TAG, as Meta spells them.
	Mode string
	// Kind is TOP or RECENT. TOP is Meta's own relevance ordering, which is
	// not exposed as a score and is not recomputed here.
	Kind string
	// AuthorUsername filters to one author, exact match.
	AuthorUsername string
	Limit          int
	After          string
	Since          time.Time
	Until          time.Time
}

// PostInsights is the metric set Meta reports for one post.
//
// ── Why every metric is a pointer ──────────────────────────────────────
// Because "Meta did not report this metric for this post" and "this post
// has zero" are different facts, and collapsing them is how a report starts
// lying. Meta documents several cases where a metric is simply absent: a
// repost facade returns an empty array, and views and shares are in
// development. A model handed `views: 0` cannot tell that it was never
// measured; a model handed nothing at all can say so.
//
// There is deliberately NO computed engagement score here. Adding likes to
// replies is an interpretation, and interpretation is the agent's job — see
// the note in tools.go.
type PostInsights struct {
	Views   *int64 `json:"views,omitempty"`
	Likes   *int64 `json:"likes,omitempty"`
	Replies *int64 `json:"replies,omitempty"`
	Reposts *int64 `json:"reposts,omitempty"`
	Quotes  *int64 `json:"quotes,omitempty"`
	Shares  *int64 `json:"shares,omitempty"`
	// Unavailable names the metrics Meta did not return, so the absence is
	// reportable rather than merely invisible.
	Unavailable []string `json:"unavailable,omitempty"`
}

// AccountInsights is the metric set Meta reports for the whole account.
type AccountInsights struct {
	Views          *int64   `json:"views,omitempty"`
	Likes          *int64   `json:"likes,omitempty"`
	Replies        *int64   `json:"replies,omitempty"`
	Reposts        *int64   `json:"reposts,omitempty"`
	Quotes         *int64   `json:"quotes,omitempty"`
	Clicks         *int64   `json:"clicks,omitempty"`
	FollowersCount *int64   `json:"followers_count,omitempty"`
	Unavailable    []string `json:"unavailable,omitempty"`
}

// AccountInsightsQuery bounds an account-level read.
//
// Meta refuses `since` before 2024-04-13 and does not accept a window at
// all for followers_count. The adapter enforces the first and omits the
// window for the second rather than letting Meta refuse the whole call.
type AccountInsightsQuery struct {
	Since time.Time
	Until time.Time
}

/* ── the OAuth grant ─────────────────────────────────────────────────── */

// TokenGrant is what a token endpoint returned.
//
// ExpiresIn is carried as the duration Meta reported rather than as an
// absolute time, because the absolute time is a fact about when WE received
// it and belongs to the layer holding the clock.
type TokenGrant struct {
	Token     string
	ExpiresIn time.Duration
	// UserID is the app-scoped user id, reported by the code exchange.
	UserID string

	// ── There is deliberately NO Scopes field ──────────────────────────
	// Meta's token endpoints do not report permissions. The documented
	// response of graph.threads.net/oauth/access_token is exactly two
	// fields — `access_token` and `user_id` — and the long-lived exchange
	// adds only `token_type` and `expires_in`.
	//
	// An earlier version of this struct had one, filled from a `scope`
	// field that does not exist. It read empty on every real connection
	// and the local scope gate then refused insights and search for a
	// credential that had them. The test fake had invented the field, so
	// the suite was green over fiction.
	//
	// The field is gone rather than left unfilled: with no field, no fake
	// and no future response shape can influence what this integration
	// believes a credential may do.
}

// ExchangeInput is an authorization code being redeemed.
type ExchangeInput struct {
	Code        string
	RedirectURI string
	AppID       string
	AppSecret   string
	BaseURL     string
}

/* ── the external system ─────────────────────────────────────────────── */

// API is everything this integration does against Meta.
//
// Read verbs and the token lifecycle, and nothing else. An implementation
// lives in adapters/api; a fake one lives in the test suite, which is how
// the whole stack — tools, service, repository, Postgres — is exercised
// without a Meta app.
type API interface {
	// ExchangeCode redeems an authorization code for a SHORT-LIVED token.
	ExchangeCode(ctx context.Context, in ExchangeInput) (TokenGrant, error)
	// ExchangeLongLived turns a short-lived token into a 60-day one.
	ExchangeLongLived(ctx context.Context, baseURL, appSecret, shortLived string) (TokenGrant, error)
	// RefreshLongLived extends a long-lived token that is at least 24 hours
	// old and not yet expired.
	RefreshLongLived(ctx context.Context, baseURL, longLived string) (TokenGrant, error)

	Profile(ctx context.Context, c Credentials) (Profile, error)
	ListPosts(ctx context.Context, c Credentials, q PostQuery) (PostPage, error)
	GetPost(ctx context.Context, c Credentials, id string) (Post, error)
	PostInsights(ctx context.Context, c Credentials, id string) (PostInsights, error)
	AccountInsights(ctx context.Context, c Credentials, userID string, q AccountInsightsQuery) (AccountInsights, error)
	SearchPosts(ctx context.Context, c Credentials, q SearchQuery) (PostPage, error)
}
