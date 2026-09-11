package app

import (
	"github.com/corsi/backend/internal/chat/domain"
)

// Turn scoping: turning a user's selection into the capabilities one turn
// may declare.
//
// ── The one sentence this file has to keep true ────────────────────────
// A selection can only ever REMOVE. It is intersected with what the agent
// was already authorized to use, and a name outside that set refuses the
// turn instead of widening it. There is no path through this file, and
// there must never be one, in which a client-supplied name causes a tool to
// be exposed that the store did not already grant.
//
// ── Why refusing, and not filtering silently ───────────────────────────
// A selection that quietly dropped an unauthorized name would produce a
// turn that ran without the capability the interface had just shown
// attached to it. The user would read a confident answer, believe it came
// from a capability that was never declared, and nothing anywhere would say
// otherwise. So an impossible selection is a 400 before the question is
// persisted and before the provider is called — the composer still holds
// the text, and nothing about the thread changed.
//
// ── Why the backend writes the label ───────────────────────────────────
// The request carries identity only. The words that go into the permanent
// record come from this process's own registry, so the transcript's account
// of what was selected cannot be authored by whoever made the request.

// scopeTools intersects an explicit selection with what the agent may use.
//
// Returns the definitions to declare for the turn, in the same order
// `authorized` had them — which is registry order, which is name order, so
// two identical turns still build an identical request body — and the
// references to freeze onto the user's message, in the same order for the
// same reason.
//
// It is only ever called for a non-empty selection. A turn with none does
// not come through here at all, which is what keeps the legacy path exactly
// as long and exactly as costly as it was.
func (s *Service) scopeTools(
	authorized []domain.ToolDefinition,
	refs []domain.TurnReference,
) ([]domain.ToolDefinition, []domain.TurnReference, error) {
	if err := domain.ValidateTurnReferences(refs); err != nil {
		return nil, nil, err
	}

	// Selecting the same capability twice is selecting it once. Deduplicated
	// rather than refused: it is a harmless request to say the same true
	// thing twice, and the composer's own rule already prevents it — a
	// server that rejected it would be punishing a client for a redundancy
	// that costs nothing.
	wanted := make(map[domain.ToolName]bool, len(refs))
	for _, r := range refs {
		// Kind is re-checked by Validate above; this switch is what makes
		// adding a kind a compile-time visit to this function rather than a
		// silent no-op that scopes nothing.
		switch r.Kind {
		case domain.ReferenceKindTool:
			name := domain.ToolName(r.ID)
			if err := s.assertSelectableTool(authorized, name); err != nil {
				return nil, nil, err
			}
			wanted[name] = true
		case domain.ReferenceKindIntegration:
			// Expanded HERE, against the grants as they stand for this turn.
			// Everything downstream — the declaration, the per-call gate,
			// the frozen record — then deals in individual capabilities and
			// has no idea a group was ever involved.
			names, err := s.integrationTools(authorized, r.ID)
			if err != nil {
				return nil, nil, err
			}
			for _, name := range names {
				wanted[name] = true
			}
		default:
			return nil, nil, domain.ReferenceRejected(domain.CodeReferenceKindUnknown,
				"reference kind "+r.Kind.String()+" is not a kind this system knows")
		}
	}

	scoped := make([]domain.ToolDefinition, 0, len(wanted))
	selected := make([]domain.TurnReference, 0, len(wanted))
	for _, d := range authorized {
		if !wanted[d.Name] {
			continue
		}
		scoped = append(scoped, d)
		selected = append(selected, domain.TurnReference{
			Kind: domain.ReferenceKindTool,
			ID:   d.Name.String(),
			// The label the record keeps, taken from this build's own
			// definition. See domain/reference.go on why it is frozen.
			Label: d.Title,
		})
	}
	return scoped, selected, nil
}

// integrationTools expands one provider into the capabilities of that
// provider this agent may already use.
//
// ── Why it reads the grants and never the registry ─────────────────────
// Because the registry is what EXISTS and the grants are what is ALLOWED,
// and a group selection must intersect with the second. Expanding from the
// registry would turn `@GitHub` into "declare the seven GitHub tools",
// which is precisely the grant a selection is forbidden to create. The
// registry is consulted for one thing only, below: telling "you have not
// authorized any of this provider" apart from "no such provider exists",
// which are different sentences for the user and neither of which exposes
// anything.
//
// The namespace comes from ToolName.Namespace, so the identity a client
// sends is compared against the same derivation the rest of the system
// uses rather than against a list of providers kept somewhere.
func (s *Service) integrationTools(
	authorized []domain.ToolDefinition,
	namespace string,
) ([]domain.ToolName, error) {
	out := make([]domain.ToolName, 0, len(authorized))
	for _, d := range authorized {
		if d.Name.Namespace() == namespace {
			out = append(out, d.Name)
		}
	}
	if len(out) > 0 {
		return out, nil
	}

	// Nothing authorized under that namespace. Which refusal it is depends
	// on whether the build has ever heard of it — the same distinction
	// assertSelectableTool draws, for the same reason: a client that is
	// ahead of the server should learn that, rather than concluding the
	// operator revoked something.
	for _, d := range s.tools.Definitions() {
		if d.Name.Namespace() == namespace {
			return nil, domain.ReferenceRejected(domain.CodeReferenceNotAuthorized,
				"this agent is not authorized to use any capability of "+
					truncate(namespace, 64))
		}
	}
	return nil, domain.ReferenceRejected(domain.CodeReferenceUnknownTool,
		"there is no integration named "+truncate(namespace, 64))
}

// assertSelectableTool answers the two refusals that have to be told apart.
//
// The registry is consulted only to distinguish them. It grants nothing:
// membership in `authorized` is the permission, and a name absent from it
// is refused whether or not the registry has heard of it.
func (s *Service) assertSelectableTool(authorized []domain.ToolDefinition, name domain.ToolName) error {
	if _, ok := isAuthorized(authorized, name); ok {
		return nil
	}
	if _, registered := s.tools.Lookup(name); registered {
		return domain.ReferenceRejected(domain.CodeReferenceNotAuthorized,
			"this agent is not authorized to use "+name.String())
	}
	// Covers a hallucinated name, a stale grant the registry no longer
	// resolves, and a syntactically impossible one alike. All three are the
	// same fact from here: there is nothing of that name to attach.
	return domain.ReferenceRejected(domain.CodeReferenceUnknownTool,
		"there is no tool named "+truncate(name.String(), 64))
}
