package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

// Context references: admitting a subject, and telling the model about it.
//
// ── The two things this file does, and why they are separate ───────────
//
//	admit()   runs when a client attaches a subject. It refuses anything
//	          this workspace cannot see, and REPLACES the client's words
//	          with the provider's own. Nothing reaches storage unadmitted.
//
//	render()  runs on every turn. It turns admitted subjects into a block
//	          of text the model reads, carrying identity and nothing else.
//
// Keeping them apart is what makes the security argument short: admission
// is the only door in, it is workspace-scoped, and it happens before a row
// exists. Rendering can then be trusted because it only ever renders what
// admission already let through.
//
// ── Why rendering deliberately says so little ──────────────────────────
// The block gives the model a type, an id and a name. It does not give it
// the entity, and it never gives it state. That is not an omission to be
// fixed later — it is the mechanism behind two invariants at once:
//
//	freshness      nothing frozen here may stand in for the present. What
//	               was attached last week is a name, not a reading.
//	authorization  a subject the user attached must not become data the
//	               agent was never granted. The name came from the user;
//	               the state needs a grant.
//
// State reaches the model through a SECOND, separate block, read fresh on
// every turn and gated on that grant — see app/hydration.go. The division is
// the whole design: this block is persisted identity, that one is ephemeral
// present, and neither can be mistaken for the other because they are not in
// the same paragraph.

// admitContextReferences resolves an attachment list and returns the
// references as they will be stored.
//
// ── Why the label is overwritten and never accepted ────────────────────
// The request carries identity; the words come from the provider. A client
// that could author the label could write anything into the permanent
// record and into the model's context — "Acme · Backend Engineer (already
// rejected)" attached to a live opportunity, for instance. So whatever
// arrived is discarded and the resolver's answer is used.
//
// ── Why an unresolvable subject is refused here ────────────────────────
// Because this is attach time, and the client is right there to be told.
// Storing a reference nobody could resolve would put a permanent dead
// pointer in a thread in exchange for nothing. Note the contrast with READ
// time, where an entity that has since disappeared is kept and marked
// unavailable: at attach time it never was valid, and later it stopped
// being — different facts, different handling.
func (s *Service) admitContextReferences(
	ctx context.Context,
	workspaceID uuid.UUID,
	refs []domain.ContextReference,
) ([]domain.ContextReference, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if err := domain.ValidateContextReferences(refs); err != nil {
		return nil, err
	}
	// Deduped before resolving so a client that attached the same subject
	// twice costs one lookup rather than two.
	refs = domain.DedupeContextReferences(refs)

	if s.references == nil {
		// No resolver was wired at all. Refusing is the honest answer: the
		// alternative is storing subjects nothing in this build can render.
		return nil, domain.ContextReferenceRejected(domain.CodeContextReferenceTypeUnknown,
			"this build cannot resolve context references")
	}

	out := make([]domain.ContextReference, 0, len(refs))
	for _, ref := range refs {
		resolved, found, err := s.references.Resolve(ctx, workspaceID, ref)
		if err != nil {
			// A domain refusal (unknown type) is already the right shape and
			// is returned as-is; anything else is a genuine failure to ask.
			var de *domain.Error
			if asDomainError(err, &de) {
				return nil, de
			}
			return nil, err
		}
		if !found {
			// Deleted, never existed, or another workspace's — one answer, so
			// a fabricated id learns nothing from the difference.
			return nil, domain.ContextReferenceRejected(domain.CodeContextReferenceNotFound,
				"there is no "+ref.Type.String()+" here with that id")
		}
		out = append(out, domain.ContextReference{
			Type:     ref.Type,
			ID:       ref.ID,
			Label:    resolved.Label,
			Subtitle: resolved.Subtitle,
		})
	}
	return out, nil
}

