// Package threads is the Threads bounded context: units of content work —
// an idea, a draft, a finished post — and the lifecycle they move through.
//
// It is self-contained: domain, ports, application service and adapters all
// live here, and the Module type is its only public surface, with two
// documented exceptions — Tools() and ReferenceResolvers(), which hand the
// composition root values satisfying interfaces the Agents module declares.
// That is the same seam GitHub and Job Radar use, and it is what lets an
// agent operate this module without either package importing the other.
//
// It imports no other bounded context. The `tools` subpackage imports the
// chat module's PORTS, which is dependency inversion rather than the
// forbidden `Module → Module` direction — see the note at the top of
// internal/threads/tools/tools.go.
//
// ── Why there are no HTTP routes ───────────────────────────────────────
// Because this sprint deliberately ships no Threads screen, and a route
// with no caller is a surface nobody is testing. The interface to Threads
// today is a conversation with an authorized agent, which reaches the same
// application service through the same tools. That is a DECISION and not a
// gap: the module is complete without it, and adding routes the day a
// screen exists is a Register method and a handler, with the domain, the
// schema and the application layer already in place and already proven.
package threads

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/threads/adapters/repo"
	"github.com/corsi/backend/internal/threads/app"
	"github.com/corsi/backend/internal/threads/tools"
)

type Deps struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

type Module struct {
	deps Deps
	svc  *app.Service
}

func New(deps Deps) *Module {
	repos := repo.New(deps.Pool)
	return &Module{deps: deps, svc: app.NewService(repos.Threads, deps.Logger)}
}

// Tools exposes this module's capabilities to whoever composes the binary.
//
// ── Why the module hands these over rather than registering them ───────
// Because registering would require knowing the registry, and the registry
// belongs to Agents. The composition root takes these values and passes
// them to chat.Deps.Tools, exactly as it does for GitHub and Job Radar.
// This module never learns that agents exist; the Agents module never
// learns that Threads does. The only place both names appear is
// cmd/corsi/main.go.
func (m *Module) Tools() []ports.Tool { return tools.New(m.svc) }

// ReferenceResolvers exposes this module's entity types to the Chat's
// reference registry.
//
// Separate from Tools() and deliberately so: a tool is something an agent
// may DO, a reference is something a conversation may be ABOUT. Threads
// offers both over the same application service, and the composition root
// hands each to the collaborator that asked for it.
func (m *Module) ReferenceResolvers() []ports.ContextReferenceResolver {
	return []ports.ContextReferenceResolver{tools.NewReferenceResolver(m.svc)}
}
