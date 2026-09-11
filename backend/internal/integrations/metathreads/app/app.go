// Package app is the Meta Threads integration's application layer.
//
// It owns the credential lifecycle — build an authorization URL, redeem a
// code, hold a sealed token, refresh it, throw it away — and it is the one
// place that turns a stored connection into the Credentials a read needs.
//
// ── What it does NOT own ───────────────────────────────────────────────
// Meaning. It does not decide which post performed well, what a theme is,
// or whether the operator has covered a topic. Those are questions a model
// answers from evidence, and building a classifier here would be this layer
// inventing a fact the API never reported. See docs and the tools package.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/metathreads/adapters/api"
	"github.com/corsi/backend/internal/integrations/metathreads/domain"
	"github.com/corsi/backend/internal/integrations/metathreads/ports"
	"github.com/corsi/backend/internal/platform/secrets"
)

// AppConfig is the deployment's Meta app.
//
// ── Why this is configuration and not a stored row ─────────────────────
// Because it identifies THIS DEPLOYMENT to Meta, not a workspace to us. The
// app id and secret are the same for every workspace on the installation,
// they come from the Meta developer console, and they are rotated by a
// person editing an environment — exactly like the LiteLLM key's SECRETS_KEY
// and unlike a user's token.
type AppConfig struct {
	AppID       string
	AppSecret   string
	RedirectURI string
	// BaseURL is overridable so a test can point the entire OAuth dance at a
	// local server. Empty means Meta's documented host.
	BaseURL string
}

func (c AppConfig) Configured() bool {
	return strings.TrimSpace(c.AppID) != "" &&
		strings.TrimSpace(c.AppSecret) != "" &&
		strings.TrimSpace(c.RedirectURI) != ""
}

type Service struct {
	connections ports.ConnectionRepo
	api         ports.API
	sealer      *secrets.Sealer
	cfg         AppConfig
	log         *slog.Logger
	now         func() time.Time
}

func NewService(c ports.ConnectionRepo, a ports.API, sealer *secrets.Sealer, cfg AppConfig, log *slog.Logger) *Service {
	return &Service{connections: c, api: a, sealer: sealer, cfg: cfg, log: log, now: time.Now}
}

// WithClock returns a copy that reads time from fn. Used by tests.
func (s *Service) WithClock(fn func() time.Time) *Service {
	cp := *s
	cp.now = fn
	return &cp
}

// Configured reports whether this deployment can begin an OAuth flow.
func (s *Service) Configured() bool { return s.cfg.Configured() }

/* ── the connection lifecycle ────────────────────────────────────────── */

// AuthorizationURL is where a person is sent to approve the connection.
//
// The scopes are not a parameter here or anywhere below: they come from
// domain.RequestedScopes(), which contains no write permission. See the note
// on domain.ReadScopes.
func (s *Service) AuthorizationURL(state string) (string, error) {
	if !s.cfg.Configured() {
		return "", domain.NotConfigured()
	}
	return api.AuthorizationURL(s.cfg.AppID, s.cfg.RedirectURI, state), nil
}

// ConnectInput is an authorization code coming back from Meta.
type ConnectInput struct {
	WorkspaceID uuid.UUID
	Code        string
	// RedirectURI must match the one the authorization used. Accepted from
	// the caller because Meta compares it, and a mismatch is a specific,
	// explainable failure rather than a mysterious one.
	RedirectURI string
}

