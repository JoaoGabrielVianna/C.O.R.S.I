// Package httpserver builds the root chi router with the shared middleware
// stack (request id, real ip, structured access log, panic recovery, timeout).
package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/corsi/backend/internal/platform/apierror"
)

func NewRouter(log *slog.Logger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(requestLogger(log))
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))
	return r
}

// UseErrorEnvelope teaches the router to answer its own two failures — an
// address that does not exist and a verb a route does not take — in the
// shape the rest of the API documents.
//
// ── Why this is not cosmetic ───────────────────────────────────────────
// Every handler in this codebase answers a failure with
// `{"error":{"code","message"}}`, and the httpapi packages document that as
// the contract. chi's defaults do not: they write `404 page not found` as
// `text/plain`. So the one shape a client is told to expect is exactly the
// shape it does not get on the two failures it is most likely to meet.
//
// A client hitting that has no code to branch on and no message to show. It
// is what turned "this build of the API does not have that route" into
// "non-JSON response from server".
//
// The status is untouched. A 404 stays a 404 and stays a failure; only its
// body changes, from something nothing can read into something everything
// already can.
//
// ── Why it is a separate call, made LAST ───────────────────────────────
// Because chi propagates these handlers to sub-routers at the moment they
// are set, by walking the routes that exist THEN. Every bounded context
// mounts itself with `r.Route("/chat", …)`, which is a sub-router with its
// own 404 handler — so setting this inside NewRouter, before anything is
// mounted, reaches the root and none of the modules. That is precisely the
// path that was broken, and the first version of this fix reproduced the
// bug it was fixing: the root answered JSON and `/chat/whatever` went on
// answering plain text.
//
// Hence the requirement, stated where it cannot be missed: call this after
// every module has registered.
func UseErrorEnvelope(r *chi.Mux, log *slog.Logger) {
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		apierror.Write(w, log, apierror.New(http.StatusNotFound, "not_found",
			"no route for "+req.Method+" "+req.URL.Path))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		apierror.Write(w, log, apierror.New(http.StatusMethodNotAllowed, "method_not_allowed",
			req.Method+" is not allowed on "+req.URL.Path))
	})
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", chimw.GetReqID(r.Context()),
			)
		})
	}
}
