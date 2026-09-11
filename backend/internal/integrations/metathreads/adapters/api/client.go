// Package api is the HTTP client for the Meta Threads Graph API.
//
// It is the only place in this repository that knows Meta's dialect: the
// `data`/`paging` envelope, the comma-separated `fields` parameter, the
// metric rows shaped as {name, values:[{value}]}, and the error body shaped
// as {"error":{"message","type","code"}}. Everything above the ports
// contract is free of it.
//
// ── Read verbs only ────────────────────────────────────────────────────
// There is no POST anywhere in this file except the two token exchanges,
// which are GETs in Meta's design anyway. No publish, no reply, no delete.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/corsi/backend/internal/integrations/metathreads/domain"
	"github.com/corsi/backend/internal/integrations/metathreads/ports"
)

// DefaultBaseURL is Meta's documented host for the Threads API.
const DefaultBaseURL = "https://graph.threads.net"

// AuthorizationHost is where a person is sent to approve the connection.
// Deliberately a different host from the API: it is a browser destination,
// not an endpoint.
const AuthorizationHost = "https://threads.net"

// requestTimeout bounds one call to Meta.
//
// Twenty seconds. A tool call is inside a turn the user is watching, and a
// turn that hangs on a slow upstream is worse than one that reports the
// upstream was slow.
const requestTimeout = 20 * time.Second

// maxResponseBytes bounds what we will read from Meta.
//
// A response larger than this is not a listing, it is a problem, and
// decoding it would spend memory on the strength of a promise the upstream
// never made.
const maxResponseBytes = 4 << 20

type Client struct {
	http *http.Client
	log  *slog.Logger
}

func New(log *slog.Logger) *Client {
	return &Client{http: &http.Client{Timeout: requestTimeout}, log: log}
}

/* ── the wire ────────────────────────────────────────────────────────── */

// metaError is Meta's error envelope.
type metaError struct {
	Error struct {
		Message   string `json:"message"`
		Type      string `json:"type"`
		Code      int    `json:"code"`
		Subcode   int    `json:"error_subcode"`
		UserTitle string `json:"error_user_title"`
		UserMsg   string `json:"error_user_msg"`
	} `json:"error"`
}

// do performs one GET and decodes into out.
//
// ── Why a failure is never an empty result ─────────────────────────────
// Every non-2xx becomes a typed domain error carrying Meta's own message.
// Returning an empty page instead would be the single most damaging bug
// this file could have: a model asked "have I written about X" would be
// told "no posts" when the truth was "the token expired", and would go on
// to confidently advise the user about a gap that does not exist.
func (c *Client) do(ctx context.Context, rawURL string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return domain.Upstream("could not build a request to Meta Threads: %v", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return domain.Upstream("Meta Threads did not answer within %s", requestTimeout)
		}
		// The URL is deliberately not in the message: it carries the access
		// token as a query parameter, and an error string travels into logs
		// and into a tool result.
		return domain.Upstream("could not reach Meta Threads")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return domain.Upstream("could not read the Meta Threads response")
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.failure(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return domain.Upstream("Meta Threads returned a body this build could not read")
	}
	return nil
}

// failure turns Meta's error body into a typed refusal.
func (c *Client) failure(status int, body []byte) error {
	var me metaError
	_ = json.Unmarshal(body, &me)
	msg := strings.TrimSpace(me.Error.Message)
	if msg == "" {
		msg = fmt.Sprintf("Meta Threads answered %d", status)
	}
	switch {
	case status == http.StatusUnauthorized || me.Error.Code == 190:
		// 190 is Meta's "access token invalid/expired" family.
		return domain.TokenExpired()
	case status == http.StatusForbidden:
		return &domain.Error{Kind: domain.KindScopeMissing, Message: msg}
	case status == http.StatusNotFound:
		return domain.NotFound("%s", msg)
	default:
		return domain.Upstream("%s", msg)
	}
}