// asDomainError is errors.As for *domain.Error without importing errors
// into every call site.
func asDomainError(err error, target **domain.Error) bool {
	for err != nil {
		if de, ok := err.(*domain.Error); ok {
			*target = de
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// turnContextReferences is the subject list one turn reasons about: what
// the thread is about, plus what this turn attached.
//
// ── Why the conversation's subjects are included on every turn ─────────
// Because "essa vaga" has to keep resolving on the fourth message. A thread
// opened from an opportunity is about that opportunity until it is closed,
// and requiring the user to re-attach it on every question would make the
// feature useless for exactly the conversation it was built for.
//
// The turn's own attachments come FIRST because they are the more specific
// statement: a message that attaches a different opportunity is asking
// about that one, and the model reads the list in order.
func turnContextReferences(conv *domain.Conversation, turn []domain.ContextReference) []domain.ContextReference {
	if len(turn) == 0 && (conv == nil || len(conv.ContextReferences) == 0) {
		return nil
	}
	combined := make([]domain.ContextReference, 0, len(turn)+len(conv.ContextReferences))
	combined = append(combined, turn...)
	combined = append(combined, conv.ContextReferences...)
	return domain.DedupeContextReferences(combined)
}

// RenderContextReferencesBlock is the exact text the model receives.
//
// Exported because the context budget has to charge for precisely what is
// sent, and a renderer the report cannot call is a cost the report cannot
// see. Same arrangement as RenderMemoryBlock.
//
// ── Why the shape is this shape ────────────────────────────────────────
// One line per subject, identity first, in a block that names itself. The
// model needs three things from it and gets exactly three: that a subject
// exists, what it is called so it can match "essa vaga" against it, and the
// id to pass to a tool.
//
// It names no provider and no tool. A block that said
// "use job_radar.opportunity.get" would be Agents teaching the model about
// a module it must not know, and would be wrong for every other provider.
//
// ── `stateShown` and why a subtitle is sometimes withheld ──────────────
// A subtitle is FROZEN recognition text: the words the provider used when
// the subject was attached, kept so two roles at the same company can be
// told apart on a chip. It was never meant to be reasoned from, and the
// previous version of this block spent four sentences telling the model so.
//
// That was the wrong shape of fix. When a subject's present state is in this
// same context — because its type declares per-turn freshness and the state
// block is right below this one — the frozen line is not a hint any more, it
// is a SECOND ANSWER to the question the state block just answered, and
// whichever the model picks it is choosing between two things that disagree.
// So it is not printed. The state block is the authority for those subjects,
// the card still shows the subtitle to a person, and there is no contest.
//
// For a subject with no per-turn freshness — nothing declares one today
// besides Job Radar's, and a large entity never will — the subtitle is still
// the only recognition text there is, so it is printed, and the closing
// sentence is what keeps it from being read as the present.
func RenderContextReferencesBlock(refs []domain.ContextReference, stateShown map[string]bool) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The user has attached the following to this conversation. " +
		"They identify what is being discussed — when the user says \"this one\", " +
		"they mean one of these.\n")
	frozen := false
	for _, r := range refs {
		b.WriteString("- ")
		b.WriteString(r.Label)
		if r.Subtitle != "" && !stateShown[r.Key()] {
			frozen = true
			b.WriteString(" (")
			b.WriteString(r.Subtitle)
			b.WriteString(")")
		}
		b.WriteString("\n  type: ")
		b.WriteString(r.Type.String())
		b.WriteString("\n  id: ")
		b.WriteString(r.ID)
		b.WriteString("\n")
	}
	b.WriteString("These lines are identity: what each item is called, and the id to " +
		"pass to a tool.")
	if frozen {
		// Only when a frozen description was actually printed. Charging every
		// turn for a warning about text that is not on the page would be
		// paying for a sentence with no referent — and would tell the model
		// to distrust a description it cannot see, which is how an
		// instruction becomes noise.
		b.WriteString(" The parenthesised description of an item was written when it " +
			"was attached and these items CHANGE, so it may be out of date. Do not " +
			"answer about an item's present state from it: read the item again with " +
			"a tool using the id above, even if you have read it before in this " +
			"conversation. If you have no tool for it, say so rather than answering " +
			"from the description.")
	}
	return b.String()
}
