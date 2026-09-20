package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// R1 diagnosis · R2 correction: the generic deadline and the routes that
// must not inherit it.
//
// ══════════════════════════════════════════════════════════════════════
//
//	LONG-LIVED CHAT STREAMS MUST NOT INHERIT THE GENERIC REQUEST DEADLINE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What these tests are evidence for ──────────────────────────────────
// The live database holds 246 assistant turns that finished normally and
// not one of them took longer than 30 seconds. It also holds 18 turns
// recorded as `aborted`, and 14 of those died between 30.2s and 31.3s,
// across three different agents, with and without tools.
//
// That distribution had one cause: `NewRouter` wrapped EVERY request in a
// 30-second context, and `POST /chat/conversations/{id}/messages` is a
// Server-Sent Events stream that routinely runs longer than a request.
//
// ── What is pinned here, and what is not ───────────────────────────────
// The NUMBER is pinned here, because this is where the decision lives. The
// exemption is pinned here as a property of the middleware. What a chat
// turn does under it is pinned in chat/turnstream_integration_test.go,
// against the real route and a real database.

func timeoutRouter(t *testing.T, register func(chi.Router)) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouter(log)
	// Mounted as a module mounts itself, because that is the shape the chat
	// stream is served under: a sub-router below the root's middleware.
	r.Route("/chat", register)
	return r
}

/* ── the number ──────────────────────────────────────────────────────── */

// A deadline is right for a request/response route and is the wrong tool
// for a stream. This test does not argue that; it refuses to let the
// ceiling be invisible.
func TestEveryRouteCarriesAThirtySecondDeadline(t *testing.T) {
	var (
		deadline time.Time
		ok       bool
	)
	before := time.Now()
	rec := httptest.NewRecorder()
	timeoutRouter(t, func(r chi.Router) {
		r.Post("/plain", func(_ http.ResponseWriter, req *http.Request) {
			deadline, ok = req.Context().Deadline()
		})
	}).ServeHTTP(rec, httptest.NewRequest("POST", "/chat/plain", nil))

	if !ok {
		t.Fatal("the request context carries no deadline: this test is describing a router that no longer exists")
	}
	if budget := deadline.Sub(before); budget < 29*time.Second || budget > 31*time.Second {
		t.Fatalf("the route's budget is %s, want ~30s — see DefaultRequestTimeout", budget)
	}
	if DefaultRequestTimeout != 30*time.Second {
		t.Fatalf("DefaultRequestTimeout = %s, want 30s", DefaultRequestTimeout)
	}
}

/* ── ordinary routes still time out ──────────────────────────────────── */

