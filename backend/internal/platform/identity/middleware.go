package identity

import (
	"net/http"
	"path"
	"strings"

	"github.com/corsi/backend/internal/platform/apierror"
)

// PublicPaths is the complete list of addresses this API answers without a
// session. Everything else — every module, every route, every one added
// later — is refused.
//
// ── Each entry, and the justification the sprint requires ──────────────
//
//	/health     Infrastructure liveness and readiness. EasyPanel's
//	            container healthcheck calls it with no browser, no cookie
//	            and no ability to log in, and a health endpoint behind auth
//	            is a health endpoint that reports the orchestrator's
//	            credentials rather than the service's state. It discloses
//	            {application, status} plus the build's go version and
//	            binary timestamp, and no operator data.
//
//	/metrics    The existing documented decision — Prometheus scrapes
//	            without credentials — is preserved rather than silently
//	            reversed. It is NOT publicly reachable: the only public
//	            hostname routes to the frontend's nginx, which proxies
//	            /api/ and deliberately does not proxy this path.
//
//	/auth/login  The endpoint that MAKES a session cannot require one.
//	             Rate-limited, and the only route here that is.
//
//	/auth/logout Idempotent and public on purpose: a session that has
//	             already expired must still be able to clear its cookie,
//	             and requiring auth to log out means the one state you
//	             cannot leave is the broken one.
//
// A path is public only if it matches exactly or is a child of an entry.
// Prefix matching is on SEGMENTS, so an entry never accidentally covers a
// sibling that merely starts with the same letters.
var PublicPaths = []string{
	"/health",
	"/metrics",
	"/auth/login",
	"/auth/logout",
}

// IsPublic reports whether a path bypasses authentication.
//
// ── Why the path is CLEANED first ──────────────────────────────────────
// Because `/metrics/../finance/summary` starts with a public prefix and
// addresses a private route. Matching the raw string called that public,
// and anything downstream that normalises — a proxy, a router, a handler
// joining it to a root — would then serve a private route through a gate
// that had already waved it past. The regression test that found this is
// TestEveryRouteIsDeniedByDefault.
//
// path.Clean resolves `.` and `..` and collapses repeated slashes, and it
// runs BEFORE the comparison so the allow-list decides about the address
// that will actually be served. `r.URL.Path` is already percent-decoded by
// net/http, so `%2e%2e` arrives here as `..` and is resolved with it.
func IsPublic(p string) bool {
	if p == "" || p[0] != '/' {
		// A relative or empty path is not something this allow-list can
		// reason about. Refusing is the safe answer.
		return false
	}
	p = path.Clean(p)
	for _, pub := range PublicPaths {
		if p == pub || strings.HasPrefix(p, pub+"/") {
			return true
		}
	}
	return false
}

// Middleware refuses every request without a live session, except the
// handful of addresses PublicPaths names.
//
// It is registered on the ROOT router, before any module mounts. Registering
// it per module would make protection a thing each module opts into, and an
// opt-in gate is one module away from a hole nobody notices — which is the
// defect this sprint exists to close, in its reverse-proxy form.
func (s *Service) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsPublic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			sess, ok := s.resolve(r)
			if !ok {
				// One code for every way of not being logged in: no
				// cookie, a malformed one, an unknown one and an expired
				// one are the same answer. The frontend branches on this
				// code to send the operator back to the login screen.
				apierror.Write(w, s.log, apierror.New(http.StatusUnauthorized,
					"unauthenticated", "authentication required"))
				return
			}
			next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), sess)))
		})
	}
}
