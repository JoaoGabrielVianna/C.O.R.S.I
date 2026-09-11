// Package tools is the driven adapter that answers "which tools exist".
//
// ── Why the registry is code ───────────────────────────────────────────
// A tool's identity is its name, its contract is its schema, and its
// behaviour is a function. All three ship with the binary. Putting them in
// a table would create a description that a deploy can contradict: a row
// promising `github.repository.read` in a build that no longer implements
// it is worse than no row, because the interface would offer it and the
// turn would fail. So the definitions live here, and the database stores
// only the two things code cannot know — who was granted what, and what
// actually ran.
//
// ── Why it is composed and not global ──────────────────────────────────
// New() takes the extra tools the host binary wants to add. That is the
// seam the first real tool arrives through: a GitHub-backed tool is
// implemented in Integrations, a Finance-backed one is implemented by
// Finance exposing a capability, and cmd/corsi hands either to this
// constructor. Agents never imports either — it declares ports.Tool and
// receives implementations. A package-level global would have made that
// impossible without an import in the wrong direction.
package tools

import (
	"fmt"
	"sort"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Registry is an immutable, in-memory index of tools by name.
//
// Immutable after construction, and that is load-bearing: "which tools
// exist" must not depend on which requests have already been served. There
// is no Register method for the same reason.
type Registry struct {
	byName map[domain.ToolName]ports.Tool
	defs   []domain.ToolDefinition
}

// Options is what the host binary decides about the catalogue.
type Options struct {
	// Internal admits the tools marked internal in their own definition.
	//
	// ── Why the default is off, and why that is the whole mechanism ────
	// A tool marked internal exists to exercise the machinery, not to be
	// useful — `system.echo` returns what it was given. Offering it in
	// production as though it were a capability would be the module
	// describing itself dishonestly, which is the one thing every other
	// decision here is arranged to prevent.
	//
	// The gate is the REGISTRY, not the interface. When this is false the
	// tool is not in the catalogue at all, and every existing rule then
	// does the right thing without being told about it:
	//
	//	Definitions()  omits it   → no surface can render it
	//	Lookup()       fails      → AuthorizeTool answers 404
	//	the turn's read skips it  → a stale grant is reported, never used
	//	the executor's first gate → tool_not_found
	//
	// That is why there is no `if name == "system.echo"` anywhere in this
	// repository, and why there must never be one: a name repeated across
	// layers is a rule that will eventually be enforced in only some of
	// them.
	//
	// Zero value is production. A deployment that configures nothing gets
	// the safe answer, and the flag is what a developer opts INTO.
	Internal bool
	// Extra are capabilities implemented outside this package and supplied
	// by the host: a GitHub-backed tool from Integrations, a Finance-backed
	// one from a module capability.
	//
	// They pass through the same filter. The authority is the definition,
	// never the provenance: a host-supplied tool that declares itself
	// internal is a diagnostic wherever it was implemented, and a rule that
	// applied to built-ins only would be a rule with an exception nobody
	// would remember on the day it mattered.
	Extra []ports.Tool
}

// New builds the registry from the built-in tools plus whatever the host
// supplies.
//
// It returns an error rather than panicking on a bad tool because the
// caller is a composition root that can report the problem properly — and
// because a duplicate name is a wiring mistake worth naming, not a mystery
// crash at start-up. A malformed definition or a duplicate name refuses the
// whole registry: half a registry is a system that silently offers fewer
// capabilities than it was configured with.
func New(o Options) (*Registry, error) {
	r := &Registry{byName: make(map[domain.ToolName]ports.Tool)}

	all := make([]ports.Tool, 0, len(builtins)+len(o.Extra))
	// One filter, over every tool, reading the definition and nothing else.
	// A name never appears in this decision — see the note on Options.
	for _, t := range append(append([]ports.Tool{}, builtins...), o.Extra...) {
		if t.Definition().Internal && !o.Internal {
			continue
		}
		all = append(all, t)
	}

	for _, t := range all {
		def := t.Definition()
		if err := def.Validate(); err != nil {
			return nil, fmt.Errorf("tool registry: invalid definition: %w", err)
		}
		if _, exists := r.byName[def.Name]; exists {
			return nil, fmt.Errorf("tool registry: duplicate tool name %q", def.Name)
		}
		r.byName[def.Name] = t
	}

	r.defs = make([]domain.ToolDefinition, 0, len(r.byName))
	for _, t := range r.byName {
		r.defs = append(r.defs, t.Definition())
	}
	// Name order, computed once. Every surface that lists tools — the model's
	// declaration, the settings page, the report — reads this slice, so a
	// stable order here is what keeps two identical turns building an
	// identical request body.
	sort.Slice(r.defs, func(i, j int) bool { return r.defs[i].Name < r.defs[j].Name })

	return r, nil
}

// MustNew is New for the composition root, where a wiring error is fatal
// anyway and there is nothing useful to do but stop.
func MustNew(o Options) *Registry {
	r, err := New(o)
	if err != nil {
		panic(err)
	}
	return r
}

func (r *Registry) Lookup(name domain.ToolName) (ports.Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Definitions returns the catalogue in name order. The slice is rebuilt per
// call so a caller cannot reorder the registry's own copy by sorting it.
func (r *Registry) Definitions() []domain.ToolDefinition {
	out := make([]domain.ToolDefinition, len(r.defs))
	copy(out, r.defs)
	return out
}

// builtins are the tools this binary ships with.
//
// Exactly one today, and it is marked internal because it is a test
// instrument rather than a product capability — see echo.go. So the default
// registry, the one a production deployment builds, is EMPTY. Real
// capabilities arrive through Options.Extra, implemented by whoever owns
// them.
var builtins = []ports.Tool{
	echoTool{},
}
