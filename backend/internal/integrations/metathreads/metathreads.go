// Package metathreads is the Meta Threads integration.
//
// ══════════════════════════════════════════════════════════════════════
//
//	This is NOT internal/threads
//
// ══════════════════════════════════════════════════════════════════════
//
//	internal/threads              C.O.R.S.I. Threads. A MODULE. Our own
//	                              bounded context of content work — ideas,
//	                              drafts, review, published, archived —
//	                              stored in our Postgres and written by
//	                              threads.thread.* .
//
//	internal/integrations/
//	  metathreads                 THIS. An INTEGRATION with Meta's social
//	                              network. Holds one OAuth credential per
//	                              workspace and READS the operator's
//	                              published posts and their metrics
//	                              through meta_threads.* . Stores no
//	                              content and writes nothing to Meta.
//
// The two never touch. This package has no import of internal/threads and
// must never gain one: an agent that can read both is what makes the
// comparison possible, and that agent is the only place the two meet.
//
// ── What an Integration is, here ───────────────────────────────────────
// The adaptation layer between C.O.R.S.I. and one external system. It owns
// the protocol, the credential, the dialect and the error vocabulary of
// that system, and hands back a normalized contract. It owns no business
// rule, answers no product question on its own, and knows nothing about the
// modules that consume it.
//
// ── The two things it hands out ────────────────────────────────────────
//
//	Register(r)  an HTTP surface for MANAGING the connection. No agent ever
//	             calls it.
//	Tools()      read capabilities implementing the port Agents declares,
//	             handed to chat.Deps.Tools by cmd/corsi. This is how an
//	             agent reaches Meta Threads, and it is the only way.
package metathreads

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/integrations/metathreads/adapters/api"
	"github.com/corsi/backend/internal/integrations/metathreads/adapters/httpapi"
	"github.com/corsi/backend/internal/integrations/metathreads/adapters/repo"
	"github.com/corsi/backend/internal/integrations/metathreads/app"
	"github.com/corsi/backend/internal/integrations/metathreads/ports"
	"github.com/corsi/backend/internal/integrations/metathreads/tools"
	"github.com/corsi/backend/internal/platform/secrets"
)

type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	Sealer              *secrets.Sealer
	WorkspaceMiddleware func(http.Handler) http.Handler
	// App is the deployment's Meta app. An unconfigured one still builds a
	// working module: the tools are registered, every connection attempt
	// refuses with an actionable 503, and every read reports that the
	// workspace is not connected. That is a truer description of the system
	// than hiding the capability.
	App app.AppConfig
	// API replaces the real Meta client. Nil means the real one, which is
	// what production gets. It exists so a test can drive the whole stack —
	// tools, service, repository, Postgres — against a fake HTTP server
	// without a Meta app and without a global switch a deployment could also
	// read.
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
	svc := app.NewService(repos.Connections, client, deps.Sealer, deps.App, deps.Logger)
	return &Module{deps: deps, svc: svc, handler: httpapi.NewHandler(svc, deps.Logger)}
}

// Register mounts the management surface under /integrations/meta-threads.
//
// The path uses a hyphen because that is what every other URL in this
// product does. It is deliberately NOT the tool namespace, which is
// `meta_threads` — a tool name may not contain a hyphen (see
// chat/domain.ValidToolName), and bending one to match the other would mean
// changing a rule to satisfy a cosmetic wish. Job Radar made the same
// choice for the same reason.
func (m *Module) Register(r chi.Router) {
	r.Route("/integrations/meta-threads", func(r chi.Router) {
		// ── Two groups, and the split is the security boundary ───────
		//
		// PUBLIC: the endpoints META calls. They carry no workspace header
		// because Meta has never heard of a workspace, and they are
		// authenticated by the HMAC signature on their body instead. They
		// must sit OUTSIDE the workspace middleware or they would 401 in
		// any deployment that requires the header — production — and a
		// deletion request would go silently unhonoured.
		//
		// EVERYTHING ELSE: the operator's own management surface, behind
		// the workspace guard exactly as before.
		m.handler.MountPublic(r)

		r.Group(func(r chi.Router) {
			r.Use(m.deps.WorkspaceMiddleware)
			m.handler.Mount(r)
		})
	})
}

// Tools exposes this integration's capabilities to whoever composes the
// binary. Every one is a READ.
func (m *Module) Tools() []chatports.Tool { return tools.New(m.svc) }

// Configured reports whether this deployment can begin an OAuth flow. The
// composition root uses it to warn once at start-up rather than letting the
// first operator discover it as a failed connect.
func (m *Module) Configured() bool { return m.svc.Configured() }
