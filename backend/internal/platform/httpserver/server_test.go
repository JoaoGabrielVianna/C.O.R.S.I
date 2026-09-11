package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The router's own failures have to speak the API's language.
//
// ── Why this file exists ───────────────────────────────────────────────
// Every handler in this codebase answers a failure with
// `{"error":{"code","message"}}`, and the httpapi packages document that as
// the contract. chi's defaults did not: an unknown path came back as
// `404 page not found` in `text/plain`, and a wrong verb came back the same
// way.
//
// That gap is not theoretical. A frontend running against a backend that
// predated a route received exactly that body, could not parse it, and
// reported "non-JSON response from server" — burying the one fact that
// would have diagnosed it instantly, which was the 404.
//
// These tests fail on the router as it was.

// testRouter mirrors how the real binary is assembled, and the mirroring
// is the point.
//
// Every bounded context mounts itself with `r.Route("/prefix", …)`, which
// is a chi SUB-router with a 404 handler of its own. A test that registered
// only flat routes on the root would pass while the mounted modules — that
// is, the entire API — went on answering plain text. The first version of
// this fix did exactly that and this helper is what caught it.
func testRouter() http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouter(log)
	r.Get("/known", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// A module, mounted the way a module is.
	r.Route("/chat", func(r chi.Router) {
		r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	// Last, as the composition root does it.
	UseErrorEnvelope(r, log)
	return r
}

type wireError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeWireError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) wireError {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json — a client told to expect "+
			"JSON cannot read anything else", ct)
	}
	var body wireError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		// The exact failure the hotfix is about: a body no client can parse.
		t.Fatalf("body is not JSON (%v): %q", err, rec.Body.String())
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("the envelope needs a code and an actionable message, got %+v", body.Error)
	}
	return body
}

// The exact request that failed, at the exact address it failed at: a path
// INSIDE a mounted module, which is where every real 404 of this API
// happens.
func TestUnknownRouteInsideAModuleAnswersTheErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter().ServeHTTP(rec, httptest.NewRequest("POST", "/chat/conversations/x/memory-candidates", nil))

	body := decodeWireError(t, rec, http.StatusNotFound)
	if body.Error.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", body.Error.Code)
	}
}

func TestUnknownRouteAtTheRootAnswersTheErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter().ServeHTTP(rec, httptest.NewRequest("GET", "/nothing-here", nil))

	body := decodeWireError(t, rec, http.StatusNotFound)
	if body.Error.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", body.Error.Code)
	}
}

func TestWrongMethodAnswersTheErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter().ServeHTTP(rec, httptest.NewRequest("POST", "/known", nil))

	body := decodeWireError(t, rec, http.StatusMethodNotAllowed)
	if body.Error.Code != "method_not_allowed" {
		t.Fatalf("code = %q, want method_not_allowed", body.Error.Code)
	}
}

// The status is unchanged. This is the router keeping a promise about the
// BODY, never a relaxation of what the status means: a missing route is
// still missing, and nothing here turns a failure into a success.
func TestTheseAreStillFailures(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/nope", http.StatusNotFound},
		{"POST", "/chat/conversations/x/memory-candidates", http.StatusNotFound},
		{"DELETE", "/known", http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		testRouter().ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Fatalf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

// Routes that exist are untouched by any of this, inside a module or not.
func TestKnownRoutesAreUnaffected(t *testing.T) {
	for _, path := range []string{"/known", "/chat/ping"} {
		rec := httptest.NewRecorder()
		testRouter().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("GET %s = %d, want 204", path, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("GET %s body = %q, want empty", path, rec.Body.String())
		}
	}
}