// Connect redeems a code and stores the resulting long-lived credential.
//
// ── The order of operations, and why ───────────────────────────────────
//  1. redeem the code               → a 1-hour token
//  2. exchange for a long-lived one → a 60-day token
//  3. read the profile with it      → proves the credential works AND
//     tells us who it belongs to
//  4. seal and store
//
// Step 3 is before step 4 on purpose. Storing first would leave a row
// claiming a connection whose token might already be refused, and the
// operator would discover it the next time an agent tried to use it — in
// the middle of a conversation, as a tool failure.
func (s *Service) Connect(ctx context.Context, in ConnectInput) (*domain.Connection, error) {
	if !s.cfg.Configured() {
		return nil, domain.NotConfigured()
	}
	if !s.sealer.Enabled() {
		// The same contract the chat provider credential has: no key, no
		// storage, and an honest refusal rather than a plaintext token.
		return nil, domain.Invalid("SECRETS_KEY is not configured, so no credential can be stored")
	}
	if strings.TrimSpace(in.Code) == "" {
		return nil, domain.Invalid("an authorization code is required")
	}
	redirect := strings.TrimSpace(in.RedirectURI)
	if redirect == "" {
		redirect = s.cfg.RedirectURI
	}

	short, err := s.api.ExchangeCode(ctx, ports.ExchangeInput{
		Code: in.Code, RedirectURI: redirect,
		AppID: s.cfg.AppID, AppSecret: s.cfg.AppSecret, BaseURL: s.cfg.BaseURL,
	})
	if err != nil {
		return nil, err
	}

	long, err := s.api.ExchangeLongLived(ctx, s.cfg.BaseURL, s.cfg.AppSecret, short.Token)
	if err != nil {
		return nil, err
	}
	// ── What the stored scopes are, and what they are NOT ──────────────
	// They are the permissions THIS FLOW ASKED FOR — domain.ReadScopes, the
	// same list AuthorizationURL puts in front of the user — and nothing
	// else. There is exactly one list, here and in the authorization URL,
	// so the two cannot drift.
	//
	// They are NOT a record of what Meta granted. Meta's token endpoints
	// report no permissions at all and the Threads API publishes no
	// equivalent of the Graph API's /me/permissions, so a granted-scope
	// record is not obtainable. Believing otherwise is what broke this
	// before: the field was filled from a `scope` field that does not
	// exist, read empty on every real connection, and the local gate then
	// refused capabilities the credential actually had.
	//
	// ── Why an approximation is safe here ──────────────────────────────
	// Because it is only ever used to SKIP a round trip, never to permit
	// one. The authority on what a token may do is Meta: a call for a
	// permission the user declined comes back 403, which this integration
	// already reports as KindScopeMissing with Meta's own wording. So the
	// worst case of an optimistic record is one wasted request and an
	// accurate error — where the worst case of a pessimistic one, which is
	// what shipped, was a working credential reported as broken.
	scopes := domain.ReadScopes

	creds := ports.Credentials{BaseURL: s.baseURL(), Token: long.Token}
	profile, err := s.api.Profile(ctx, creds)
	if err != nil {
		return nil, err
	}

	cipher, err := s.sealer.Seal(long.Token)
	if err != nil {
		return nil, domain.Invalid("the credential could not be sealed: %v", err)
	}

	now := s.now().UTC()
	conn := &domain.Connection{
		WorkspaceID:       in.WorkspaceID,
		TokenCipher:       cipher,
		TokenHint:         domain.TokenHintOf(long.Token),
		TokenExpiresAt:    now.Add(long.ExpiresIn),
		Scopes:            scopes,
		AccountID:         profile.ID,
		Username:          profile.Username,
		DisplayName:       profile.Name,
		ProfilePictureURL: profile.ProfilePictureURL,
		APIBaseURL:        s.baseURL(),
	}
	if err := s.connections.Upsert(ctx, conn); err != nil {
		return nil, err
	}
	if err := s.connections.TouchVerified(ctx, in.WorkspaceID, conn.ID, now); err != nil {
		return nil, err
	}
	conn.LastVerifiedAt = &now
	return conn, nil
}

func (s *Service) baseURL() string {
	if b := strings.TrimSpace(s.cfg.BaseURL); b != "" {
		return b
	}
	return api.DefaultBaseURL
}

// Status returns the workspace's connection, or domain.NotConnected.
func (s *Service) Status(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, error) {
	return s.connections.FindByWorkspace(ctx, workspaceID)
}

// Refresh extends the stored long-lived token.
//
// Meta refuses a token younger than 24 hours and one already expired, and
// both refusals are checked here rather than discovered upstream — so the
// operator is told "not yet, and here is when" instead of receiving Meta's
// wording for a rule it never explained.
func (s *Service) Refresh(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, error) {
	conn, err := s.connections.FindByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if conn.Expired(now) {
		return nil, domain.TokenExpired()
	}
	if !conn.Refreshable(now) {
		return nil, domain.Invalid(
			"a Meta Threads token can only be refreshed once it is 24 hours old; this one may be refreshed after %s",
			conn.CreatedAt.Add(domain.MinTokenAgeBeforeRefresh).UTC().Format(time.RFC3339))
	}

	token, err := s.open(conn)
	if err != nil {
		return nil, err
	}
	grant, err := s.api.RefreshLongLived(ctx, s.baseURL(), token)
	if err != nil {
		return nil, err
	}
	cipher, err := s.sealer.Seal(grant.Token)
	if err != nil {
		return nil, domain.Invalid("the refreshed credential could not be sealed: %v", err)
	}
	expires := now.Add(grant.ExpiresIn)
	if err := s.connections.ReplaceToken(ctx, workspaceID, conn.ID, cipher,
		domain.TokenHintOf(grant.Token), expires); err != nil {
		return nil, err
	}
	conn.TokenCipher = cipher
	conn.TokenHint = domain.TokenHintOf(grant.Token)
	conn.TokenExpiresAt = expires
	return conn, nil
}

