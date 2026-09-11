package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/jobradar/app"
	"github.com/corsi/backend/internal/jobradar/domain"
)

// The Job Radar context-reference resolver.
//
// ── Why it lives beside the tools and not in a package of its own ──────
// Because it is the same relationship to the same collaborator: this file
// and tools.go both satisfy an interface the Agents module declares, both
// call the Job Radar application service, and both are handed over by
// module.go to the composition root. Splitting them would create a second
// package whose entire content is "the other way Agents reaches Job Radar".
//
// ── What it does NOT do, and this is the point ─────────────────────────
// It does not return the opportunity. It returns the words needed to
// recognise one:
//
//	resolver  →  "Acme · Backend Engineer", "Applied"   (who it is)
//	tool      →  stage, salary, stack, notes, history   (how it is)
//
// The distinction is what keeps a reference from becoming a capability. An
// agent with a reference and without a grant can see the subject's NAME —
// which the user just told it by attaching it — and cannot read its state.
// Everything an agent learns beyond the name comes through
// job_radar.opportunity.get, which is refused without authorization.
//
// ── Hydration: the same service, a different question ──────────────────
// This file also answers "what is it doing NOW", through the hydrator half
// of the port. The two are not in tension, they are the two halves of the
// split the reference design is built on:
//
//	Resolve   →  identity + STABLE recognition text.  Persisted. Ungated:
//	             the user attached it, so they may see what it is called.
//	Hydrate   →  MUTABLE state, read fresh every turn. Never persisted.
//	             Gated on the agent's grant for OpportunityGetTool.
//
// Nothing here decides whether the agent may read state. Agents checks the
// grant before this file's Hydrate is reached — see
// Service.hydrationAuthorized — which is why Hydrate may call the same
// application service the tool calls without that being a second, quieter
// door into the same data. It reads on behalf of an agent that was
// authorized to read exactly this, or it is not called.

// referenceResolver answers for Job Radar's entity types.
type referenceResolver struct{ svc *app.Service }

// NewReferenceResolver builds the resolver, ready for the reference
// registry in cmd/corsi.
func NewReferenceResolver(svc *app.Service) chatports.ContextReferenceResolver {
	return referenceResolver{svc: svc}
}

// OpportunityReferenceType is the one type this module owns today.
//
// It is exported because the composition root does not name it but the
// tests and the frontend contract do, and a string literal repeated across
// three files is a rename waiting to go half-done.
const OpportunityReferenceType chatdomain.ContextReferenceType = "job_radar.opportunity"

func (referenceResolver) Types() []chatdomain.ContextReferenceType {
	return []chatdomain.ContextReferenceType{OpportunityReferenceType}
}

// Resolve looks one opportunity up for one workspace.
//
// ── Why a bad id is `found=false` and not an error ─────────────────────
// Because the caller is holding a string that came from a client, and the
// three ways it can fail to name something this workspace owns — malformed,
// never existed, someone else's — are one answer on purpose. Reporting them
// apart would let a fabricated id be used to probe for the existence of
// rows in other workspaces, which is exactly the attack the single answer
// closes. An `error` is kept for a genuine failure to ask, such as the
// database being unreachable, because that is not a fact about the id.
func (r referenceResolver) Resolve(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref chatdomain.ContextReference,
) (chatports.ResolvedReference, bool, error) {
	if ref.Type != OpportunityReferenceType {
		return chatports.ResolvedReference{}, false, nil
	}

	id, err := uuid.Parse(strings.TrimSpace(ref.ID))
	if err != nil {
		return chatports.ResolvedReference{}, false, nil
	}

	// The same application service the tools call. There is no second read
	// path to Job Radar, and no repository reachable from here.
	//
	// The workspace comes from the caller and is applied inside the service,
	// which filters in SQL — so an id belonging to another workspace reads
	// exactly like one that never existed, without this function having to
	// remember to check anything.
	opportunity, err := r.svc.GetOpportunity(ctx, workspaceID, id)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotFound {
			return chatports.ResolvedReference{}, false, nil
		}
		return chatports.ResolvedReference{}, false, err
	}

	return chatports.ResolvedReference{
		Label:    opportunity.CompanyName + " · " + opportunity.Role,
		Subtitle: subtitleFor(opportunity),
	}, true, nil
}

