// Package references is the driven adapter that answers "who can resolve
// this kind of entity".
//
// ── Why a registry and not a switch ────────────────────────────────────
// A `switch ref.Type` in the application layer would put the name of every
// bounded context inside Agents — the exact coupling the module is built to
// avoid, and the one the tool registry already refused for the same reason.
// Here the providers register themselves, Agents holds an interface, and
// the arrow between them exists only in cmd/corsi.
//
// It is deliberately the smaller sibling of adapters/tools.Registry: same
// shape, same immutability, same composition seam. The second provider adds
// a line to the composition root and nothing else.
package references

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Registry maps a reference type to the provider that owns it.
//
// Immutable after construction, for the reason the tool registry gives:
// "which types can be referenced" must not depend on which requests have
// already been served.
type Registry struct {
	byType map[domain.ContextReferenceType]ports.ContextReferenceResolver
	// hydrators holds the subset of resolvers that also declare a freshness
	// policy for a type.
	//
	// ── Why a second map and not a type assertion at call time ─────────
	// Because the assertion would then live in the application layer, which
	// would make Agents ask "is this provider the hydrating kind?" on every
	// turn — a question about an implementation, asked by the layer that is
	// specifically not allowed to know about implementations. Resolved once,
	// here, at composition, where knowing which concrete resolvers exist is
	// the entire job.
	hydrators map[domain.ContextReferenceType]ports.ContextReferenceHydrator
}

// New builds the registry from the resolvers the host supplies.
//
// A duplicate type refuses the whole registry rather than letting one
// provider silently shadow another: two owners for one entity type is a
// wiring mistake worth naming at start-up, not a mystery at runtime.
func New(resolvers ...ports.ContextReferenceResolver) (*Registry, error) {
	r := &Registry{
		byType:    make(map[domain.ContextReferenceType]ports.ContextReferenceResolver),
		hydrators: make(map[domain.ContextReferenceType]ports.ContextReferenceHydrator),
	}
	for _, resolver := range resolvers {
		if resolver == nil {
			continue
		}
		// Hydration is opt-in and additive: a resolver that does not
		// implement the interface has every one of its types treated as
		// on-demand, which is what every type was before hydration existed.
		hydrator, hydrates := resolver.(ports.ContextReferenceHydrator)
		for _, t := range resolver.Types() {
			if !t.Valid() {
				return nil, fmt.Errorf("reference registry: %q is not a valid reference type", t)
			}
			if _, exists := r.byType[t]; exists {
				return nil, fmt.Errorf("reference registry: duplicate resolver for %q", t)
			}
			r.byType[t] = resolver
			if !hydrates {
				continue
			}
			// A resolver may implement the interface and still decline a
			// particular type — a provider with two entity types where only
			// one is small enough to carry every turn.
			policy, declared := hydrator.HydrationPolicy(t)
			if !declared || policy.Freshness == ports.FreshnessOnDemand {
				continue
			}
			// A policy that names no capability is refused at COMPOSITION
			// rather than denied at runtime. Both are safe — see
			// Service.hydrationAuthorized, which denies it either way — but a
			// wiring mistake that silently turns a declared policy into a
			// permanently unauthorized one is a feature that looks configured
			// and never works. Naming it at start-up is the cheaper failure.
			if policy.Capability == "" {
				return nil, fmt.Errorf(
					"reference registry: %q declares freshness %q and names no capability; "+
						"hydration is authorized by a tool grant and a policy without one could never be authorized",
					t, policy.Freshness)
			}
			r.hydrators[t] = hydrator
		}
	}
	return r, nil
}

// MustNew is New for the composition root, where a wiring error is fatal.
func MustNew(resolvers ...ports.ContextReferenceResolver) *Registry {
	r, err := New(resolvers...)
	if err != nil {
		panic(err)
	}
	return r
}

// Resolve looks one reference up through its provider.
//
// ── The three answers, and why they are told apart here ────────────────
//
//	unknown type   → the build has no resolver for it. A client ahead of
//	                 the server, or a typo. Refused as a bad request.
//	not found      → the provider looked and this workspace has no such
//	                 entity. Deleted, never existed, or someone else's —
//	                 one answer for all three, by design.
//	resolved       → the label and subtitle the provider authored.
//
// The workspace is passed through untouched and every provider is required
// to scope on it. Nothing in this package can widen that: it holds no
// database handle and no way to reach one.
func (r *Registry) Resolve(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref domain.ContextReference,
) (ports.ResolvedReference, bool, error) {
	resolver, ok := r.byType[ref.Type]
	if !ok {
		return ports.ResolvedReference{}, false, domain.ContextReferenceRejected(
			domain.CodeContextReferenceTypeUnknown,
			"this build cannot resolve references of type "+ref.Type.String())
	}
	return resolver.Resolve(ctx, workspaceID, ref)
}

// HydrationPolicy answers what the owning provider declared for a type.
//
// ── Why an unknown type is `false` and not a refusal ───────────────────
// Because unlike Resolve, this is not answering a client. It is asked on
// every turn about subjects that were ADMITTED long ago, and a build that
// dropped a provider must let those conversations keep working — with no
// hydration, which is exactly what `false` means. Refusing here would make
// removing a provider break every thread that ever referenced one of its
// entities.
func (r *Registry) HydrationPolicy(t domain.ContextReferenceType) (ports.ReferenceHydrationPolicy, bool) {
	h, ok := r.hydrators[t]
	if !ok {
		return ports.ReferenceHydrationPolicy{}, false
	}
	return h.HydrationPolicy(t)
}

// Hydrate reads one entity's present state through its provider.
//
// ── What this function does not do, and must not start doing ───────────
// It does not check authorization. It holds no grants, no agent and no way
// to reach either, and that is deliberate: the check lives beside the tool
// executor's check, in the application layer, using the same grant set the
// same way — see Service.hydrateSubjects. A second authorization decision
// here would be a second answer to a question that must have one.
//
// A type with no hydrator answers `false` rather than an error, for the
// reason HydrationPolicy gives. In practice the caller has already asked for
// the policy and will not reach this.
func (r *Registry) Hydrate(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref domain.ContextReference,
) (ports.ReferenceState, bool, error) {
	h, ok := r.hydrators[ref.Type]
	if !ok {
		return ports.ReferenceState{}, false, nil
	}
	// The workspace is passed through untouched and the provider is required
	// to scope on it — the same contract Resolve states, enforced the same
	// way: nothing in this package holds a database handle.
	return h.Hydrate(ctx, workspaceID, ref)
}

// Types lists everything resolvable, for a diagnostic or a test. Order is
// not guaranteed and no caller should depend on one.
func (r *Registry) Types() []domain.ContextReferenceType {
	out := make([]domain.ContextReferenceType, 0, len(r.byType))
	for t := range r.byType {
		out = append(out, t)
	}
	return out
}
