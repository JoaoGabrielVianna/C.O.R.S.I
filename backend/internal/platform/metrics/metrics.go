// Package metrics is the operational-observability surface: a Prometheus
// registry, an HTTP middleware that records per-route counters /
// histograms, a Recorder type the workspace middleware uses to count
// header outcomes, and a pgxpool stats collector.
//
// Design:
//
//   - One shared `*prometheus.Registry` owned by `Registry`. The Go
//     runtime + process collectors are registered up front; everything
//     else hangs off Registry.
//
//   - HTTP metrics are recorded by an outer chi middleware. Route
//     pattern is read from chi's RouteContext *after* `next.ServeHTTP`
//     returns, so the matched pattern (not the raw URL) becomes the
//     label — bounded cardinality.
//
//   - Workspace metrics live behind a `Recorder` interface (declared
//     in the workspace package) so that package keeps a zero-dep
//     domain boundary and tests can pass nil.
//
//   - DB stats are emitted by a custom Collector that calls
//     `pool.Stat()` on each scrape. No background goroutines.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "corsi"

// Registry owns the prometheus registry plus every metric the binary
// exposes. Build once in main.go, share its handle into every collaborator.
type Registry struct {
	reg *prometheus.Registry

	httpRequestsTotal *prometheus.CounterVec
	httpDuration      *prometheus.HistogramVec
	httpStatusTotal   *prometheus.CounterVec
	httpRouteCount    prometheus.Gauge

	workspaceTotal    prometheus.Counter
	workspaceMissing  prometheus.Counter
	workspaceInvalid  prometheus.Counter
	workspaceSentinel prometheus.Counter
}

func New() *Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector())
	r.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	out := &Registry{
		reg: r,
		httpRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "http", Name: "requests_total",
			Help: "Total HTTP requests received, labeled by method, route pattern and status.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "http", Name: "request_duration_seconds",
			Help:    "HTTP request latency in seconds, labeled by method and route.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		httpStatusTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "http", Name: "status_code_total",
			Help: "Total HTTP responses by status code (separate from requests_total for cheap aggregation).",
		}, []string{"code"}),
		httpRouteCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "http", Name: "route_count",
			Help: "Number of HTTP routes registered on the server (set once at boot).",
		}),
		workspaceTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "workspace", Name: "requests_total",
			Help: "Total requests that passed through the workspace middleware.",
		}),
		workspaceMissing: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "workspace", Name: "missing_header_total",
			Help: "Total requests with no X-Workspace-Id header (any mode).",
		}),
		workspaceInvalid: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "workspace", Name: "invalid_total",
			Help: "Total requests with a malformed X-Workspace-Id header (always 400).",
		}),
		workspaceSentinel: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "workspace", Name: "sentinel_total",
			Help: "Total times the dev-sentinel workspace was used. Should stay at 0 in production.",
		}),
	}

	r.MustRegister(
		out.httpRequestsTotal, out.httpDuration, out.httpStatusTotal, out.httpRouteCount,
		out.workspaceTotal, out.workspaceMissing, out.workspaceInvalid, out.workspaceSentinel,
	)
	return out
}

// Handler returns the http.Handler that serves `/metrics`.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		Registry: r.reg,
	})
}

// HTTPMiddleware records counters + histograms + status-code totals.
// Skips `/metrics` itself so scrapes don't churn their own counters.
func (r *Registry) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasPrefix(req.URL.Path, "/metrics") {
				next.ServeHTTP(w, req)
				return
			}
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, req.ProtoMajor)
			next.ServeHTTP(ww, req)

			route := chi.RouteContext(req.Context()).RoutePattern()
			if route == "" {
				// 404s and any path that didn't match a registered route
				// land here. Using the literal URL would blow up label
				// cardinality, so bucket them into a single label.
				route = "unmatched"
			}
			status := strconv.Itoa(ww.Status())

			r.httpRequestsTotal.WithLabelValues(req.Method, route, status).Inc()
			r.httpStatusTotal.WithLabelValues(status).Inc()
			r.httpDuration.WithLabelValues(req.Method, route).Observe(time.Since(start).Seconds())
		})
	}
}

// SetRouteCount is called once after all routes are mounted.
func (r *Registry) SetRouteCount(n int) {
	r.httpRouteCount.Set(float64(n))
}

// Workspace returns the recorder used by the workspace middleware.
func (r *Registry) Workspace() WorkspaceRecorder {
	return WorkspaceRecorder{
		total:    r.workspaceTotal,
		missing:  r.workspaceMissing,
		invalid:  r.workspaceInvalid,
		sentinel: r.workspaceSentinel,
	}
}

// WorkspaceRecorder satisfies workspace.Recorder. Kept as a value type
// so callers can pass it cheaply by value.
type WorkspaceRecorder struct {
	total, missing, invalid, sentinel prometheus.Counter
}

func (r WorkspaceRecorder) Request()       { r.total.Inc() }
func (r WorkspaceRecorder) MissingHeader() { r.missing.Inc() }
func (r WorkspaceRecorder) InvalidHeader() { r.invalid.Inc() }
func (r WorkspaceRecorder) Sentinel()      { r.sentinel.Inc() }

// RegisterDBCollector wires pgxpool stats into the registry. Call after
// the pool is open; safe to call once per process.
func (r *Registry) RegisterDBCollector(pool *pgxpool.Pool) error {
	return r.reg.Register(newDBCollector(pool))
}

// dbCollector emits the four operationally interesting pool metrics on
// each scrape. AcquiredConns / IdleConns are gauges; AcquireCount /
// EmptyAcquireCount are monotonic counters (cumulative since boot).
type dbCollector struct {
	pool          *pgxpool.Pool
	active        *prometheus.Desc
	idle          *prometheus.Desc
	acquiredTotal *prometheus.Desc
	waitingTotal  *prometheus.Desc
}

func newDBCollector(pool *pgxpool.Pool) *dbCollector {
	return &dbCollector{
		pool: pool,
		active: prometheus.NewDesc(namespace+"_db_connections_active",
			"Currently acquired database connections from the pgxpool pool.", nil, nil),
		idle: prometheus.NewDesc(namespace+"_db_connections_idle",
			"Currently idle database connections held by the pool.", nil, nil),
		acquiredTotal: prometheus.NewDesc(namespace+"_db_connections_acquired_total",
			"Total successful pool acquires since process boot.", nil, nil),
		waitingTotal: prometheus.NewDesc(namespace+"_db_connections_waiting_total",
			"Total pool acquires that had to wait because the pool was empty (proxy for contention).", nil, nil),
	}
}

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.active
	ch <- c.idle
	ch <- c.acquiredTotal
	ch <- c.waitingTotal
}

func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(c.acquiredTotal, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.waitingTotal, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
}
