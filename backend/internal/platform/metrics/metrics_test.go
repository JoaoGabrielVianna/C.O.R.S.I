package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/corsi/backend/internal/platform/workspace"
)

// Build a chi router with the same shape main.go uses: HTTP middleware
// wraps every route; /metrics is mounted unguarded; the workspace
// middleware (with the registry's recorder) guards a /finance group.
// No database — the DB collector is exercised separately.
func newTestRouter(reg *Registry) chi.Router {
	r := chi.NewRouter()
	r.Use(reg.HTTPMiddleware())
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Mount("/metrics", reg.Handler())
	r.Route("/finance", func(r chi.Router) {
		r.Use(workspace.Middleware(workspace.Config{RequireHeader: false}, reg.Workspace()))
		r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
	})
	return r
}

func send(t *testing.T, h http.Handler, method, path string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

func mustContainAll(t *testing.T, body string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(body, n) {
			t.Errorf("/metrics body missing %q\n----- body -----\n%s", n, body)
		}
	}
}

func TestMetrics_HTTPRequestCounters(t *testing.T) {
	reg := New()
	router := newTestRouter(reg)

	// 1 health request, 2 finance pings (with header, no leak), 1 finance
	// ping (header missing → sentinel), 1 finance ping (malformed header).
	wsID := "11111111-2222-3333-4444-555555555555"
	send(t, router, "GET", "/health/live", nil)
	send(t, router, "GET", "/finance/ping", map[string]string{"X-Workspace-Id": wsID})
	send(t, router, "GET", "/finance/ping", map[string]string{"X-Workspace-Id": wsID})
	send(t, router, "GET", "/finance/ping", nil)
	send(t, router, "GET", "/finance/ping", map[string]string{"X-Workspace-Id": "bogus"})

	body := scrape(t, router)

	mustContainAll(t, body,
		// HTTP family. Requests that reach the leaf handler resolve the
		// full route pattern (`/finance/ping`); requests short-circuited
		// by the workspace middleware resolve only the mount pattern
		// (`/finance/*`) because chi hasn't descended past the group yet.
		`corsi_http_requests_total{method="GET",route="/health/live",status="200"} 1`,
		`corsi_http_requests_total{method="GET",route="/finance/ping",status="200"} 3`, // 2 with header + 1 sentinel (all dispatched to leaf)
		`corsi_http_requests_total{method="GET",route="/finance/*",status="400"} 1`,    // malformed header short-circuit
		`corsi_http_status_code_total{code="200"}`,
		`corsi_http_status_code_total{code="400"}`,
		`corsi_http_request_duration_seconds_bucket`,
		// Workspace family
		`corsi_workspace_requests_total 4`,       // all /finance/ping hits
		`corsi_workspace_missing_header_total 1`, // one request omitted header
		`corsi_workspace_sentinel_total 1`,       // sentinel fired once
		`corsi_workspace_invalid_total 1`,        // one malformed header
		// Go + process collectors arrive automatically
		`go_goroutines`,
		`process_resident_memory_bytes`,
	)
}

func TestMetrics_StrictMode_NoSentinel(t *testing.T) {
	reg := New()
	r := chi.NewRouter()
	r.Use(reg.HTTPMiddleware())
	r.Mount("/metrics", reg.Handler())
	r.Route("/finance", func(r chi.Router) {
		r.Use(workspace.Middleware(workspace.Config{RequireHeader: true}, reg.Workspace()))
		r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	})

	// Strict mode + missing header → 401, sentinel must NOT fire.
	code := send(t, r, "GET", "/finance/ping", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	body := scrape(t, r)
	mustContainAll(t, body,
		`corsi_workspace_missing_header_total 1`,
		`corsi_workspace_sentinel_total 0`,                                          // sentinel never used
		`corsi_http_requests_total{method="GET",route="/finance/*",status="401"} 1`, // short-circuit before leaf
	)
}

func TestMetrics_MetricsEndpointSelfExcludedFromHTTPCounters(t *testing.T) {
	reg := New()
	router := newTestRouter(reg)

	// Scrape twice; the HTTP middleware skips /metrics so corsi_http_*
	// must not record either scrape.
	_ = scrape(t, router)
	body := scrape(t, router)

	if strings.Contains(body, `corsi_http_requests_total{method="GET",route="/metrics"`) {
		t.Errorf("/metrics scrape recorded itself in corsi_http_requests_total")
	}
}

func TestMetrics_RouteCountGauge(t *testing.T) {
	reg := New()
	reg.SetRouteCount(13)
	router := newTestRouter(reg)
	body := scrape(t, router)
	mustContainAll(t, body, `corsi_http_route_count 13`)
}