func (s *Service) Disconnect(ctx context.Context, workspaceID uuid.UUID) error {
	return s.connections.Disconnect(ctx, workspaceID)
}

/* ── resolving a call ────────────────────────────────────────────────── */

// open decrypts the stored token. The plaintext never leaves this package
// except as a field of Credentials handed to the client.
func (s *Service) open(c *domain.Connection) (string, error) {
	if !s.sealer.Enabled() {
		return "", domain.Invalid("SECRETS_KEY is not configured, so the stored credential cannot be read")
	}
	token, err := s.sealer.Open(c.TokenCipher)
	if err != nil {
		return "", domain.Invalid("the stored Meta Threads credential could not be read; reconnect the account")
	}
	return token, nil
}

// resolve is the single gate every read passes.
//
// ── Why all four checks live here ──────────────────────────────────────
// Connected, unexpired, scoped, decryptable. Every read needs all four, and
// a check that lived in the tools would be a check each new tool has to
// remember. `need` is the scope that specific call requires; the empty
// scope means "basic only", which every connection has by construction
// because Meta will not issue a grant without it.
func (s *Service) resolve(ctx context.Context, workspaceID uuid.UUID, need domain.Scope) (*domain.Connection, ports.Credentials, error) {
	conn, err := s.connections.FindByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, ports.Credentials{}, err
	}
	if conn.Expired(s.now().UTC()) {
		return nil, ports.Credentials{}, domain.TokenExpired()
	}
	if need != "" && !conn.HasScope(need) {
		return nil, ports.Credentials{}, domain.ScopeMissing(need)
	}
	token, err := s.open(conn)
	if err != nil {
		return nil, ports.Credentials{}, err
	}
	base := conn.APIBaseURL
	if base == "" {
		base = s.baseURL()
	}
	return conn, ports.Credentials{BaseURL: base, Token: token}, nil
}

/* ── the reads ───────────────────────────────────────────────────────── */

func (s *Service) Profile(ctx context.Context, workspaceID uuid.UUID) (ports.Profile, error) {
	_, creds, err := s.resolve(ctx, workspaceID, "")
	if err != nil {
		return ports.Profile{}, err
	}
	return s.api.Profile(ctx, creds)
}

func (s *Service) ListPosts(ctx context.Context, workspaceID uuid.UUID, q ports.PostQuery) (ports.PostPage, error) {
	_, creds, err := s.resolve(ctx, workspaceID, "")
	if err != nil {
		return ports.PostPage{}, err
	}
	return s.api.ListPosts(ctx, creds, q)
}

func (s *Service) GetPost(ctx context.Context, workspaceID uuid.UUID, id string) (ports.Post, error) {
	_, creds, err := s.resolve(ctx, workspaceID, "")
	if err != nil {
		return ports.Post{}, err
	}
	return s.api.GetPost(ctx, creds, id)
}

func (s *Service) PostInsights(ctx context.Context, workspaceID uuid.UUID, id string) (ports.PostInsights, error) {
	_, creds, err := s.resolve(ctx, workspaceID, domain.ScopeInsights)
	if err != nil {
		return ports.PostInsights{}, err
	}
	return s.api.PostInsights(ctx, creds, id)
}

func (s *Service) AccountInsights(ctx context.Context, workspaceID uuid.UUID, q ports.AccountInsightsQuery) (ports.AccountInsights, error) {
	conn, creds, err := s.resolve(ctx, workspaceID, domain.ScopeInsights)
	if err != nil {
		return ports.AccountInsights{}, err
	}
	return s.api.AccountInsights(ctx, creds, conn.AccountID, q)
}

