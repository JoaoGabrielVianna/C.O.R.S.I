package identity

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

/* ── a store that lives in memory, so these tests need no database ──── */

type memStore struct {
	mu       sync.Mutex
	rows     map[string]Session
	failNext bool
}

func newMemStore() *memStore { return &memStore{rows: map[string]Session{}} }

func key(h []byte) string { return string(h) }

func (m *memStore) Create(_ context.Context, h []byte, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key(h)] = s
	return nil
}

func (m *memStore) Lookup(_ context.Context, h []byte, now time.Time) (Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return Session{}, false, io.ErrUnexpectedEOF
	}
	s, ok := m.rows[key(h)]
	if !ok || !s.ExpiresAt.After(now) {
		return Session{}, false, nil
	}
	return s, true, nil
}

func (m *memStore) Delete(_ context.Context, h []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, key(h))
	return nil
}

func (m *memStore) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, s := range m.rows {
		if !s.ExpiresAt.After(now) {
			delete(m.rows, k)
			n++
		}
	}
	return n, nil
}

/* ── harness ─────────────────────────────────────────────────────────── */

const (
	testEmail    = "operator@example.com"
	testPassword = "correct-horse-battery-staple"
)

func newTestService(t *testing.T) (*Service, *memStore) {
	t.Helper()
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := newMemStore()
	svc, err := New(Config{
		Email:        testEmail,
		PasswordHash: hash,
		SessionTTL:   time.Hour,
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, store
}

// router composes the middleware exactly as the composition root does: the
// gate on the ROOT, before anything is mounted.
func router(svc *Service) *chi.Mux {
	r := chi.NewRouter()
	r.Use(svc.Middleware())
	svc.Register(r)
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r.Get("/finance/summary", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return r
}

func login(t *testing.T, r http.Handler, email, password string) *http.Response {
	t.Helper()
	body := strings.NewReader(`{"email":"` + email + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	req.RemoteAddr = "203.0.113.7:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Result()
}

func sessionCookie(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == CookieNameSecure || c.Name == CookieNameInsecure {
			return c
		}
	}
	t.Fatalf("no session cookie on response")
	return nil
}

func errCode(t *testing.T, res *http.Response) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	return body.Error.Code
}

/* ── the gate ────────────────────────────────────────────────────────── */

// TestEveryRouteIsDeniedByDefault is the assertion the whole capability
// exists for. A route nobody thought about is refused, which is the
// property the reverse-proxy allow-list this replaced could not have.
func TestEveryRouteIsDeniedByDefault(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)

	for _, path := range []string{
		"/finance/summary",
		"/auth/session",
		"/chat/agents",     // never registered: still 401, not 404
		"/anything/at/all", // ditto
		// Path traversal through a public prefix. Cleaned before the
		// allow-list is consulted, so this is the private route it
		// addresses and not the public one it starts with.
		"/metrics/../finance/summary",
		"/health/../../finance/summary",
		"//metrics/../finance/summary",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s anonymous = %d, want 401", path, w.Code)
		}
	}
}

func TestPublicPathsAnswerWithoutASession(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /health/live anonymous = %d, want 200", w.Code)
	}
}

// TestIsPublicMatchesOnSegments pins the one way an allow-list like this
// goes wrong: a prefix that swallows a sibling whose name merely starts
// with the same letters.
func TestIsPublicMatchesOnSegments(t *testing.T) {
	cases := map[string]bool{
		"/health":            true,
		"/health/":           true,
		"/health/ready":      true,
		"/metrics":           true,
		"/auth/login":        true,
		"/auth/logout":       true,
		"/auth/session":      false,
		"/healthcheck":       false, // NOT a child of /health
		"/metricsss":         false,
		"/auth/login-bypass": false,
		"/finance/summary":   false,
		"/":                  false,
	}
	for path, want := range cases {
		if got := IsPublic(path); got != want {
			t.Errorf("IsPublic(%q) = %v, want %v", path, got, want)
		}
	}
}

/* ── login ───────────────────────────────────────────────────────────── */

