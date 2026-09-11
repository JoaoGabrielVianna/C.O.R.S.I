package app

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Reference hydration: putting the PRESENT in front of the model.
//
// ── The failure, stated precisely ──────────────────────────────────────
// A conversation is about an opportunity. Turn one asks its stage, a tool
// reads `applied`, the model answers `applied`. The opportunity then moves
// to `interview`. Turn two asks "e agora?" and the model answers `applied`
// again, without calling anything, because the conversation it is reading
// contains its own sentence saying so.
//
// Two vectors were already closed and neither was enough:
//
//	the tool result   dropReadingsOfSubjects stops the earlier reading from
//	                  being replayed as evidence. Closed, verified, kept.
//	the wording       two revisions of the reference block telling the model
//	                  its readings may be stale. It complied sometimes.
//
// What remained is the model's own prose in the history, and there is no
// wording that reliably beats it — from the model's side, its own recent
// answer is the best evidence it has, and instructing it to distrust that
// competes with instructing it to use evidence at all. A third prompt
// revision would have been the third attempt at the same idea.
//
// ── So the runtime stops asking ────────────────────────────────────────
// It reads the state itself, before the model reasons, on every turn the
// subject is attached to, and puts it in the context as the present. The
// history may still say `applied`; the model is now reading a block that
// says `stage: interview` and says in as many words that it is newer.
//
//	persisted reference    identity. never touched by any of this.
//	      ↓
//	freshness policy       the provider decides: per_turn, or nothing.
//	      ↓
//	authorization          the agent's grant for the named capability.
//	      ↓
//	provider hydration     the provider's own application layer.
//	      ↓
//	one context block      ephemeral. discarded when the turn ends.
//
// ── Why this is not a bypass, in one paragraph ─────────────────────────
// The gate is the SAME grant the model's own tool call is checked against,
// read from the same store, on the same turn, compared with the same
// function (isAuthorized). A revoked `job_radar.opportunity.get` stops
// hydration on exactly the turn it stops the tool call, and there is
// nothing hydration can read that an authorized agent could not have read
// by calling the tool itself. What it removes is the model's discretion
// about WHEN to look, which was never a permission.

/* ── the turn's read ─────────────────────────────────────────────────── */

// HydratedReference is one subject's present state, for one turn.
//
// It exists only inside a turn. Nothing writes it back to the conversation
// or to a message: the persisted reference is identity, and a reference
// that learned to carry state would be the snapshot this whole design is
// built to refuse. See TestHydrationIsNeverPersisted.
type HydratedReference struct {
	// Reference is the subject as it is stored: identity and recognition
	// text. Carried so the rendered block can name what it is reporting on
	// without a second lookup.
	Reference domain.ContextReference
	Status    domain.HydrationStatus
	// Fields are the provider's answer, in the provider's order. Empty for
	// every status but Current.
	Fields []ports.ReferenceStateField
}

