// Package chat is the chat bounded context.
//
// It is wired as a self-contained module — domain, ports, application
// services, and adapters all live here. The Module type is the only public
// surface exposed to cmd/corsi; extracting this into its own service later
// means moving this directory and re-pointing the import.
//
// What it owns: credentials for OpenAI-compatible LLM endpoints (LiteLLM),
// a registry of configured agents, and the conversation log. It imports no
// other bounded context, and none imports it.
package chat

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/adapters/httpapi"
	"github.com/corsi/backend/internal/chat/adapters/llm"
	"github.com/corsi/backend/internal/chat/adapters/references"
	"github.com/corsi/backend/internal/chat/adapters/repo"
	"github.com/corsi/backend/internal/chat/adapters/tools"
	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
)

// Deps are the cross-cutting collaborators a Module needs from the host
// binary. The workspace middleware and the sealer are supplied as values
// rather than constructed here so their config stays at the composition
// root, next to every other env-driven decision.
type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	Sealer              *secrets.Sealer
	WorkspaceMiddleware func(http.Handler) http.Handler
	// Tools are capabilities implemented outside this module and offered to
	// its agents. Empty today, and the field is the whole point: it is the
	// seam a GitHub-backed tool (implemented in Integrations) or a
	// Finance-backed one (implemented by Finance exposing a capability)
	// arrives through, wired by cmd/corsi.
	//
	// Agents defines ports.Tool and receives implementations. It never
	// imports the packages that satisfy it, which is what keeps
	// `Module → Module` and `Module → Integration` from becoming real
	// imports the day the first real tool ships.
	Tools []ports.Tool
	// TurnMetrics counts how turns end, by terminal reason. Optional: a
	// deployment that wires nothing still gets the structured terminal log,
	// and loses only the aggregate. See ports.TurnMetrics.
	TurnMetrics ports.TurnMetrics
	// InternalTools admits the built-in diagnostic tools into the registry.
	//
	// False in production, and false is the zero value on purpose: a
	// deployment that configures nothing gets a catalogue with no
	// diagnostics in it. `system.echo` returns what it was given and is
	// useless to a user; offering it as a capability would be the module
	// describing itself dishonestly.
	//
	// The registry is the gate, not the interface. See tools.Options.
	InternalTools bool
	// ReferenceResolvers answer "what is this entity called", for the
	// subjects a conversation can be about.
	//
	// The sibling of Tools and supplied the same way: implemented by the
	// bounded context that owns the entity, handed over by the composition
	// root, and reached here only through an interface this module
	// declares. A build with none can resolve no subjects, and attaching
	// one is then refused rather than stored unresolved.
	ReferenceResolvers []ports.ContextReferenceResolver
	// DisablePromptCache turns provider-side prompt caching OFF.
	//
	// ── Why the field is negative when the behaviour is positive ───────
	// Because the zero value has to be the behaviour a deployment gets when
	// it configures nothing, and the answer this module wants to give in
	// that case is "cache". Naming the field `PromptCache` would make an
	// unconfigured Deps — including every existing test harness — silently
	// send the un-cached body, which is the more expensive of the two and
	// the one nobody would notice.
	//
	// Caching changes only how a prefix is BILLED, never what the model
	// reads; that is asserted in app/context_cache_test.go and on the bytes
	// in adapters/llm/cache_control_test.go. So the safe default is the
	// cheap one.
	DisablePromptCache bool
}

type Module struct {
	deps    Deps
	svc     *app.Service
	handler *httpapi.Handler
}

func New(deps Deps) *Module {
	txm := postgres.NewTxManager(deps.Pool)
	repos := repo.New(deps.Pool)
	client := llm.New(deps.Logger)
	// A bad tool definition or a duplicate name is a wiring mistake, and the
	// only honest response is to refuse to start: a process that boots with
	// half a registry serves an agent fewer capabilities than it was
	// configured with, and says nothing about it.
	registry := tools.MustNew(tools.Options{
		Internal: deps.InternalTools,
		Extra:    deps.Tools,
	})
	// Same failure rule as the tool registry: a duplicate type is a wiring
	// mistake worth stopping for, not something to discover when a chip
	// renders the wrong provider's label.
	referenceRegistry := references.MustNew(deps.ReferenceResolvers...)

	svc := app.NewService(repos, txm, client, deps.Sealer, registry, referenceRegistry, deps.Logger,
		app.WithPromptCache(!deps.DisablePromptCache),
		// Nil is a build that counts nothing and answers identically. See
		// ports.TurnMetrics.
		app.WithTurnMetrics(deps.TurnMetrics))
	h := httpapi.NewHandler(svc, deps.Logger)
	return &Module{deps: deps, svc: svc, handler: h}
}

// Register mounts the chat routes under /chat, guarded by the workspace
// middleware supplied via Deps.
func (m *Module) Register(r chi.Router) {
	r.Route("/chat", func(r chi.Router) {
		r.Use(m.deps.WorkspaceMiddleware)
		m.handler.Mount(r)
	})
}

// Service hands the application service to the composition root.
//
// ── Why this exists, and why it is not a widening of the boundary ──────
// HTTP is not the only way to drive a conversation. `Register` gives
// cmd/corsi one driving adapter — a chi router — and this gives it the
// seam any OTHER driving adapter needs: a messaging interface, a scheduled
// job, a CLI. The alternative was for each of those to re-open the
// repositories, the LLM client, the tool registry and the reference
// registry, which would mean a second construction of this module that
// could differ from the first in ways nobody would notice until a
// guarantee was missing on one surface.
//
// It is still cmd/corsi's privilege alone. Nothing in `internal/` may call
// this: a module or an integration that did would be creating the very
// import the architecture forbids, and the direction of that arrow is
// checked by reading the import list, not by hiding the method.
//
// What a caller receives is the SAME service the HTTP handler holds, with
// the same tool registry, the same grants, the same budget, the same
// receipts and the same prompt caching. That identity is the point: a turn
// is a turn regardless of what asked for it.
func (m *Module) Service() *app.Service { return m.svc }
