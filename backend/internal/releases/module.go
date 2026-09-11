// Package releases is the release-history bounded context.
//
// It records what each module of this installation shipped, and when. Like
// finance and chat it is self-contained — domain, ports, application
// service and adapters all live here — and the Module type is its only
// public surface.
//
// ── Why this is a bounded context and not part of Platform ─────────────
// Platform's admission rule asks whether a capability is technical,
// reusable across domains, and free of business rules. This is none of
// those: it has its own entities, its own schema, its own invariants
// (SemVer, immutability, what "current" means), and it renders a page a
// person reads. Platform explicitly holds nothing with standalone user
// value. Putting release history there would also mean Platform storing
// rows named after Agents and Finance, which is the direction the
// dependency rules forbid.
//
// It imports no other bounded context, and none imports it. It knows the
// *names* of modules only as data in its own tables — never as a Go
// import, which is what keeps `Module → Module` from becoming real.
package releases

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/releases/adapters/httpapi"
	"github.com/corsi/backend/internal/releases/adapters/repo"
	"github.com/corsi/backend/internal/releases/app"
)

// Deps mirrors the other modules: cross-cutting collaborators are supplied
// by the composition root rather than constructed here, so every
// env-driven decision stays in one place.
type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	WorkspaceMiddleware func(http.Handler) http.Handler
}

type Module struct {
	deps    Deps
	handler *httpapi.Handler
}

func New(deps Deps) *Module {
	repos := repo.New(deps.Pool)
	svc := app.NewService(repos.Releases, deps.Logger)
	return &Module{deps: deps, handler: httpapi.NewHandler(svc, deps.Logger)}
}

// Register mounts the release routes under /releases.
//
// The workspace middleware is applied even though nothing here is scoped
// by workspace. Keeping every module behind the same gate means the header
// policy has one shape across the API; exempting this one would create a
// surface whose auth behavior differs from its neighbours for a reason a
// future reader would have to rediscover.
func (m *Module) Register(r chi.Router) {
	r.Route("/releases", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}
