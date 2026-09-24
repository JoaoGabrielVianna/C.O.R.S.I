package identity

import (
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// sessionResponse is what an authenticated caller learns about itself.
// The subject and the expiry, and nothing else: there is no profile to
// return and inventing one would create a field the frontend starts
// trusting.
type sessionResponse struct {
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at"`
}

// Register mounts the three auth routes on the root router.
//
// `/auth/login` and `/auth/logout` are in PublicPaths; `/auth/session` is
// not, and that is deliberate — it is the endpoint whose 401 IS the answer,
// both for the frontend's bootstrap and for the frontend nginx's
// auth_request subrequest.
func (s *Service) Register(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
		r.Get("/session", s.handleSession)
	})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	key := clientKey(r)
	if !s.limiter.allow(key) {
		// 429 rather than 401, because the two are different facts and
		// conflating them would tell a locked-out operator that their
		// password is wrong. It leaks only that this address has been
		// failing, which the address already knows.
		apierror.Write(w, s.log, apierror.New(http.StatusTooManyRequests,
			"too_many_attempts", "too many failed attempts; try again later"))
		return
	}

	var req loginRequest
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, s.log, apierror.New(http.StatusBadRequest, "invalid_body", err.Error()))
		return
	}

	if err := s.authenticate(req.Email, req.Password); err != nil {
		s.limiter.fail(key)
		// Logged WITHOUT the submitted email or password. A failed login is
		// worth knowing about; the credential that failed is not worth
		// writing to a log that is less protected than the database.
		s.log.Warn("identity: failed login", "remote", key)
		apierror.Write(w, s.log, apierror.New(http.StatusUnauthorized,
			"invalid_credentials", "invalid credentials"))
		return
	}

	cookie, sess, err := s.issue(r.Context())
	if err != nil {
		apierror.Write(w, s.log, apierror.New(http.StatusInternalServerError,
			"session_failed", "could not start a session"))
		s.log.Error("identity: issue session", "err", err)
		return
	}
	s.limiter.succeed(key)

	// Opportunistic reclaim of expired rows, on the one request that is
	// already rate-limited and already writing. Its failure is logged and
	// never surfaced: Lookup refuses expired sessions regardless, so this
	// is housekeeping and not correctness.
	if n, err := s.store.DeleteExpired(r.Context(), s.now()); err != nil {
		s.log.Warn("identity: expired session sweep failed", "err", err)
	} else if n > 0 {
		s.log.Info("identity: expired sessions removed", "count", n)
	}

	http.SetCookie(w, cookie)
	s.log.Info("identity: login", "remote", key)
	render.JSON(w, http.StatusOK, sessionResponse{
		Email:     sess.Subject,
		ExpiresAt: sess.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// handleLogout ends the presented session server-side and clears the
// cookie.
//
// Public, and idempotent: no cookie, an unknown cookie and a live one all
// return 204. The row is deleted, so the token is dead even for a copy of
// the cookie held somewhere else — which is the property a stateless token
// could not have given.
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cookieName()); err == nil && c.Value != "" {
		if digest, ok := digestOf(c.Value); ok {
			if err := s.store.Delete(r.Context(), digest); err != nil {
				s.log.Error("identity: delete session", "err", err)
				apierror.Write(w, s.log, apierror.New(http.StatusInternalServerError,
					"logout_failed", "could not end the session"))
				return
			}
		}
	}
	http.SetCookie(w, s.clearCookie())
	w.WriteHeader(http.StatusNoContent)
}

// handleSession answers who the caller is. Reaching this handler already
// means the middleware admitted them, so the only body it ever writes is a
// success — the 401 is the middleware's.
func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := FromContext(r.Context())
	if !ok {
		apierror.Write(w, s.log, apierror.New(http.StatusUnauthorized,
			"unauthenticated", "authentication required"))
		return
	}
	render.JSON(w, http.StatusOK, sessionResponse{
		Email:     sess.Subject,
		ExpiresAt: sess.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// clientKey is the rate limiter's bucket: the caller's IP.
//
// chi's RealIP middleware has already rewritten RemoteAddr from
// X-Forwarded-For where a proxy set one, so this reads one field rather
// than re-implementing that parsing. Behind our own nginx that is the
// browser's address; with no proxy it is the socket's.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if strings.TrimSpace(r.RemoteAddr) == "" {
			return "unknown"
		}
		return r.RemoteAddr
	}
	return host
}