// hydrateSubjects reads the present state of this turn's subjects.
//
// ── Why the authorized tool set is the gate, and not the grants ────────
// `tools` is what THIS turn may do: the agent's grants, already read from
// the store by authorizedTools, and already narrowed if the user scoped the
// turn with `@`. Passing it rather than re-reading the grants has three
// consequences, all wanted:
//
//   - one authorization read per turn, so hydration and the tool executor
//     cannot disagree about what was granted at the moment the turn ran;
//   - a revoke takes effect on the next turn, which is the same guarantee
//     tool execution already gives and is documented in tools.go;
//   - a turn the user narrowed to one capability does not quietly read
//     through a capability they excluded. Hydration is never MORE than what
//     the turn itself could do.
//
// ── Why a subject with no policy produces no entry at all ──────────────
// Not an entry with a status, not a line saying nothing was read: nothing.
// A type whose provider declared no freshness is a type the model was never
// promised state for, and announcing its absence on every turn would spend
// characters telling the model about a feature rather than about the work.
//
// Degrades rather than fails, like memory and sources: a provider that is
// down costs the turn its freshness, never the answer. The model is told,
// which is the part that matters — see renderReferenceState.
func (s *Service) hydrateSubjects(
	ctx context.Context,
	workspaceID uuid.UUID,
	agentID uuid.UUID,
	conversationID uuid.UUID,
	subjects []domain.ContextReference,
	tools []domain.ToolDefinition,
) []HydratedReference {
	if len(subjects) == 0 || s.references == nil {
		return nil
	}

	out := make([]HydratedReference, 0, len(subjects))
	for _, ref := range subjects {
		policy, declared := s.references.HydrationPolicy(ref.Type)
		if !declared || policy.Freshness != ports.FreshnessPerTurn {
			continue
		}

		entry := HydratedReference{Reference: ref}
		started := time.Now()

		if !s.hydrationAuthorized(policy.Capability, tools) {
			// Nothing is read. Not the label, not the state, not a count —
			// the provider is not called at all, so there is no privileged
			// read to argue about. The subject stays in the conversation and
			// stays recognisable, because the USER named it by attaching it.
			entry.Status = domain.HydrationUnauthorized
			out = append(out, entry)
			s.logHydration(conversationID, agentID, ref, policy, entry.Status, started)
			continue
		}

		state, found, err := s.references.Hydrate(ctx, workspaceID, ref)
		switch {
		case err != nil:
			entry.Status = domain.HydrationFailed
			s.log.Warn("hydrate context reference",
				"conversation_id", conversationID, "type", ref.Type, "err", err)
		case !found:
			// Deleted, never existed, or another workspace's. One answer, so
			// an id that was fabricated — or that survived from a workspace
			// it no longer belongs to — cannot be used to tell the three
			// apart. Same rule as Resolve and for the same reason.
			entry.Status = domain.HydrationUnavailable
		default:
			entry.Status = domain.HydrationCurrent
			entry.Fields = state.Fields
		}
		out = append(out, entry)
		s.logHydration(conversationID, agentID, ref, policy, entry.Status, started)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hydrationAuthorized answers whether this turn may read that state.
//
// DENY BY DEFAULT, at three points, and each one closes a different door:
//
//	no capability named  → denied. A provider cannot opt out of the check by
//	                       declining to name one. (The registry also refuses
//	                       such a policy at composition; this is the second
//	                       lock on the same door, because "safe because it
//	                       was checked elsewhere" is the assumption that
//	                       stops being true after a refactor.)
//	not in the registry  → denied. A capability that is not a tool in this
//	                       build is a name nobody can grant and nobody can
//	                       revoke, so it must never authorize anything.
//	not in this turn's
//	  authorized set     → denied. The grant, as the store had it when this
//	                       turn started.
//
// The last check is `isAuthorized`, the very function the tool executor
// calls in its own gate 2 (see toolexec.go). That is not a coincidence to
// be tidied up later: one function means one answer, and the invariant that
// hydration reads nothing a tool call would have been refused is a property
// of the code rather than of two implementations agreeing today.
func (s *Service) hydrationAuthorized(capability domain.ToolName, tools []domain.ToolDefinition) bool {
	if capability == "" {
		return false
	}
	if _, registered := s.tools.Lookup(capability); !registered {
		return false
	}
	_, allowed := isAuthorized(tools, capability)
	return allowed
}

// logHydration is the observability seam.
//
// ── Why hydration writes no row to chat.tool_calls ─────────────────────
// It was considered and rejected, and the reason is what that table is. It
// is the record of what THE MODEL asked to run: every row has a provider
// call id, a round, and a place in a turn's tool loop. A hydration row would
// have none of those and would have to invent them, and the transcript that
// reads the table would then show a call the model never made — which is a
// worse audit than no row, because it is a false one.
//
// It would also corrupt evidence replay, which keys on "the newest call
// with these arguments" and drops readings of the current subjects. A
// synthetic row is a reading of the current subject, so it would be written
// and immediately dropped: cost with no reader.
//
// What replaces it is stronger for the question anyone actually asks. The
// context report carries a BlockReferenceState with the item count and the
// exclusions — unauthorized, unavailable — and it is STAMPED ON THE
// ASSISTANT MESSAGE and stored with it. So "did this turn have the current
// stage in front of it?" is answered months later from a row, not from a log
// that rotated. This function adds what a report cannot hold: the per-subject
// duration and provider, at debug level, for the turn that is happening now.
func (s *Service) logHydration(
	conversationID, agentID uuid.UUID,
	ref domain.ContextReference,
	policy ports.ReferenceHydrationPolicy,
	status domain.HydrationStatus,
	started time.Time,
) {
	s.log.Debug("reference hydration",
		"conversation_id", conversationID,
		"agent_id", agentID,
		"provider", ref.Type.Provider(),
		"type", ref.Type.String(),
		"reference_id", ref.ID,
		"capability", policy.Capability.String(),
		"freshness", string(policy.Freshness),
		"status", status.String(),
		"duration_ms", int(time.Since(started).Milliseconds()))
}

// hydratedKeys is the set of subjects that appear in the state block.
//
// Used by the identity block to know whose frozen recognition text it must
// keep quiet about. See RenderContextReferencesBlock.
func hydratedKeys(hydrated []HydratedReference) map[string]bool {
	if len(hydrated) == 0 {
		return nil
	}
	keys := make(map[string]bool, len(hydrated))
	for _, h := range hydrated {
		keys[h.Reference.Key()] = true
	}
	return keys
}

/* ── rendering ───────────────────────────────────────────────────────── */

// referenceStateHeader is the block's claim about itself, and the sentence
// that has to beat the history.
//
// ── What each clause is doing ──────────────────────────────────────────
// "read just now, from the system that owns them" is provenance: it says
// where the numbers came from, which is the only claim this system can
// actually stand behind.
//
// "more recent than anything else in this conversation, including your own
// earlier answers" is the precedence rule. It is stated explicitly because
// the competing text — the model's own sentence three turns up — is the
// most persuasive thing in the context and does not announce its own age.
//
// "do not repeat an earlier answer about these values" closes the specific
// observed failure: a model that read `applied` on turn one answered
// "applied, como mencionei anteriormente" on turn two. Naming the behaviour
// is what makes the instruction actionable rather than abstract.
//
// ── Why this wording is not what makes the batch work ──────────────────
// The header is a tiebreak, not the mechanism. The mechanism is that the
// present state is PRESENT, on every relevant turn, whether or not the model
// thought to look. Two prompt revisions failed at this before there was any
// current state in the context to prefer; there is now, and the header only
// says which of the two things in front of the model is newer.
const referenceStateHeader = "Current state of the attached items, read just now, " +
	"at the start of this turn, from the system that owns them.\n\n" +
	"This is MORE RECENT than anything else in this conversation, including " +
	"your own earlier answers and the descriptions attached to the items. " +
	"These items change between turns. When the user asks about the present " +
	"state of one of them, answer from the values below and do not repeat an " +
	"earlier answer about them.\n"

// The per-entry scaffolding. Derived constants rather than typed literals
// so the budget and the rendered text cannot drift — the same arrangement
// memory, sources and evidence use.
const (
	referenceStateBullet    = "\n- "
	referenceStateTypeMark  = " ("
	referenceStateIDMark    = ", id: "
	referenceStateTypeEnd   = ")\n"
	referenceStateFieldMark = "  "
	referenceStateFieldSep  = ": "
	referenceStateFieldEnd  = "\n"
)

// The sentence the model reads for each of the three ways state was not
// obtained.
//
// Each one says what happened AND what not to do about it, because the
// failure being guarded against is not silence — it is the model filling the
// gap from the history, which is precisely what it does when told only that
// something is missing.
//
// The unauthorized sentence deliberately does not name the tool or the
// grant. The model has no way to fix it, the user is not the one who
// configured it in every channel this may run in, and a runtime block that
// taught the model our capability names would be Agents leaking a provider's
// vocabulary into a prompt — the same rule the identity block already keeps.
const (
	referenceStateUnauthorized = "  current state: NOT AVAILABLE — you are not authorized to read " +
		"this item's state. Do not state what it is; say you cannot check it.\n"
	referenceStateUnavailable = "  current state: NOT AVAILABLE — it could not be found, and may " +
		"have been removed. Do not answer from anything said about it earlier.\n"
	referenceStateFailed = "  current state: NOT AVAILABLE — the read did not succeed. Do not " +
		"answer from anything said about it earlier, and do not say it was removed.\n"
)

// RenderReferenceStateBlock is the exact text the model receives.
//
// Exported for the same reason RenderMemoryBlock and RenderEvidenceBlock
// are: the report charges for precisely what is sent, and a renderer the
// report cannot call is a cost the report cannot see.
//
// One system message for the whole list, like every other block: an entry
// per subject would read to the model as a dialogue it took part in, and
// would pay the per-message overhead every gateway charges.
//
// ── Why the id is repeated here ────────────────────────────────────────
// It is already in the identity block a few lines above. It is here too
// because these two blocks are read as one thing and a model that wants to
// verify — or to move the entity afterwards — should not have to correlate
// two lists by label. Labels are not unique; ids are.
//
// ── Why there is no budget selection ───────────────────────────────────
// The list is capped at domain.MaxContextReferences, and the fields are the
// provider's minimum rather than its record. The realistic worst case is a
// few hundred characters, which is smaller than one memory. What IS reported
// is the cost, because a block that reached the model without appearing in
// the account would be a silent charge — and hydration runs on every turn,
// so a silent charge here would compound.
func RenderReferenceStateBlock(hydrated []HydratedReference) string {
	if len(hydrated) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(referenceStateHeader)
	for _, h := range hydrated {
		b.WriteString(referenceStateBullet)
		b.WriteString(h.Reference.Label)
		b.WriteString(referenceStateTypeMark)
		b.WriteString(h.Reference.Type.String())
		b.WriteString(referenceStateIDMark)
		b.WriteString(h.Reference.ID)
		b.WriteString(referenceStateTypeEnd)

		switch h.Status {
		case domain.HydrationUnauthorized:
			b.WriteString(referenceStateUnauthorized)
		case domain.HydrationUnavailable:
			b.WriteString(referenceStateUnavailable)
		case domain.HydrationFailed:
			b.WriteString(referenceStateFailed)
		default:
			for _, f := range h.Fields {
				b.WriteString(referenceStateFieldMark)
				b.WriteString(f.Name)
				b.WriteString(referenceStateFieldSep)
				b.WriteString(flattenLines(f.Value))
				b.WriteString(referenceStateFieldEnd)
			}
		}
	}
	return b.String()
}

// hydrationExclusions counts the subjects whose state was not obtained,
// by the reason a report reader needs.
//
// Unauthorized is its own reason and not folded into unavailable: see
// domain.ReasonUnauthorized on why the distinction is the difference
// between an outage and a decision.
func hydrationExclusions(hydrated []HydratedReference) (unauthorized, unavailable int) {
	for _, h := range hydrated {
		switch h.Status {
		case domain.HydrationUnauthorized:
			unauthorized++
		case domain.HydrationUnavailable, domain.HydrationFailed:
			// Two statuses, one exclusion reason. The model is told them
			// apart because the sentences differ; the report counts them
			// together because the action a reader takes is the same — the
			// state was not read and the turn ran without it.
			unavailable++
		}
	}
	return unauthorized, unavailable
}

// hydratedItems is how many subjects actually carried state, which is what
// the block's Items means. A subject that was refused cost characters and
// contributed no state, so it is an exclusion rather than an item.
func hydratedItems(hydrated []HydratedReference) int {
	n := 0
	for _, h := range hydrated {
		if h.Status.Obtained() {
			n++
		}
	}
	return n
}
