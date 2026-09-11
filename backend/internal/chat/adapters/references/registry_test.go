package references

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// The registry is the answer to "how does a second provider arrive?", and
// the answer has to be "it is handed to New, and nothing else changes".
//
// These tests use providers that do not exist anywhere in the product on
// purpose. If registering a type required the runtime to know its name —
// a switch, a constant, a special case — a made-up provider could not
// possibly work here, and every one of these would fail.

/* ── fakes ───────────────────────────────────────────────────────────── */

// plainResolver names entities and nothing more: the shape every provider
// had before hydration existed.
type plainResolver struct {
	types []domain.ContextReferenceType
	label string
}

func (r plainResolver) Types() []domain.ContextReferenceType { return r.types }

func (r plainResolver) Resolve(context.Context, uuid.UUID, domain.ContextReference) (ports.ResolvedReference, bool, error) {
	return ports.ResolvedReference{Label: r.label}, true, nil
}

// freshResolver also reports present state.
type freshResolver struct {
	plainResolver
	freshness  ports.ReferenceFreshness
	capability domain.ToolName
	value      string
}

func (r freshResolver) HydrationPolicy(domain.ContextReferenceType) (ports.ReferenceHydrationPolicy, bool) {
	return ports.ReferenceHydrationPolicy{Freshness: r.freshness, Capability: r.capability}, true
}

func (r freshResolver) Hydrate(context.Context, uuid.UUID, domain.ContextReference) (ports.ReferenceState, bool, error) {
	return ports.ReferenceState{
		Fields: []ports.ReferenceStateField{{Name: "state", Value: r.value}},
	}, true, nil
}

func fresh(t domain.ContextReferenceType, capability domain.ToolName, value string) freshResolver {
	return freshResolver{
		plainResolver: plainResolver{types: []domain.ContextReferenceType{t}, label: string(t)},
		freshness:     ports.FreshnessPerTurn,
		capability:    capability,
		value:         value,
	}
}

/* ── registration is data ────────────────────────────────────────────── */

// A provider this build has never heard of registers, resolves and
// hydrates, with no change to any file outside its own package.
func TestAProviderRegistersWithoutTheRuntimeKnowingItsName(t *testing.T) {
	r := MustNew(
		fresh("calendar.event", "calendar.event.get", "starts in 20 minutes"),
		plainResolver{types: []domain.ContextReferenceType{"gmail.thread"}, label: "a thread"},
	)

	policy, declared := r.HydrationPolicy("calendar.event")
	if !declared {
		t.Fatal("a declared policy was not visible through the registry")
	}
	if policy.Capability != "calendar.event.get" || policy.Freshness != ports.FreshnessPerTurn {
		t.Fatalf("policy = %+v", policy)
	}

	state, found, err := r.Hydrate(context.Background(), uuid.New(),
		domain.ContextReference{Type: "calendar.event", ID: "x"})
	if err != nil || !found {
		t.Fatalf("hydrate: found=%v err=%v", found, err)
	}
	if len(state.Fields) != 1 || state.Fields[0].Value != "starts in 20 minutes" {
		t.Fatalf("fields = %+v", state.Fields)
	}
}

// Hydration is opt-in. A resolver that does not implement the second half
// keeps behaving exactly as it did before that half existed: it resolves,
// it declares no freshness, and the runtime reads nothing on its own.
func TestAResolverThatDoesNotHydrateDeclaresNothing(t *testing.T) {
	r := MustNew(plainResolver{
		types: []domain.ContextReferenceType{"gmail.thread"}, label: "a thread",
	})

	if _, declared := r.HydrationPolicy("gmail.thread"); declared {
		t.Error("a plain resolver was reported as hydrating")
	}
	if _, found, err := r.Hydrate(context.Background(), uuid.New(),
		domain.ContextReference{Type: "gmail.thread", ID: "x"}); found || err != nil {
		t.Errorf("found=%v err=%v, want false/nil", found, err)
	}
	// It still resolves. Nothing about hydration changed the older contract.
	if _, found, err := r.Resolve(context.Background(), uuid.New(),
		domain.ContextReference{Type: "gmail.thread", ID: "x"}); !found || err != nil {
		t.Errorf("resolve: found=%v err=%v", found, err)
	}
}

// An on-demand declaration is the same as no declaration, and must not
// cause a per-turn read. This is the seam that keeps a large entity — a
// repository, a mail thread — out of every prompt.
func TestAnOnDemandTypeIsNotHydratedByTheRuntime(t *testing.T) {
	lazy := fresh("github.repository", "github.repository.get", "never asked for")
	lazy.freshness = ports.FreshnessOnDemand

	r := MustNew(lazy)
	if _, declared := r.HydrationPolicy("github.repository"); declared {
		t.Error("an on-demand type was registered as hydrating")
	}
	if _, found, _ := r.Hydrate(context.Background(), uuid.New(),
		domain.ContextReference{Type: "github.repository", ID: "x"}); found {
		t.Error("an on-demand type was hydrated")
	}
}

// A type nobody owns answers "no policy" rather than refusing.
//
// Unlike Resolve, this is not answering a client: it is asked on every turn
// about subjects admitted long ago, and a build that dropped a provider must
// leave those conversations working — with no hydration, which is what the
// negative answer means.
func TestAnUnknownTypeHasNoPolicyAndIsNotAnError(t *testing.T) {
	r := MustNew(fresh("calendar.event", "calendar.event.get", "v"))

	if _, declared := r.HydrationPolicy("finance.transaction"); declared {
		t.Error("a type with no provider reported a policy")
	}
	if _, found, err := r.Hydrate(context.Background(), uuid.New(),
		domain.ContextReference{Type: "finance.transaction", ID: "x"}); found || err != nil {
		t.Errorf("found=%v err=%v, want false/nil", found, err)
	}
}

/* ── wiring mistakes are named at start-up ───────────────────────────── */

// A policy with no capability could never be authorized — hydration is
// gated on a tool grant. Left to runtime it would look configured and
// silently never work, so it is refused where the wiring happens.
func TestAPolicyWithoutACapabilityRefusesTheRegistry(t *testing.T) {
	broken := fresh("calendar.event", "", "v")

	_, err := New(broken)
	if err == nil {
		t.Fatal("a policy naming no capability was accepted")
	}
	if !strings.Contains(err.Error(), "capability") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// Two owners for one entity type is a wiring mistake worth a start-up
// failure rather than a mystery at runtime. Unchanged by hydration, and
// asserted here so it stays that way.
func TestTwoOwnersForOneTypeRefuseTheRegistry(t *testing.T) {
	if _, err := New(
		fresh("calendar.event", "calendar.event.get", "a"),
		fresh("calendar.event", "calendar.event.get", "b"),
	); err == nil {
		t.Fatal("a duplicate type was accepted")
	}
}

// A tool name is three segments and a reference type is two. The registry
// must refuse anything that is not a type, so the two vocabularies cannot
// bleed into one another through a mistyped registration.
func TestAToolNameCannotBeRegisteredAsAType(t *testing.T) {
	if _, err := New(plainResolver{
		types: []domain.ContextReferenceType{"job_radar.opportunity.get"},
	}); err == nil {
		t.Fatal("a tool name was accepted as a reference type")
	}
}