// subtitleFor is the one line of extra recognition, and it carries STABLE
// facts only.
//
// ── Why the stage is not in here any more ──────────────────────────────
// It used to be: "applied · Remote (BR)". It was the most useful thing on a
// chip and it was the wrong thing to persist, because the subtitle is
// written once, at attach time, into a JSONB column that is never rewritten
// — and the stage is the single most likely field of this entity to move
// while a conversation about it is happening. A thread opened in `applied`
// kept saying `applied` in its stored metadata after the opportunity reached
// `interview`, and that frozen word travelled into the model's context on
// every turn, competing with the truth.
//
// So the rule is now stated in the data rather than in a warning around it:
//
//	persisted   what the item IS        company · role · where
//	hydrated    what the item is DOING  stage, read this turn
//
// Location stays because it is what actually distinguishes two roles at the
// same company, and because a role's location changes at most once in its
// life — and when it does, the chip being a week stale is a cosmetic
// difference, not a wrong answer about the pipeline.
//
// An opportunity with no location has no subtitle at all. An empty line is
// better than a filler one: the label already carries company and role,
// which is enough to recognise it.
func subtitleFor(o *domain.Opportunity) string {
	return strings.TrimSpace(o.Location)
}

/* ── hydration ───────────────────────────────────────────────────────── */

// HydrationPolicy declares how this module's entities stay fresh.
//
// ── Why per_turn for an opportunity ────────────────────────────────────
// Two properties, and it needs both. It is SMALL — four short fields, well
// under a hundred characters — so carrying it on every turn costs about as
// much as one memory. And it MOVES: the stage is the whole point of the
// module, it changes while conversations about it are open, and it is
// exactly what the user is asking when they say "e agora?".
//
// An entity with only one of the two properties would not qualify. A GitHub
// repository moves and is large, so it stays on demand: the model asks for
// what it needs, when it needs it. A field that never changes would not need
// hydrating at all — it could simply be persisted.
//
// ── Why the capability is the get tool ─────────────────────────────────
// Because hydration reads exactly what `job_radar.opportunity.get` reads,
// from the same service, and nothing an agent could not have read by calling
// it. Naming any other capability would be claiming the read is a different
// kind of access than it is; naming none would be asking to skip the check.
func (referenceResolver) HydrationPolicy(t chatdomain.ContextReferenceType) (chatports.ReferenceHydrationPolicy, bool) {
	if t != OpportunityReferenceType {
		return chatports.ReferenceHydrationPolicy{}, false
	}
	return chatports.ReferenceHydrationPolicy{
		Freshness:  chatports.FreshnessPerTurn,
		Capability: OpportunityGetTool,
	}, true
}

// Hydrate reads one opportunity's present state.
//
// ── Why these four fields and not `detail` ─────────────────────────────
// `detail` is what the get tool returns when the model ASKED for an
// opportunity: description, notes, stack, salary, timestamps — a full
// record, appropriate for a question about the record. This runs on every
// turn whether or not the turn is about the entity's details, so it carries
// the minimum a reasoning step needs to be right about the present:
//
//	company, role   which item this is, restated beside its state so the
//	                model never has to correlate two lists by label
//	stage           the mutable fact this whole mechanism exists for
//	location        the one other field that disambiguates two roles
//
// Everything else is left out on purpose. The description alone can be
// twenty thousand characters, the notes are private working text, and a
// hydration that grew into the whole record would be a database dump billed
// on every turn. If the model needs the rest, the tool is right there and it
// is authorized — this is the same grant.
//
// ── Why a bad id is found=false and not an error ───────────────────────
// The same rule Resolve follows: malformed, never existed, and another
// workspace's are one answer, so a fabricated id cannot be used to probe for
// rows this workspace may not see. Note that the id reaching here was
// admitted for this workspace at attach time — this is the second check on
// the same door, because a workspace's access can be true when a reference
// is stored and false later.
func (r referenceResolver) Hydrate(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref chatdomain.ContextReference,
) (chatports.ReferenceState, bool, error) {
	if ref.Type != OpportunityReferenceType {
		return chatports.ReferenceState{}, false, nil
	}

	id, err := uuid.Parse(strings.TrimSpace(ref.ID))
	if err != nil {
		return chatports.ReferenceState{}, false, nil
	}

	// The same application service the tool calls, scoped by the same
	// workspace, filtered in the same SQL. There is no second read path into
	// Job Radar and no repository reachable from here.
	o, err := r.svc.GetOpportunity(ctx, workspaceID, id)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotFound {
			return chatports.ReferenceState{}, false, nil
		}
		return chatports.ReferenceState{}, false, err
	}

	fields := []chatports.ReferenceStateField{
		{Name: "company", Value: o.CompanyName},
		{Name: "role", Value: o.Role},
		// stageLabel spells the untracked state "discover" rather than
		// leaving the field out, for the reason it gives: a model reading an
		// absent stage has to guess what the absence means.
		{Name: "stage", Value: stageLabel(o)},
	}
	if loc := strings.TrimSpace(o.Location); loc != "" {
		fields = append(fields, chatports.ReferenceStateField{Name: "location", Value: loc})
	}
	return chatports.ReferenceState{Fields: fields}, true, nil
}
