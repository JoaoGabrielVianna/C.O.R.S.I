// Package finance is the finance bounded context.
//
// It is wired as a self-contained module — domain, ports, application
// services, and adapters all live here. The Module type is the only public
// surface exposed to cmd/corsi; extracting this into its own service later
// means moving this directory and re-pointing the import.
package finance

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/finance/adapters/httpapi"
	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/tools"
	"github.com/corsi/backend/internal/platform/postgres"
)

// Deps are the cross-cutting collaborators a Module needs from the host
// binary. The workspace middleware is supplied as a value rather than
// constructed inside the module so its config (e.g.
// `WORKSPACE_REQUIRE_HEADER`) stays at the composition root.
type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	WorkspaceMiddleware func(http.Handler) http.Handler
	// ReportingTimezone is the zone a finance PERIOD is cut in: where
	// "hoje" starts and where "este mês" ends.
	//
	// It exists because the screens have always cut their windows in the
	// browser's zone and a conversation has no browser — see the argument in
	// platform/config. Nil falls back to UTC rather than to the process's
	// local zone: a wrong answer that is obviously wrong beats one that
	// depends on which machine the binary happens to be running on.
	ReportingTimezone *time.Location
}

type Module struct {
	deps    Deps
	svc     *app.Service
	handler *httpapi.Handler
	loc     *time.Location
}

func New(deps Deps) *Module {
	txm := postgres.NewTxManager(deps.Pool)
	repos := repo.New(deps.Pool)
	// The reporting zone is resolved FIRST and handed to everything that
	// crosses between a month and an instant. One value, read once, so the
	// service and the capabilities cannot disagree about which month an
	// instant belongs to.
	loc := deps.ReportingTimezone
	if loc == nil {
		loc = time.UTC
	}
	svc := app.NewService(repos, txm, deps.Logger, loc)
	h := httpapi.NewHandler(svc, deps.Logger)
	return &Module{deps: deps, svc: svc, handler: h, loc: loc}
}

// Tools exposes this module's capabilities to whoever composes the binary.
//
// ── Why the module hands these over rather than registering them ───────
// Because registering would require knowing the registry, and the registry
// belongs to Agents. The composition root takes these values and passes
// them to chat.Deps.Tools, exactly as it does for GitHub, Job Radar and
// Threads. This module never learns that agents exist; the Agents module
// never learns that Finance does. The only place both names appear is
// cmd/corsi/main.go.
//
// They reach the SAME application service the HTTP handler above holds.
// There is no agent-specific finance rule anywhere in this repository, and
// no second path to the ledger.
func (m *Module) Tools() []chatports.Tool { return tools.New(m.svc, m.loc) }

// ReferenceResolvers exposes this module's entity types to the Chat's
// reference registry.
//
// Separate from Tools() and deliberately so: a tool is something an agent
// may DO, a reference is something a conversation may be ABOUT.
func (m *Module) ReferenceResolvers() []chatports.ContextReferenceResolver {
	return []chatports.ContextReferenceResolver{tools.NewReferenceResolver(m.svc, m.loc)}
}

// Register mounts the finance routes under /finance, guarded by the
// workspace middleware supplied via Deps.
func (m *Module) Register(r chi.Router) {
	r.Route("/finance", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}