func TestCorrectCredentialAuthenticatesAndCookieIsHardened(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)

	res := login(t, r, testEmail, testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", res.StatusCode)
	}
	c := sessionCookie(t, res)

	if c.Name != CookieNameSecure {
		t.Errorf("cookie name = %q, want the __Host- prefixed name", c.Name)
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly — script could read the session")
	}
	if !c.Secure {
		t.Error("cookie is not Secure — session would travel in clear text")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("cookie Path = %q, want / (required by the __Host- prefix)", c.Path)
	}
	if c.Domain != "" {
		t.Errorf("cookie Domain = %q, want empty (required by the __Host- prefix)", c.Domain)
	}
	// The cookie must not be the thing stored. A dump of the store holding
	// the bearer token verbatim is the defect the digest exists to prevent.
	if strings.Contains(c.Value, " ") || len(c.Value) < 40 {
		t.Errorf("cookie value looks wrong: %q", c.Value)
	}
}

func TestWrongPasswordAndWrongEmailAreIndistinguishable(t *testing.T) {
	svc, _ := newTestService(t)

	for _, tc := range []struct{ name, email, password string }{
		{"wrong password", testEmail, "not-the-password"},
		{"wrong email", "someone@else.com", testPassword},
		{"both wrong", "someone@else.com", "not-the-password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh service per case so the rate limiter never turns a
			// 401 into a 429 and hides the property under test.
			svc, _ = newTestService(t)
			res := login(t, router(svc), tc.email, tc.password)
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", res.StatusCode)
			}
			if code := errCode(t, res); code != "invalid_credentials" {
				t.Errorf("code = %q, want invalid_credentials", code)
			}
			for _, c := range res.Cookies() {
				if c.Name == CookieNameSecure && c.Value != "" {
					t.Error("a failed login issued a session cookie")
				}
			}
		})
	}
}

func TestEmailComparisonIgnoresCaseAndSurroundingSpace(t *testing.T) {
	svc, _ := newTestService(t)
	res := login(t, router(svc), "  "+strings.ToUpper(testEmail)+"  ", testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an address is not case sensitive", res.StatusCode)
	}
}

func TestRateLimitRefusesAfterRepeatedFailures(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)

	for i := 0; i < maxAttempts; i++ {
		if res := login(t, r, testEmail, "wrong"); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, res.StatusCode)
		}
	}
	res := login(t, r, testEmail, "wrong")
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("attempt %d = %d, want 429", maxAttempts+1, res.StatusCode)
	}
	if code := errCode(t, res); code != "too_many_attempts" {
		t.Errorf("code = %q, want too_many_attempts", code)
	}
	// And the correct password does not get through the lockout either: a
	// limiter that exempts the right answer is a limiter that confirms the
	// right answer.
	if res := login(t, r, testEmail, testPassword); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("correct credential during lockout = %d, want 429", res.StatusCode)
	}
}

/* ── session lifecycle ───────────────────────────────────────────────── */

func authedGet(t *testing.T, r http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSessionGrantsAccessAndSurvivesRepeatedRequests(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)
	c := sessionCookie(t, login(t, r, testEmail, testPassword))

	// Twice, because "refresh keeps the session" is the property and a
	// single-use token would pass a one-shot assertion.
	for i := 0; i < 2; i++ {
		if w := authedGet(t, r, "/finance/summary", c); w.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i+1, w.Code)
		}
	}
	if w := authedGet(t, r, "/auth/session", c); w.Code != http.StatusOK {
		t.Fatalf("/auth/session = %d, want 200", w.Code)
	}
}

func TestLogoutRevokesServerSideNotJustTheCookie(t *testing.T) {
	svc, store := newTestService(t)
	r := router(svc)
	c := sessionCookie(t, login(t, r, testEmail, testPassword))

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", w.Code)
	}

	cleared := false
	for _, sc := range w.Result().Cookies() {
		if sc.Name == CookieNameSecure && sc.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout did not expire the cookie")
	}

	// The row is gone, so a COPY of the cookie held anywhere else is dead
	// too. This is the property a stateless token could not have given.
	if len(store.rows) != 0 {
		t.Errorf("store still holds %d session(s) after logout", len(store.rows))
	}
	if w := authedGet(t, r, "/finance/summary", c); w.Code != http.StatusUnauthorized {
		t.Errorf("replaying the logged-out cookie = %d, want 401", w.Code)
	}
}

func TestLogoutIsIdempotentWithoutASession(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("anonymous logout = %d, want 204", w.Code)
	}
}

