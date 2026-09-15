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
// ── Why there are no HTTP routes ───────────────────────────────────────
// Because this sprint ships no Palace screen, and a route with no caller
// is a surface nobody is testing. The interface to Palace today is a
// conversation with an authorized agent, which reaches the same
// application service through the same tools. That is a DECISION and not
// a gap: the module is complete without it, and adding routes the day a
// screen exists is a Register method and a handler, with the domain, the
// schema and the application layer already in place and already proven.
// Threads made the same call for the same reason.
package palace

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/palace/adapters/repo"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/tools"
)

type Deps struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

type Module struct {
	deps Deps
	svc  *app.Service
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
	return &Module{deps: deps, svc: svc}
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
