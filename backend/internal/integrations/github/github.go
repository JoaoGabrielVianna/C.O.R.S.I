// Package github is the GitHub integration.
//
// ── What an Integration is, here ───────────────────────────────────────
// The adaptation layer between C.O.R.S.I. and one external system. It owns
// the protocol, the credential, the dialect and the error vocabulary of
// that system, and it hands back a normalized contract. It owns no business
// rule, answers no product question on its own, and knows nothing about the
// modules that consume it.
//
// ── The two things it hands out ────────────────────────────────────────
//
//	Register(r)  an HTTP surface for MANAGING the connection. Used by the
//	             Integrations page. No agent ever calls it.
//	Tools()      capabilities implementing the port Agents declares. Handed
//	             to chat.Deps.Tools by cmd/corsi. This is how an agent
//	             reaches GitHub, and it is the only way.
//
// ── The dependency direction ───────────────────────────────────────────
//
//	Agents  →  ports.Tool  ←  GitHub Integration  →  GitHub
//	                                  ↓
//	                              Platform (secrets, workspace, postgres)
//
// Agents cannot name GitHub: it declares an interface and receives values.
// GitHub cannot name Agents: it satisfies that interface and calls nothing
// in the chat package. The two meet in cmd/corsi, which is the one place
// allowed to know both.
package github

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/integrations/github/adapters/api"
	"github.com/corsi/backend/internal/integrations/github/adapters/httpapi"
	"github.com/corsi/backend/internal/integrations/github/adapters/repo"
	"github.com/corsi/backend/internal/integrations/github/app"
	"github.com/corsi/backend/internal/integrations/github/ports"
	"github.com/corsi/backend/internal/integrations/github/tools"
	"github.com/corsi/backend/internal/platform/secrets"
)

// Deps are the cross-cutting collaborators the host binary supplies.
//
// The sealer is passed in rather than constructed, for the same reason the
// chat module takes one: the key is deployment configuration, and every
// env-driven decision belongs at the composition root.
type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	Sealer              *secrets.Sealer
	WorkspaceMiddleware func(http.Handler) http.Handler
	// API replaces the real GitHub client. Nil means the real one, which is
	// what production gets. It exists so a test can drive the whole stack —
	// tools, service, repositories, Postgres — against a fake HTTP server
	// without a global switch a deployment could also read.
	API ports.API
}

type Module struct {
	deps    Deps
	svc     *app.Service
	handler *httpapi.Handler
}

func New(deps Deps) *Module {
	client := deps.API
	if client == nil {
		client = api.New(deps.Logger)
	}
	repos := repo.New(deps.Pool)
	svc := app.NewService(repos.Connections, repos.Repositories, client, deps.Sealer, deps.Logger)
	return &Module{
		deps:    deps,
		svc:     svc,
		handler: httpapi.NewHandler(svc, deps.Logger),
	}
}

// Register mounts the management surface under /integrations/github.
//
// ── Why /integrations and not /chat/integrations ───────────────────────
// Because this is not part of the Agents module's API, and mounting it
// there would make the URL say otherwise. A connection outlives any agent,
// belongs to the workspace rather than to a conversation, and would still
// be managed here if Agents were deleted tomorrow.
func (m *Module) Register(r chi.Router) {
	r.Route("/integrations/github", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}

// Tools returns the capabilities this integration offers to agents.
//
// ── Why the same values every time, built once ─────────────────────────
// They are stateless: each holds a pointer to the service and reads the
// workspace from the call's own context. That is what lets a single
// registry, built at start-up and immutable afterwards, serve every
// workspace — the alternative would be a registry whose contents depend on
// who is asking, which is exactly what the Agents registry refuses to be.
func (m *Module) Tools() []chatports.Tool {
	return tools.New(m.svc)
}
