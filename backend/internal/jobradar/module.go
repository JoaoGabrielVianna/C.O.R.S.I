// Package jobradar is the Job Radar bounded context: the opportunities of a
// job search and the pipeline they move through.
//
// It is self-contained — domain, ports, application service and adapters
// all live here — and the Module type is its only public surface, with one
// documented exception: Tools(), which hands the composition root a set of
// values satisfying an interface the Agents module declares. That is the
// same seam the GitHub integration uses, and it is what lets an agent
// operate this module without either package importing the other.
//
// It imports no other bounded context. The `tools` subpackage imports the
// chat module's PORTS, which is dependency inversion rather than the
// forbidden `Module → Module` direction — see the note at the top of
// internal/jobradar/tools/tools.go.
package jobradar

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/jobradar/adapters/httpapi"
	"github.com/corsi/backend/internal/jobradar/adapters/repo"
	"github.com/corsi/backend/internal/jobradar/app"
	"github.com/corsi/backend/internal/jobradar/tools"
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
	svc := app.NewService(repos.Opportunities, repos.Companies, deps.Logger)
	return &Module{deps: deps, svc: svc, handler: httpapi.NewHandler(svc, deps.Logger)}
}

// Register mounts the Job Radar routes under /job-radar.
//
// The path uses a hyphen because that is what every other URL in this
// product does. It is deliberately NOT the tool namespace, which is
// `job_radar` — a tool name may not contain a hyphen (see
// domain.ValidToolName), and bending one of the two to match the other
// would mean changing a rule to satisfy a cosmetic wish.
func (m *Module) Register(r chi.Router) {
	r.Route("/job-radar", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}

// Tools exposes this module's capabilities to whoever composes the binary.
//
// ── Why the module hands these over rather than registering them ───────
// Because registering would require knowing the registry, and the registry
// belongs to Agents. The composition root takes these values and passes
// them to chat.Deps.Tools, exactly as it does for GitHub. This module never
// learns that agents exist; the Agents module never learns that Job Radar
// does. The only place both names appear is cmd/corsi/main.go.
func (m *Module) Tools() []ports.Tool { return tools.New(m.svc) }

// ReferenceResolvers exposes this module's entity types to the Chat's
// reference registry.
//
// Separate from Tools() and deliberately so: a tool is something an agent
// may DO, a reference is something a conversation may be ABOUT. Job Radar
// offers both over the same application service, and the composition root
// hands each to the collaborator that asked for it.
func (m *Module) ReferenceResolvers() []ports.ContextReferenceResolver {
	return []ports.ContextReferenceResolver{tools.NewReferenceResolver(m.svc)}
}
