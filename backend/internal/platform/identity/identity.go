// Package identity authenticates the single operator this product has.
//
// ── The boundary, stated once ──────────────────────────────────────────
//
//	identity  answers WHO is calling. One subject, or nobody.
//	workspace answers WHICH tenant the call is scoped to.
//
// They are different questions and this package does not answer the second
// one. `X-Workspace-Id` keeps doing exactly what it always did — it resolves
// tenancy — and stops being the only thing between the internet and the
// data, because nothing reaches the workspace middleware without a session.
// Promoting the header to an authentication token was never an option: the
// middleware accepts any syntactically valid UUID, and the frontend sends a
// public constant.
//
// ── Default-deny, and why it is a root middleware ──────────────────────
//
// Middleware is registered on the ROOT router before any module mounts, so
// every route in the product is refused without a session unless it appears
// in PublicPaths. A module added six months from now is protected because
// nobody had to remember anything — which is the opposite of the reverse
// proxy this deployment used to run, where the public path list enumerated
// module prefixes and had already fallen behind by two modules.
//
// ── What this package deliberately does not have ───────────────────────
//
// No signup, no password reset, no OAuth, no second user, no roles. There
// is one subject, named by AUTH_EMAIL, and `Session` carries no claim a
// caller could branch on. Authorization in this product is per agent and
// per capability, and it has no subject.
package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config is the operator's credential and the session policy.
type Config struct {
	// Email is the one authorized subject.
	Email string `env:"AUTH_EMAIL"`
	// PasswordHash is an argon2id PHC string. The password itself is never
	// configuration — see password.go.
	PasswordHash string `env:"AUTH_PASSWORD_HASH"`
	// SessionTTL is the absolute lifetime of a login. Absolute rather than
	// idle: a sliding window means a stolen cookie renews itself forever,
	// and re-typing a password once a week is the entire cost of not having
	// that property.
	SessionTTL time.Duration `env:"AUTH_SESSION_TTL" envDefault:"168h"`
	// CookieInsecure drops the Secure attribute and the __Host- prefix so a
	// session survives plain http://localhost during development.
	//
	// It defaults to FALSE, which is the production answer, so a deployment
	// gets the safe behaviour by configuring nothing. Setting it in
	// production would let the session cookie travel in clear text.
	CookieInsecure bool `env:"AUTH_COOKIE_INSECURE" envDefault:"false"`
}

// Cookie names. The `__Host-` prefix is enforced by the BROWSER: it refuses
// the cookie unless it is Secure, Path=/ and has no Domain attribute, which
// makes it impossible for a sibling subdomain to set a session for us.
// There is no such guarantee over plain http, so the dev name is plain too
// rather than a prefix the browser would silently reject.
const (
	CookieNameSecure   = "__Host-corsi_session"
	CookieNameInsecure = "corsi_session"
)

// Service is the identity capability.
type Service struct {
	cfg      Config
	verifier verifier
	store    Store
	log      *slog.Logger
	limiter  *rateLimiter
	now      func() time.Time
}

// New validates the configuration and builds the service.
//
// ── Why a missing credential fails the boot ────────────────────────────
// There is no "auth disabled" mode and no env var that turns it off. A
// switch would be the exact shape of the defect this deployment just paid
// for with Telegram: a value absent from the orchestrator's stored spec,
// the process coming up healthy, and the missing thing discovered later by
// its consequences. An unauthenticated C.O.R.S.I. on a public hostname is a
// worse version of that, so a deployment with no credential refuses to
// serve rather than serving everyone.
func New(cfg Config, store Store, log *slog.Logger, now func() time.Time) (*Service, error) {
	if strings.TrimSpace(cfg.Email) == "" {
		return nil, errors.New("identity: AUTH_EMAIL is required")
	}
	if strings.TrimSpace(cfg.PasswordHash) == "" {
		return nil, errors.New("identity: AUTH_PASSWORD_HASH is required")
	}
	v, err := parseVerifier(cfg.PasswordHash)
	if err != nil {
		return nil, err
	}
	if cfg.SessionTTL <= 0 {
		return nil, errors.New("identity: AUTH_SESSION_TTL must be positive")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		cfg:      cfg,
		verifier: v,
		store:    store,
		log:      log,
		limiter:  newRateLimiter(now),
		now:      now,
	}, nil
}

