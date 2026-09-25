// Package closet is the Closet bounded context: the operator's real
// wardrobe, the photographs of each piece, and the looks they are combined
// into.
//
// It is self-contained — domain, ports, application service and adapters
// all live here — and the Module type is its only public surface.
//
// ── What this module does NOT offer, and why that is a decision ────────
// No Tools(). Every other module wired into this binary hands the
// composition root a set of values satisfying the Agents module's
// ports.Tool; this one hands over nothing, because this sprint builds a
// wardrobe and not a stylist. The seam is not missing — it is the same seam
// Job Radar and Threads use, and adding it later is a `tools` subpackage
// and one line in main.go. What would be wrong is shipping capabilities for
// an agent that does not exist yet: an unused capability in the catalogue
// is one the operator has to reason about and cannot revoke usefully.
//
// It imports no other bounded context, and nothing external. The PNGs
// arrive already cut out from whatever tool the operator used outside this
// product; there is no image processing here, no vendor and no credential.
package closet

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/closet/adapters/httpapi"
	"github.com/corsi/backend/internal/closet/adapters/repo"
	"github.com/corsi/backend/internal/closet/app"
)

type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	WorkspaceMiddleware func(http.Handler) http.Handler
}

type Module struct {
	deps    Deps
	svc     *app.Service
	handler *httpapi.Handler
}

func New(deps Deps) *Module {
	repos := repo.New(deps.Pool)
	svc := app.NewService(repos.Items, repos.Images, repos.Assets, repos.Looks, deps.Logger)
	return &Module{deps: deps, svc: svc, handler: httpapi.NewHandler(svc, deps.Logger)}
}

// Register mounts the Closet routes under /closet.
//
// Every route sits behind the workspace middleware, including the one that
// serves image bytes. That is the whole reason the frontend fetches an
// asset instead of putting its URL in an `<img src>`: a tag cannot send
// `X-Workspace-Id`, and the alternative — exempting the asset route so a
// tag could reach it — would make the one address that returns raw bytes
// the one address with weaker scoping.
func (m *Module) Register(r chi.Router) {
	r.Route("/closet", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}

// Service exposes the application layer to the composition root.
//
// Used by the integration suite, and the seam a future stylist's tools
// would be built on. It is a method rather than an exported field so the
// module stays the only thing that constructs it.
func (m *Module) Service() *app.Service { return m.svc }
