// Package domain holds the GitHub integration's own entities and rules.
//
// ── What "domain" means for an integration ─────────────────────────────
// Not business rules — an integration has none, and the architecture says
// so. What lives here is the small amount of state the integration owns
// because nobody else can: which account is linked, which repositories the
// operator allowed, and the vocabulary for saying no. Everything about what
// a commit *means* belongs to whoever asked for it.
//
// The normalized contract — the shapes this integration hands back — lives
// in ports.go, because a contract belongs to the boundary and not to the
// adapter that happens to produce it.
package domain

import (
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

/* ── the connection ──────────────────────────────────────────────────── */

// AuthKind is how the token on a connection was obtained.
//
// One value today. The type exists so that the day a second one ships, the
// places that must branch are a compile error rather than a search.
type AuthKind string

// AuthPAT is a personal access token the operator pasted.
//
// ── Why this is v1 and not OAuth ───────────────────────────────────────
// Everything downstream of the token is identical across
// auth kinds, because the REST API takes `Authorization: Bearer <token>`
// either way. So the part that OAuth would replace is the acquisition flow
// and this one constant — not the repository model, not the tools, not the
// API, not the UI. A fine-grained PAT also arrives already scoped by GitHub
// to selected repositories and read-only permissions, which means the
// operator's first line of defence exists before ours does.
const AuthPAT AuthKind = "pat"

func (k AuthKind) Valid() bool { return k == AuthPAT }

// AccountType distinguishes a personal account from an organization.
type AccountType string

const (
	AccountUser AccountType = "User"
	AccountOrg  AccountType = "Organization"
)

func (t AccountType) Valid() bool { return t == AccountUser || t == AccountOrg }

// Connection is one linked GitHub account, scoped to a workspace.
//
// TokenCipher is the sealed credential and is json:"-": it must never reach
// a response body, a log line, or an audit row. TokenHint is the
// display-safe remnant that lets the interface confirm which token is
// stored. The plaintext exists in exactly two places — the request that is
// setting it, and the HTTP client that is spending it — and is never a
// field on this struct.
type Connection struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	AuthKind    AuthKind  `json:"auth_kind"`

	TokenCipher []byte `json:"-"`
	TokenHint   string `json:"token_hint"`

	AccountLogin     string      `json:"account_login"`
	AccountID        int64       `json:"account_id"`
	AccountType      AccountType `json:"account_type"`
	AccountName      string      `json:"account_name,omitempty"`
	AccountAvatarURL string      `json:"account_avatar_url,omitempty"`

	APIBaseURL string `json:"-"`

	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

func (c *Connection) Validate() error {
	if c.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if !c.AuthKind.Valid() {
		return Invalid("auth_kind must be pat")
	}
	if len(c.TokenCipher) == 0 {
		return Invalid("token required")
	}
	if l := strings.TrimSpace(c.AccountLogin); l == "" || len(l) > 100 {
		return Invalid("account_login must be 1..100 chars")
	}
	if c.AccountID == 0 {
		return Invalid("account_id required")
	}
	if !c.AccountType.Valid() {
		return Invalid("account_type must be User or Organization")
	}
	return ValidateAPIBaseURL(c.APIBaseURL)
}

// DefaultAPIBaseURL is github.com's REST root. A connection stores its own
// so that a GitHub Enterprise host is a row rather than a redeploy — and so
// that a test can point one connection at a fake server without a global
// switch that production would also read.
const DefaultAPIBaseURL = "https://api.github.com"

// ValidateAPIBaseURL refuses anything that is not an absolute http(s) URL.
//
// The scheme check is not cosmetic. This value becomes the prefix of every
// request that carries the token, so a stored `file://` or a bare host
// would be a credential pointed somewhere nobody chose.
func ValidateAPIBaseURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 500 {
		return Invalid("api_base_url must be 1..500 chars")
	}
	u, err := url.Parse(s)
	if err != nil {
		return Invalid("api_base_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Invalid("api_base_url must start with http:// or https://")
	}
	if u.Host == "" {
		return Invalid("api_base_url must include a host")
	}
	return nil
}

// NormalizeAPIBaseURL trims trailing slashes so paths can be appended
// without producing a double slash.
func NormalizeAPIBaseURL(raw string) string {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	if s == "" {
		return DefaultAPIBaseURL
	}
	return s
}

/* ── the token ───────────────────────────────────────────────────────── */

// MaxTokenLength bounds what may be submitted as a credential.
//
// GitHub's tokens are around 40 (classic) to 93 (fine-grained) characters.
// Five hundred is far above any real one and small enough that a pasted
// file is refused before it is sealed and stored.
const MaxTokenLength = 500

// ValidateToken checks the shape of a submitted credential without ever
// putting it in an error message.
//
// ── Why the message never quotes the value ─────────────────────────────
// Because error messages get logged, and a validation error that echoes
// the invalid input is the most common way a secret ends up in a log file.
// The rule here has no exception: nothing in this package formats a token
// into a string for any purpose.
func ValidateToken(raw string) error {
	t := strings.TrimSpace(raw)
	if t == "" {
		return Invalid("token required")
	}
	if len(t) > MaxTokenLength {
		return Invalid("token is longer than the maximum a GitHub credential can be")
	}
	// A token travels in an HTTP header. A control character in one would
	// either be rejected by net/http or, worse, split the header — so it is
	// refused here rather than at the socket.
	for i := 0; i < len(t); i++ {
		if t[i] < 0x21 || t[i] > 0x7e {
			return Invalid("token contains characters a GitHub credential never has")
		}
	}
	return nil
}

/* ── the authorized repository ───────────────────────────────────────── */

// Repository is one repository the operator authorized for use inside
// C.O.R.S.I.
//
// ── Presence is the authorization ──────────────────────────────────────
// There is no `enabled` field and no `revoked_at`. A row means allowed; no
// row means denied. That is the same rule chat.agent_tools follows, and it
// is what makes revocation a delete rather than a state machine two layers
// have to agree about.
//
// ── Why Owner and Name are separate from FullName ──────────────────────
// Because they are used for different things, and conflating them is the
// bug this whole design is arranged to prevent. Owner and Name are what
// build a request path. FullName is only ever a LOOKUP KEY: a string a
// model produced, matched against stored rows, and then discarded. Nothing
// is ever built from a string that came from outside.
type Repository struct {
	ID            uuid.UUID `json:"id"`
	WorkspaceID   uuid.UUID `json:"workspace_id"`
	ConnectionID  uuid.UUID `json:"connection_id"`
	GitHubID      int64     `json:"github_id"`
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Private       bool      `json:"private"`
	DefaultBranch string    `json:"default_branch"`
	HTMLURL       string    `json:"html_url,omitempty"`
	Description   string    `json:"description,omitempty"`
	OwnerType     string    `json:"owner_type"`
	AuthorizedAt  time.Time `json:"authorized_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (r *Repository) Validate() error {
	if r.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if r.ConnectionID == uuid.Nil {
		return Invalid("connection_id required")
	}
	if r.GitHubID == 0 {
		return Invalid("github_id required")
	}
	if !ValidOwner(r.Owner) {
		return Invalid("owner is not a valid GitHub account name")
	}
	if !ValidRepoName(r.Name) {
		return Invalid("name is not a valid GitHub repository name")
	}
	if r.FullName != r.Owner+"/"+r.Name {
		return Invalid("full_name must be owner/name")
	}
	return nil
}

// ValidOwner and ValidRepoName encode GitHub's own naming rules.
//
// ── Why validate at all, when the values come from GitHub ──────────────
// Because they do not always. A repository row is written from a GitHub
// response, but it is written in response to a request that named which
// repositories to authorize, and the day somebody adds a second write path
// this is the check that is already there. Bounded, anchored patterns also
// mean no stored owner or name can ever contain a slash, a dot-dot, or a
// query separator — which is what makes path construction from these two
// columns safe by construction rather than by review.
func ValidOwner(s string) bool {
	if len(s) == 0 || len(s) > 39 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || (c == '-' && i > 0)
		if !ok {
			return false
		}
	}
	return true
}

func ValidRepoName(s string) bool {
	if len(s) == 0 || len(s) > 100 || s == "." || s == ".." {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// NormalizeFullName lowercases and trims a caller-supplied "owner/name".
//
// GitHub treats these case-insensitively and a model will not reproduce
// capitalisation reliably, so the lookup is case-folded. The function does
// NOT decide whether the value is legitimate — that is the repository
// lookup's job, and the distinction matters: this only prepares a key.
func NormalizeFullName(raw string) string {
	return strings.ToLower(strings.TrimSpace(strings.Trim(raw, "/")))
}

// ValidFullNameShape is a cheap pre-filter for a caller-supplied key.
//
// It exists so that an obviously impossible string — a path traversal
// attempt, a URL, a sentence — is refused before it costs a database round
// trip. It is NOT a security boundary: the boundary is that a key which
// matches no row is denied, and that requests are built from stored columns
// rather than from the key. This just makes the common refusal cheap and
// its message specific.
func ValidFullNameShape(raw string) bool {
	s := strings.TrimSpace(raw)
	owner, name, found := strings.Cut(s, "/")
	return found && ValidOwner(owner) && ValidRepoName(name)
}
