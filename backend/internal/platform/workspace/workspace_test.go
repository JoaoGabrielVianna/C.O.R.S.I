package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// inspector is the dummy "next" handler the middleware delegates to. It
// captures the workspace id from context plus a synthetic 200 response so
// the test can assert downstream behavior end-to-end.
func inspector(t *testing.T, captured *uuid.UUID) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			t.Errorf("downstream handler reached without a workspace id in context")
		}
		*captured = id
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_HeaderPresent_Lax(t *testing.T) {
	t.Parallel()
	wantID := uuid.New()
	var got uuid.UUID
	h := Middleware(Config{RequireHeader: false}, nil)(inspector(t, &got))

	req := httptest.NewRequest(http.MethodGet, "/finance/anything", nil)
	req.Header.Set(HeaderName, wantID.String())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != wantID {
		t.Fatalf("ctx workspace = %s, want %s", got, wantID)
	}
	if rec.Header().Get(SourceResponseHeader) != "" {
		t.Fatalf("sentinel response header leaked when header was present: %q",
			rec.Header().Get(SourceResponseHeader))
	}
}

func TestMiddleware_HeaderPresent_Strict(t *testing.T) {
	t.Parallel()
	wantID := uuid.New()
	var got uuid.UUID
	h := Middleware(Config{RequireHeader: true}, nil)(inspector(t, &got))

	req := httptest.NewRequest(http.MethodGet, "/finance/anything", nil)
	req.Header.Set(HeaderName, wantID.String())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != wantID {
		t.Fatalf("ctx workspace = %s, want %s", got, wantID)
	}
}

func TestMiddleware_HeaderMissing_Lax_UsesSentinel(t *testing.T) {
	t.Parallel()
	var got uuid.UUID
	h := Middleware(Config{RequireHeader: false}, nil)(inspector(t, &got))

	req := httptest.NewRequest(http.MethodGet, "/finance/anything", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (lax fallback)", rec.Code)
	}
	if got != DevWorkspaceID {
		t.Fatalf("ctx workspace = %s, want sentinel %s", got, DevWorkspaceID)
	}
	if v := rec.Header().Get(SourceResponseHeader); v != SourceDevSentinelValue {
		t.Fatalf("%s = %q, want %q", SourceResponseHeader, v, SourceDevSentinelValue)
	}
}

func TestMiddleware_HeaderMissing_Strict_Returns401(t *testing.T) {
	t.Parallel()
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})
	h := Middleware(Config{RequireHeader: true}, nil)(next)

	req := httptest.NewRequest(http.MethodGet, "/finance/anything", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatalf("downstream handler was reached despite missing header in strict mode")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "workspace_required" {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, "workspace_required")
	}
	if body.Error.Message != "workspace header required" {
		t.Fatalf("error.message = %q, want %q", body.Error.Message, "workspace header required")
	}
}

func TestMiddleware_MalformedHeader_AlwaysReturns400(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name string
		cfg  Config
	}{
		{"lax", Config{RequireHeader: false}},
		{"strict", Config{RequireHeader: true}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			})
			h := Middleware(mode.cfg, nil)(next)

			req := httptest.NewRequest(http.MethodGet, "/finance/anything", nil)
			req.Header.Set(HeaderName, "not-a-uuid")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if called {
				t.Fatalf("downstream reached on malformed header")
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body.Error.Code != "workspace_invalid" {
				t.Fatalf("error.code = %q, want workspace_invalid", body.Error.Code)
			}
		})
	}
}