func TestExpiredSessionIsRefused(t *testing.T) {
	hash, _ := HashPassword(testPassword)
	store := newMemStore()
	now := time.Now()
	clock := func() time.Time { return now }
	svc, err := New(Config{Email: testEmail, PasswordHash: hash, SessionTTL: time.Hour},
		store, slog.New(slog.NewTextHandler(io.Discard, nil)), clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := router(svc)
	c := sessionCookie(t, login(t, r, testEmail, testPassword))

	if w := authedGet(t, r, "/finance/summary", c); w.Code != http.StatusOK {
		t.Fatalf("fresh session = %d, want 200", w.Code)
	}
	now = now.Add(time.Hour + time.Second)
	w := authedGet(t, r, "/finance/summary", c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session = %d, want 401", w.Code)
	}
	if code := errCode(t, w.Result()); code != "unauthenticated" {
		t.Errorf("code = %q, want unauthenticated", code)
	}
}

func TestForgedAndMalformedCookiesAreRefused(t *testing.T) {
	svc, _ := newTestService(t)
	r := router(svc)

	for _, value := range []string{
		"",
		"not-base64!!",
		"c2hvcnQ",               // valid base64url, wrong length
		strings.Repeat("A", 43), // right length, never issued
	} {
		c := &http.Cookie{Name: CookieNameSecure, Value: value}
		if w := authedGet(t, r, "/finance/summary", c); w.Code != http.StatusUnauthorized {
			t.Errorf("cookie %q = %d, want 401", value, w.Code)
		}
	}
}

// TestStoreFailureDeniesRatherThanAdmits pins the direction a broken
// database fails in. Reading an error as "authenticated" would turn an
// outage into an open door.
func TestStoreFailureDeniesRatherThanAdmits(t *testing.T) {
	svc, store := newTestService(t)
	r := router(svc)
	c := sessionCookie(t, login(t, r, testEmail, testPassword))

	store.failNext = true
	if w := authedGet(t, r, "/finance/summary", c); w.Code != http.StatusUnauthorized {
		t.Errorf("store failure = %d, want 401", w.Code)
	}
}

/* ── configuration ───────────────────────────────────────────────────── */

// TestMissingCredentialFailsConstruction is the fail-closed assertion. A
// deployment that lost AUTH_* must refuse to serve, not serve everyone.
func TestMissingCredentialFailsConstruction(t *testing.T) {
	hash, _ := HashPassword(testPassword)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"no email", Config{PasswordHash: hash, SessionTTL: time.Hour}},
		{"no hash", Config{Email: testEmail, SessionTTL: time.Hour}},
		{"blank email", Config{Email: "   ", PasswordHash: hash, SessionTTL: time.Hour}},
		{"unparseable hash", Config{Email: testEmail, PasswordHash: "not-a-phc-string", SessionTTL: time.Hour}},
		{"bcrypt instead of argon2id", Config{Email: testEmail, PasswordHash: "$2a$10$abcdefghijklmnopqrstuv", SessionTTL: time.Hour}},
		{"zero ttl", Config{Email: testEmail, PasswordHash: hash}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg, newMemStore(), log, nil); err == nil {
				t.Fatal("New succeeded with an unusable configuration")
			}
		})
	}
}

func TestInsecureCookieModeIsOptInAndRenamesTheCookie(t *testing.T) {
	hash, _ := HashPassword(testPassword)
	svc, err := New(Config{
		Email: testEmail, PasswordHash: hash, SessionTTL: time.Hour, CookieInsecure: true,
	}, newMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := sessionCookie(t, login(t, router(svc), testEmail, testPassword))
	if c.Name != CookieNameInsecure {
		t.Errorf("cookie name = %q, want the un-prefixed dev name", c.Name)
	}
	if c.Secure {
		t.Error("insecure mode still set Secure")
	}
}

/* ── the verifier ────────────────────────────────────────────────────── */

func TestHashPasswordRoundTrips(t *testing.T) {
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, testPassword) {
		t.Fatal("the verifier contains the password")
	}
	v, err := parseVerifier(hash)
	if err != nil {
		t.Fatalf("parseVerifier: %v", err)
	}
	if !v.matches(testPassword) {
		t.Error("the verifier rejects the password it was built from")
	}
	if v.matches(testPassword + "x") {
		t.Error("the verifier accepts a wrong password")
	}
}

func TestHashPasswordSaltsEachCall(t *testing.T) {
	a, _ := HashPassword(testPassword)
	b, _ := HashPassword(testPassword)
	if a == b {
		t.Fatal("two hashes of the same password are identical — the salt is not random")
	}
}

func TestHashPasswordRefusesEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("hashed an empty password")
	}
}
