// Package palace is the Palace bounded context: the operator's
// persistent memory and the structures that organise it.
//
// It is self-contained: domain, ports, application service and adapters
// all live here, and the Module type is its only public surface, with one
// documented exception: Tools(), which hands the composition root values
// satisfying an interface the Agents module declares. That is the same
// seam GitHub, Job Radar, Threads and Finance use, and it is what lets an
// agent operate this module without either package importing the other.
//
// It imports no other bounded context. The `tools` subpackage imports the
// chat module's PORTS, which is dependency inversion rather than the
// forbidden `Module → Module` direction.
//
// ══════════════════════════════════════════════════════════════════════
//
//	Palace Memory is not Agent Memory. Palace Source is not Agent Source
//
// ══════════════════════════════════════════════════════════════════════
//
//	chat.memories        The AGENT's memory: a short fact one agent keeps
//	                     between conversations, scoped to that agent and
//	                     injected into the prompt under a token budget. It
//	                     is configuration of how an assistant behaves.
//
//	palace.memories      THIS. The OPERATOR's knowledge, scoped to the
//	                     workspace, read on demand through palace.memory.*
//	                     and never injected by budget.
//
// There is no synchronisation, no conversion and no bridge between them,
// in either direction, and adding one would collapse a distinction the
// whole design rests on. The same goes for `chat.agent_sources` and
// `palace.sources`. See domain/palace.go, which states the boundary in
// full.
//
// ── The two surfaces, and what each one is for ─────────────────────────
//
//	tools     THE CONVERSATIONAL SURFACE. Twenty-four capabilities an
//	          agent can be granted one at a time, and the only way
//	          anything in this context is WRITTEN. Every grant is a row
//	          the operator can see and revoke.
//
//	httpapi   THE OPERATOR'S OWN PROJECTIONS. Read only, no exceptions:
//	          screens that show the operator their own record. It creates
//	          nothing, changes nothing and deletes nothing.
//
// Until this slice the second one did not exist, and its absence was a
// recorded decision rather than a gap: a route with no caller is a surface
// nobody is testing. It exists now because a caller does, and it cost what
// was predicted — a Register method and a handler, with the domain, the
// schema and the application layer already in place and already proven.
//
// Both reach the SAME application service. There is no second set of rules
// about what may be read, and no SQL anywhere but in the repositories.
//
// ── Why the HTTP surface withholds MORE than the tools do ──────────────
// A capability answering "onde foi que eu guardei aquilo" is a targeted
// question from the operator. A projection is a broad, ambient rendering
// of a whole area, and it is the surface most likely to be on a screen
// somebody else can see. So it is stricter in two ways the tools are not:
// highly sensitive content is never returned and cannot be asked for, and
// something filed in a withheld room is withheld with it. See
// app/surface.go. None of that changes what a tool sees.
//
// ── Tool audit and UI access are DIFFERENT observability models ────────
// A `palace.*` capability is Confidential, so `chat.tool_calls` records
// the capability, the round, the outcome, the duration and the error code,
// and never the arguments or the result. That is an audit trail of what an
// AGENT did, and the redaction is what makes it safe to keep.
//
// A read through this adapter does not travel that trail and is not meant
// to. It is the operator reading their own database through their own
// screen, and it is covered by the platform's HTTP metrics and logs like
// every other route in this product.
//
// This slice introduces NO Palace-specific access audit, and the absence
// is a decision rather than an oversight. Recording every screen read of a
// personal knowledge base would build a second, unredacted record of what
// the operator looked at and when, inside the one context whose entire
// design is about withholding content from records. If that is ever
// wanted, it is its own decision with its own questions about retention.
// Nothing here changes the behaviour of the existing audit.
package palace

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/palace/adapters/httpapi"
	"github.com/corsi/backend/internal/palace/adapters/repo"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/tools"
)

type Deps struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// WorkspaceMiddleware guards the read routes. Supplied by the
	// composition root rather than built here, because every bounded
	// context must enforce the SAME workspace rules with the SAME
	// counters: a module that constructed its own would be a second
	// opinion about what an absent header means.
	//
	// Required only by Register. A module wired without it still serves
	// its capabilities, which is what a deployment that mounts no routes
	// would want.
	WorkspaceMiddleware func(http.Handler) http.Handler
}

type Module struct {
	deps    Deps
	svc     *app.Service
	handler *httpapi.Handler
}

// New builds the module.
//
// ── Why MustNewService is safe here ────────────────────────────────────
// The application service refuses an incomplete Deps, which is what keeps
// a missing repository from becoming a nil dereference three layers away.
// Here the Deps is built in this function out of repo.New, which fills
// every field, so a failure would be a bug in the two lines below rather
// than anything a caller can cause. That is the same reasoning the tool
// registry's MustNew uses at the composition root.
func New(deps Deps) *Module {
	repos := repo.New(deps.Pool)
	svc := app.MustNewService(app.Deps{
		Rooms:      repos.Rooms,
		Memories:   repos.Memories,
		Artifacts:  repos.Artifacts,
		Items:      repos.Items,
		Sources:    repos.Sources,
		Provenance: repos.Provenance,
		Relations:  repos.Relations,
		Sessions:   repos.Sessions,
		Clock:      repos.Clock,
		Logger:     deps.Logger,
	})
	return &Module{deps: deps, svc: svc, handler: httpapi.NewHandler(svc, deps.Logger)}
}

// Register mounts the Palace read routes under /palace, guarded by the
// workspace middleware supplied via Deps.
//
// ── Why a missing middleware stops the boot ────────────────────────────
// Because chi accepts a nil middleware without complaint and dereferences
// it on the first request, which turns a wiring mistake into a 500 in
// production, far from the line that caused it. Worse, the failure mode of
// a workspace guard that is not there is not an error at all: it is a
// handler serving a request nobody scoped. Refusing at composition is the
// same reasoning MustNewService already applies to a missing repository.
func (m *Module) Register(r chi.Router) {
	if m.deps.WorkspaceMiddleware == nil {
		panic("palace: WorkspaceMiddleware is required to mount the read routes")
	}
	r.Route("/palace", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}

// Tools exposes this module's capabilities to whoever composes the
// binary.
//
// ── Why the module hands these over rather than registering them ───────
// Because registering would require knowing the registry, and the
// registry belongs to Agents. The composition root takes these values and
// passes them to chat.Deps.Tools, exactly as it does for GitHub, Job
// Radar, Threads and Finance. This module never learns that agents exist;
// the Agents module never learns that Palace does. The only place both
// names appear is cmd/corsi/main.go.
func (m *Module) Tools() []ports.Tool { return tools.New(m.svc) }