func (s *Service) cookieName() string {
	if s.cfg.CookieInsecure {
		return CookieNameInsecure
	}
	return CookieNameSecure
}

/* ── authentication ──────────────────────────────────────────────────── */

// errInvalidCredentials is the ONLY failure a caller is told about.
//
// A wrong email, a wrong password and an email that is not the operator's
// are the same answer with the same status and the same wording. There is
// one account and nothing useful to enumerate, but the property is free to
// keep and expensive to reintroduce later.
var errInvalidCredentials = errors.New("invalid credentials")

// authenticate checks a credential pair in time that does not depend on
// which half was wrong.
//
// The argon2 verification runs even when the email does not match. Skipping
// it would make a wrong email return in microseconds and a wrong password
// in ~90ms — a timing oracle that tells an attacker when they have found
// the right address, which is the one fact this product would rather not
// confirm.
func (s *Service) authenticate(email, password string) error {
	emailOK := subtle.ConstantTimeCompare(
		[]byte(strings.ToLower(strings.TrimSpace(email))),
		[]byte(strings.ToLower(strings.TrimSpace(s.cfg.Email))),
	) == 1
	passwordOK := s.verifier.matches(password)
	if !emailOK || !passwordOK {
		return errInvalidCredentials
	}
	return nil
}

// issue creates a session and returns the cookie that carries it.
func (s *Service) issue(ctx context.Context) (*http.Cookie, Session, error) {
	token, digest, err := newSessionToken()
	if err != nil {
		return nil, Session{}, err
	}
	now := s.now()
	sess := Session{Subject: s.cfg.Email, IssuedAt: now, ExpiresAt: now.Add(s.cfg.SessionTTL)}
	if err := s.store.Create(ctx, digest, sess); err != nil {
		return nil, Session{}, err
	}
	return &http.Cookie{
		Name:     s.cookieName(),
		Value:    token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   !s.cfg.CookieInsecure,
		// Lax, not Strict. Strict would also refuse the cookie on a
		// top-level navigation that arrives from anywhere else — a
		// bookmark sync, a link in a note — which reads to the operator as
		// a session that randomly logged itself out. Lax still withholds
		// the cookie from every cross-site POST, which is the CSRF
		// property that matters for a state-changing API.
		SameSite: http.SameSiteLaxMode,
	}, sess, nil
}

// clearCookie is the cookie that deletes the session cookie. Its attributes
// must match the one that was set or the browser keeps the original.
func (s *Service) clearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   !s.cfg.CookieInsecure,
		SameSite: http.SameSiteLaxMode,
	}
}

/* ── request context ─────────────────────────────────────────────────── */

type ctxKey struct{}

// FromContext returns the session stamped by Middleware.
func FromContext(ctx context.Context) (Session, bool) {
	v, ok := ctx.Value(ctxKey{}).(Session)
	return v, ok
}

// WithSession seeds a session into a context, for tests that exercise a
// handler without going through the middleware.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// resolve reads the cookie and returns the live session behind it.
func (s *Service) resolve(r *http.Request) (Session, bool) {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" {
		return Session{}, false
	}
	digest, ok := digestOf(c.Value)
	if !ok {
		return Session{}, false
	}
	sess, found, err := s.store.Lookup(r.Context(), digest, s.now())
	if err != nil {
		// A store that cannot answer must not be read as "authenticated".
		// Logged, because a database failure that presents as a logout is
		// otherwise indistinguishable from an expiry.
		s.log.Error("identity: session lookup failed", "err", err)
		return Session{}, false
	}
	return sess, found
}