// endpoint builds a URL under the credential's base, with the token
// attached as a query parameter — which is how Meta's documentation
// authenticates these calls.
func endpoint(base, path string, q url.Values) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		b = DefaultBaseURL
	}
	u := b + "/v1.0/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

/* ── the token lifecycle ─────────────────────────────────────────────── */

// AuthorizationURL builds the page a person is sent to.
//
// ── Why the scopes are not a parameter ─────────────────────────────────
// They come from domain.RequestedScopes() and nowhere else. A caller that
// could choose them could ask for `threads_content_publish`, and this
// sprint's read-only promise would then depend on every call site
// remembering. Here it depends on there being no way to say it.
func AuthorizationURL(appID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", appID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(domain.RequestedScopes(), ","))
	if state != "" {
		q.Set("state", state)
	}
	return AuthorizationHost + "/oauth/authorize?" + q.Encode()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	// The code exchange reports this; the long-lived exchanges do not.
	//
	// There is no `scope` field here on purpose — see ports.TokenGrant.
	// Meta does not return one, and modelling it would let a response shape
	// decide what this integration believes a credential may do.
	UserID any `json:"user_id"`
}

func (t tokenResponse) grant() ports.TokenGrant {
	g := ports.TokenGrant{
		Token:     t.AccessToken,
		ExpiresIn: time.Duration(t.ExpiresIn) * time.Second,
	}
	// Meta sends `user_id` as a JSON NUMBER, and a Threads id is 17 digits —
	// past the 2^53 an IEEE double holds exactly, so decoding through `any`
	// mangles the last digits.
	//
	// Nothing depends on this value: the account identity this integration
	// stores comes from `GET /me`, which returns it as a string. It is
	// normalised here for completeness, and must NOT become the id anything
	// matches on — the platform callbacks match against AccountID for
	// exactly this reason.
	switch v := t.UserID.(type) {
	case string:
		g.UserID = v
	case float64:
		g.UserID = strconv.FormatInt(int64(v), 10)
	}
	return g
}

// ExchangeCode redeems an authorization code for a short-lived token.
//
// ── Why this one is a POST and the others are GETs ─────────────────────
// Because Meta documents it that way. The code and the app secret are both
// in the body rather than in a URL, which keeps them out of any proxy's
// access log — and the two long-lived calls carry only a token Meta already
// issued.
func (c *Client) ExchangeCode(ctx context.Context, in ports.ExchangeInput) (ports.TokenGrant, error) {
	base := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	form := url.Values{}
	form.Set("client_id", in.AppID)
	form.Set("client_secret", in.AppSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", in.RedirectURI)
	form.Set("code", in.Code)

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return ports.TokenGrant{}, domain.Upstream("could not build the token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return ports.TokenGrant{}, domain.Upstream("could not reach Meta Threads to exchange the code")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ports.TokenGrant{}, c.failure(resp.StatusCode, body)
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return ports.TokenGrant{}, domain.Upstream("Meta Threads returned no access token")
	}
	return tr.grant(), nil
}

func (c *Client) ExchangeLongLived(ctx context.Context, baseURL, appSecret, shortLived string) (ports.TokenGrant, error) {
	q := url.Values{}
	q.Set("grant_type", "th_exchange_token")
	q.Set("client_secret", appSecret)
	q.Set("access_token", shortLived)
	return c.token(ctx, baseURL, "/access_token", q)
}

func (c *Client) RefreshLongLived(ctx context.Context, baseURL, longLived string) (ports.TokenGrant, error) {
	q := url.Values{}
	q.Set("grant_type", "th_refresh_token")
	q.Set("access_token", longLived)
	return c.token(ctx, baseURL, "/refresh_access_token", q)
}

func (c *Client) token(ctx context.Context, baseURL, path string, q url.Values) (ports.TokenGrant, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	var tr tokenResponse
	if err := c.do(ctx, base+path+"?"+q.Encode(), &tr); err != nil {
		return ports.TokenGrant{}, err
	}
	if tr.AccessToken == "" {
		return ports.TokenGrant{}, domain.Upstream("Meta Threads returned no access token")
	}
	return tr.grant(), nil
}
