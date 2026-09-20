// Package httpserver builds the root chi router with the shared middleware
// stack (request id, real ip, structured access log, panic recovery, timeout).
package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/corsi/backend/internal/platform/apierror"
)

// DefaultRequestTimeout bounds an ordinary request/response route.
//
// It is the right tool for a route that computes an answer and returns it,
// and the wrong tool for one that holds a connection open while a model
// thinks. See WithoutRequestDeadline.
const DefaultRequestTimeout = 30 * time.Second

func NewRouter(log *slog.Logger) *chi.Mux {
	return NewRouterWithTimeout(log, DefaultRequestTimeout)
}

// NewRouterWithTimeout is NewRouter with the generic deadline stated
// explicitly.
//
// The duration is a parameter for exactly one reason: a test that has to
// prove a route outlives the deadline cannot wait thirty seconds to do it.
// Production calls NewRouter and gets DefaultRequestTimeout; the regression
// suite composes the SAME middleware stack with a small one. Nothing else
// may vary between them, which is why this takes a duration and not a
// config struct.
func NewRouterWithTimeout(log *slog.Logger, timeout time.Duration) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(requestLogger(log))
	r.Use(chimw.Recoverer)
	r.Use(requestDeadline(timeout))
	return r
}

/* ── the generic deadline, and the routes that must not inherit it ───── */

// requestLifetimeKey retrieves the request's REAL lifetime: the context
// net/http built for this connection, before the generic deadline was
// imposed on it.
//
// A struct{} key in an unexported type, so nothing outside this package can
// write it and no other package's key can collide with it.
type requestLifetimeKey struct{}

// requestDeadline bounds a request and records what it was bounded FROM.
//
// ══════════════════════════════════════════════════════════════════════
//
//	LONG-LIVED CHAT STREAMS MUST NOT INHERIT THE GENERIC REQUEST DEADLINE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why this replaced chi's Timeout ────────────────────────────────────
// `chimw.Timeout(30s)` applied to every route in the product, including
// `POST /chat/conversations/{id}/messages`, which is a Server-Sent Events
// stream that runs for as long as a model takes to answer. R1 measured the
// consequence in the live database: 246 turns finished normally and not one
// took longer than 30 seconds, while 14 died between 30.2s and 31.3s across
// three different agents, nine of them without calling a single tool. They
// were recorded as `aborted`, which is also what the runtime records when
// the USER presses stop — so infrastructure killing a working turn was
// indistinguishable from a person changing their mind.
//
// ── Why the parent is stashed rather than the deadline skipped ─────────
// A middleware registered with `Use` runs BEFORE chi has matched a route,
// so it cannot know which handler is about to run and cannot decide to
// exempt one. Nor can the handler undo a deadline afterwards: by then the
// context that fires is the only one it has.
//
// So the exemption is expressed the only way it can be — the deadline
// middleware preserves the lifetime it derived from, and a route that needs
// it asks for it back. Explicit at the route, testable from either side, and
// with no list of module paths inside this package.
//
// ── The 504 ────────────────────────────────────────────────────────────
// chi wrote it unconditionally, which on a stream that had already
// committed a 200 produced `http: superfluous response.WriteHeader call` on
// every timed-out turn — the one server-side signal the incident left, and
// noise rather than a signal. It is written here only when the response has
// not been started.
func requestDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lifetime := r.Context()
			ctx, cancel := context.WithTimeout(lifetime, timeout)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, requestLifetimeKey{}, lifetime)))

			if ctx.Err() != context.DeadlineExceeded {
				return
			}
			// requestLogger has already wrapped the writer, so the status is
			// readable. A composition without it falls back to writing the
			// status, which is what chi always did.
			if ww, ok := w.(chimw.WrapResponseWriter); ok && ww.Status() != 0 {
				return
			}
			w.WriteHeader(http.StatusGatewayTimeout)
		})
	}
}

// WithoutRequestDeadline runs a route under the request's real lifetime
// instead of the generic deadline.
//
// ── What it keeps, and why each half matters ───────────────────────────
// It is NOT a swap back to the stashed context, which would be a bug with a
// long fuse: every value added AFTER the deadline middleware — the workspace
// id, chi's route context, the request id — lives on the current context,
// and a route that lost them would fail authorization rather than time out.
//
// So the current context's VALUES are kept and only its cancellation is
// re-sourced:
//
//	values       ← the context as it stands right now
//	cancellation ← the request's real lifetime
//	deadline     ← none
//
// Client disconnect and connection close still cancel, because those are
// what cancel the lifetime. What no longer happens is a clock deciding that
// a model taking 31 seconds is a failure.
//
// ── What it is NOT ─────────────────────────────────────────────────────
// It is not permission to run forever. A turn is bounded by maxToolRounds,
// by the agent's max_tokens, by the budget gate, by the provider closing the
// stream and by the reader going away. This removes an arbitrary bound, not
// every bound.
//
// Applied to a composition that never imposed a deadline, it is a no-op.
func WithoutRequestDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lifetime, ok := r.Context().Value(requestLifetimeKey{}).(context.Context)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(undeadlined{values: r.Context(), lifetime: lifetime}))
	})
}

// undeadlined takes its values from one context and its ending from
// another.
//
// ── Why a type and not context.WithCancel plus a watcher ───────────────
// The obvious build is `WithCancelCause(WithoutCancel(current))` plus an
// `AfterFunc` that forwards the lifetime's end. It works, and it is wrong
// in two ways that both matter.
//
// It makes cancellation ASYNCHRONOUS. The forwarding runs in a goroutine,
// so between the client hanging up and the turn noticing there is a
// scheduling gap. In production that gap is microseconds against a stream
// that runs for seconds; in a test with an instant provider the turn
// finishes inside it, which is how this was found — a hung-up client
// produced a completed turn.
//
// And it FLATTENS the error. A context cancelled by a forwarder reports
// `context.Canceled` whatever ended the lifetime, which is exactly the
// collapse R1 measured, rebuilt one layer up by the fix for it.
//
// Delegating instead has neither problem: `Done` is the lifetime's own
// channel, so a reader selecting on it wakes at the same instant it always
// did, and `Err` is the lifetime's own error, so a deadline stays a
// deadline and a cancellation stays a cancellation.
//
//	Value    ← the context as it stands now (workspace id, route, request id)
//	Done/Err ← the request's real lifetime
//	Deadline ← none, which is the entire point
type undeadlined struct {
	// values is the context at the point of the exemption. Everything added
	// after the deadline middleware lives here, and a route that lost it
	// would fail authorization rather than time out.
	values context.Context
	// lifetime is the context net/http built for this connection, before
	// the generic deadline was imposed. It ends when the client goes away.
	lifetime context.Context
}

func (u undeadlined) Deadline() (time.Time, bool) { return time.Time{}, false }
func (u undeadlined) Done() <-chan struct{}       { return u.lifetime.Done() }
func (u undeadlined) Err() error                  { return u.lifetime.Err() }
func (u undeadlined) Value(key any) any           { return u.values.Value(key) }

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