func (s *Service) SearchPosts(ctx context.Context, workspaceID uuid.UUID, q ports.SearchQuery) (ports.PostPage, error) {
	_, creds, err := s.resolve(ctx, workspaceID, domain.ScopeKeywordSearch)
	if err != nil {
		return ports.PostPage{}, err
	}
	return s.api.SearchPosts(ctx, creds, q)
}

/* ── Meta platform callbacks ─────────────────────────────────────────── */

// PlatformCallbackResult is what one verified callback did.
//
// Removed is how many live connections were destroyed. Zero is a normal,
// successful outcome: a user who never linked this deployment, or who
// disconnected already, produces a valid callback with nothing to do. It is
// reported rather than swallowed so an operator can tell "we had nothing"
// from "we failed to look".
type PlatformCallbackResult struct {
	Removed int64
	// ConfirmationCode identifies this request back to the user, and is
	// what the data-deletion contract requires be returned to Meta.
	ConfirmationCode string
}

// Deauthorize handles Meta's deauthorization callback.
//
// ── What Meta sends, and what it means ─────────────────────────────────
// Meta documents this as a ping "whenever" a user uninstalls the app
// without interacting with it — the token we hold is already dead at that
// point. Keeping the row would leave a connection the interface reports as
// healthy and every read refuses, which is the worst of both.
//
// ── Why the removal is safe ────────────────────────────────────────────
// The request is authenticated by HMAC against the app secret before this
// is called, and the id it names is matched EXACTLY against a stored
// account. Nothing here trusts a header, a workspace or a caller-supplied
// predicate.
func (s *Service) Deauthorize(ctx context.Context, signedRequest string) (PlatformCallbackResult, error) {
	payload, err := domain.ParseSignedRequest(signedRequest, s.cfg.AppSecret)
	if err != nil {
		return PlatformCallbackResult{}, err
	}
	removed, err := s.connections.DisconnectByAccountID(ctx, payload.UserID)
	if err != nil {
		return PlatformCallbackResult{}, err
	}
	// The app-scoped id is deliberately NOT logged: it identifies a person
	// to Meta, and a count answers every operational question this line is
	// here to answer.
	s.log.Info("meta threads deauthorization callback", "connections_removed", removed)
	return PlatformCallbackResult{Removed: removed}, nil
}

// DeleteUserData handles Meta's data deletion request callback.
//
// ── What there is to delete ────────────────────────────────────────────
// One thing: the sealed credential. This integration stores no posts, no
// metrics and no profile beyond the handful of fields on the connection
// row — every read is fresh from Meta and nothing is written down (see the
// note at the top of ports.go). So a deletion request is completely
// satisfied by destroying the connection, and this returns the truth rather
// than scheduling work that does not exist.
//
// The internal C.O.R.S.I. Threads pipeline is NOT touched, and must not be:
// those are the operator's own drafts, authored by them in our product.
// They are not Meta's data and a Meta callback has no standing over them.
func (s *Service) DeleteUserData(ctx context.Context, signedRequest string) (PlatformCallbackResult, error) {
	payload, err := domain.ParseSignedRequest(signedRequest, s.cfg.AppSecret)
	if err != nil {
		return PlatformCallbackResult{}, err
	}
	removed, err := s.connections.DisconnectByAccountID(ctx, payload.UserID)
	if err != nil {
		return PlatformCallbackResult{}, err
	}
	code := confirmationCode(payload.UserID, s.now().UTC())
	s.log.Info("meta threads data deletion callback",
		"connections_removed", removed, "confirmation_code", code)
	return PlatformCallbackResult{Removed: removed, ConfirmationCode: code}, nil
}

// confirmationCode is the handle Meta shows the user for their request.
//
// ── Why it is derived and not stored ───────────────────────────────────
// Because the deletion is SYNCHRONOUS and total: by the time this is
// computed there is nothing left to delete and no job to track. A codes
// table would exist only to let a status page repeat what is already true
// of every request, and would itself be a new record about a person who
// just asked to be forgotten.
//
// It is a hash rather than the raw id so the code can be shown, logged and
// pasted into a support message without carrying the app-scoped user id.
func confirmationCode(userID string, at time.Time) string {
	sum := sha256.Sum256([]byte(userID + "|" + at.Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])[:16]
}