// The protection was SCOPED, not removed.
//
// A route that does not opt out still gets the deadline, still observes
// `context.DeadlineExceeded`, and still answers 504. Composed with a small
// timeout so the test does not wait thirty seconds for a number the test
// above already pinned.
func TestAnOrdinaryRouteStillTimesOut(t *testing.T) {
	var observed error
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithTimeout(log, 50*time.Millisecond)
	r.Route("/chat", func(r chi.Router) {
		r.Post("/plain", func(_ http.ResponseWriter, req *http.Request) {
			<-req.Context().Done()
			observed = req.Context().Err()
		})
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/chat/plain", nil))

	if !errors.Is(observed, context.DeadlineExceeded) {
		t.Fatalf("an ordinary route observed %v, want context.DeadlineExceeded", observed)
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504: a route that never answered must say so", rec.Code)
	}
}

/* ── the exemption ───────────────────────────────────────────────────── */

// The regression, at the middleware.
//
// Same router, same timeout, two routes: the one that opted out is not
// killed by the clock. Before R2 there was no way to express this and both
// died.
func TestAnExemptedRouteOutlivesTheGenericDeadline(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const budget = 50 * time.Millisecond
	r := NewRouterWithTimeout(log, budget)

	var (
		hadDeadline = true
		errAfter    error
	)
	r.Route("/chat", func(r chi.Router) {
		r.With(WithoutRequestDeadline).Post("/stream", func(w http.ResponseWriter, req *http.Request) {
			_, hadDeadline = req.Context().Deadline()
			// The stream opens and then outlives the generic deadline,
			// which is what a model taking its time looks like.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "event: delta\ndata: {\"text\":\"primeiro pedaço\"}\n\n")
			time.Sleep(budget * 3)
			errAfter = req.Context().Err()
			_, _ = io.WriteString(w, "event: done\ndata: {}\n\n")
		})
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/chat/stream", nil))

	if hadDeadline {
		t.Fatal("the exempted route still carries a deadline")
	}
	if errAfter != nil {
		t.Fatalf("the exempted route was ended anyway, with %v", errAfter)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "event: done") {
		t.Fatalf("the stream did not reach its own end: %q", body)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE EXEMPTION IS NOT IMMORTALITY
//
// ══════════════════════════════════════════════════════════════════════
//
// The route keeps the request's REAL lifetime. A client that goes away
// still ends it, and it ends SYNCHRONOUSLY: `Done` is the lifetime's own
// channel, not a copy fed by a goroutine. The first build of this used
// `AfterFunc` to forward cancellation and a hung-up client produced a
// completed turn, because the turn finished inside the scheduling gap.
func TestAnExemptedRouteStillEndsWhenTheClientGoesAway(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithTimeout(log, time.Hour)

	var observed error
	r.Route("/chat", func(r chi.Router) {
		r.With(WithoutRequestDeadline).Post("/stream", func(_ http.ResponseWriter, req *http.Request) {
			<-req.Context().Done()
			observed = req.Context().Err()
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/chat/stream", nil).WithContext(ctx))

	if !errors.Is(observed, context.Canceled) {
		t.Fatalf("the exempted route observed %v, want context.Canceled", observed)
	}
}

// And a deadline that is genuinely the CALLER's stays a deadline.
//
// This is what keeps the terminal distinction honest through the
// exemption: `Err` is delegated rather than re-manufactured, so a turn
// downstream can still tell a clock from a person. See
// chat/app.stoppedBy.
func TestAnExemptedRoutePreservesWhichKindOfEndingItWas(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithTimeout(log, time.Hour)

	var observed error
	r.Route("/chat", func(r chi.Router) {
		r.With(WithoutRequestDeadline).Post("/stream", func(_ http.ResponseWriter, req *http.Request) {
			<-req.Context().Done()
			observed = context.Cause(req.Context())
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/chat/stream", nil).WithContext(ctx))

	if !errors.Is(observed, context.DeadlineExceeded) {
		t.Fatalf("a caller's deadline reached the handler as %v; the distinction was flattened", observed)
	}
}

// The values the rest of the stack depends on survive the exemption.
//
// Swapping the handler onto the stashed lifetime would have been the
// obvious implementation and a bug with a long fuse: the workspace id,
// chi's route context and the request id are all added AFTER the deadline
// middleware, and a route that lost them would fail authorization rather
// than time out.
func TestTheExemptionKeepsEverythingAddedAfterTheDeadline(t *testing.T) {
	type probeKey struct{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithTimeout(log, time.Hour)

	var (
		value any
		route string
	)
	r.Route("/chat", func(r chi.Router) {
		// A middleware INSIDE the module, exactly where the workspace
		// middleware sits: after the deadline, before the handler.
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(
					context.WithValue(req.Context(), probeKey{}, "workspace-ish")))
			})
		})
		r.With(WithoutRequestDeadline).Post("/{id}/stream", func(_ http.ResponseWriter, req *http.Request) {
			value = req.Context().Value(probeKey{})
			route = chi.RouteContext(req.Context()).RoutePattern()
		})
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/chat/abc/stream", nil))

	if value != "workspace-ish" {
		t.Fatalf("a value added after the deadline middleware = %v, want it preserved", value)
	}
	if route != "/chat/{id}/stream" {
		t.Fatalf("chi route context = %q, want the matched pattern", route)
	}
}

/* ── the 504 that used to be noise ───────────────────────────────────── */

// chi wrote a 504 unconditionally when its deadline fired, which on a
// stream that had already committed a 200 produced
// `http: superfluous response.WriteHeader call` on every timed-out turn.
// That line was the one server-side signal the incident left, and it was
// noise rather than a signal.
func TestNoGatewayTimeoutIsWrittenOverAnAlreadyStartedResponse(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithTimeout(log, 30*time.Millisecond)
	r.Route("/chat", func(r chi.Router) {
		r.Post("/plain", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "already committed")
			<-req.Context().Done()
		})
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/chat/plain", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the response had already been started", rec.Code)
	}
}

// The deadline is the ROOT's, so it reaches a module the same way it
// reaches a flat route. Stated separately because the router's other known
// defect was exactly this shape — something set on the root that did not
// reach the sub-routers every module mounts itself as. See UseErrorEnvelope.
func TestTheDeadlineReachesMountedModulesAndFlatRoutesAlike(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	seen := map[string]bool{}
	record := func(path string) http.HandlerFunc {
		return func(_ http.ResponseWriter, r *http.Request) {
			_, ok := r.Context().Deadline()
			seen[path] = ok
		}
	}
	r := NewRouter(log)
	r.Post("/flat", record("/flat"))
	r.Route("/chat", func(r chi.Router) { r.Post("/stream", record("/chat/stream")) })

	for _, p := range []string{"/flat", "/chat/stream"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", p, nil))
		if !seen[p] {
			t.Fatalf("%s carries no deadline", p)
		}
	}
}
