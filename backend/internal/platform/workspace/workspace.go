// Package workspace resolves the active workspace id for an incoming request.
//
// The workspace id is read from the `X-Workspace-Id` header (UUID). Behavior
// when the header is missing is controlled by `Config.RequireHeader`:
//
//   - RequireHeader=false (development default): fall back to a sentinel
//     workspace and signal `X-Workspace-Source: dev-sentinel` on the
//     response so callers can spot the fallback. Zero-config local dev.
//
//   - RequireHeader=true (production): reject with 401 + a uniform error
//     `{"error":{"code":"workspace_required",...}}`. Production deploys
//     must sit behind an upstream that injects the verified header; the
//     sentinel never fires.
//
// Malformed headers always return 400 regardless of mode — a header is
// present but unparseable, which is a client bug we want to surface.
//
// Authentication itself is owned by a separate repository / external
// service. This package is the consumer side: it trusts whatever populated
// the header.
package workspace

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/platform/apierror"
)

const (
	HeaderName             = "X-Workspace-Id"
	SourceResponseHeader   = "X-Workspace-Source"
	SourceDevSentinelValue = "dev-sentinel"
)

// DevWorkspaceID is the sentinel used when no header is present AND
// RequireHeader is false. It exists only to make local development
// zero-config. Production must run with RequireHeader=true so this UUID
// never appears in a real request path.
var DevWorkspaceID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type Config struct {
	// When true, a missing X-Workspace-Id header rejects the request with
	// 401. When false, the request falls through to the dev sentinel.
	// Default is false to keep local `make run` zero-config.
	RequireHeader bool `env:"WORKSPACE_REQUIRE_HEADER" envDefault:"false"`
}

// Recorder is the surface the workspace middleware uses to emit
// operational counters. The metrics package provides an implementation;
// this package depends only on the interface to keep prometheus out of
// the platform/workspace import graph. Pass nil to disable recording.
type Recorder interface {
	Request()       // any request hitting the middleware
	MissingHeader() // header was absent (regardless of mode)
	InvalidHeader() // header was present but not a UUID
	Sentinel()      // sentinel workspace was actually used (lax + missing)
}

type noopRecorder struct{}

func (noopRecorder) Request()       {}
func (noopRecorder) MissingHeader() {}
func (noopRecorder) InvalidHeader() {}
func (noopRecorder) Sentinel()      {}

type ctxKey struct{}

// FromContext returns the workspace id stamped by Middleware.
func FromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(ctxKey{}).(uuid.UUID)
	return v, ok
}

// WithWorkspaceID is exposed for tests so they can seed a workspace id
// into the context without going through the middleware.
func WithWorkspaceID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// Middleware returns a chi-compatible middleware that resolves the
// workspace id under the rules described on the package doc.
//
// `rec` may be nil — counters are simply not emitted in that case.
func Middleware(cfg Config, rec Recorder) func(http.Handler) http.Handler {
	if rec == nil {
		rec = noopRecorder{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.Request()
			raw := r.Header.Get(HeaderName)
			var id uuid.UUID

			switch {
			case raw == "" && cfg.RequireHeader:
				rec.MissingHeader()
				apierror.Write(w, slog.Default(),
					apierror.New(http.StatusUnauthorized, "workspace_required",
						"workspace header required"))
				return

			case raw == "":
				rec.MissingHeader()
				rec.Sentinel()
				id = DevWorkspaceID
				w.Header().Set(SourceResponseHeader, SourceDevSentinelValue)

			default:
				parsed, err := uuid.Parse(raw)
				if err != nil {
					rec.InvalidHeader()
					apierror.Write(w, slog.Default(),
						apierror.New(http.StatusBadRequest, "workspace_invalid",
							HeaderName+" must be a uuid"))
					return
				}
				id = parsed
			}

			next.ServeHTTP(w, r.WithContext(WithWorkspaceID(r.Context(), id)))
		})
	}
}
