package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
)

// Provenance: linking a memory to the evidence behind it.
//
// ══════════════════════════════════════════════════════════════════════
//
//	APPEND-ONLY IN v1. NO UNLINK AND NO EDIT, FOR NOW
//
// ══════════════════════════════════════════════════════════════════════
//
// The question provenance answers is "why did I believe this", asked
// later, by somebody who was not there. A link that can be taken back
// casually answers a different question, and the value of the record is
// that it is not routinely tidied.
//
// It is also what makes the sensitivity floor hold rather than being
// advisory: a floor that can be lifted by unlinking is a floor that
// exists until it is inconvenient.
//
// This is a Palace Core v1 scope decision and NOT an eternal invariant.
// A consolidator, or a person moving quickly, can cite the wrong
// transcript, and the record then says a belief rests on something it
// never rested on. An explicit, named correction may be added later; it
// must not be a silent unlink, must not become a back door for lowering
// a memory's sensitivity, and must reckon with the floor it removes. See
// domain/provenance.go.

// LinkResult is what a completed link reports back.
//
// ── Why it reports whether it created anything ─────────────────────────
// Because linking the same source twice is a legitimate request with an
// already-satisfied answer, and "já estava registrado" is a different
// sentence from "registrei". Collapsing them would let a caller report
// work it did not do, which is the same honesty every Change in this
// context owes.
type LinkResult struct {
	// Created is false when the link was already there. The row is not
	// duplicated either way: the primary key is (memory_id, source_id)
	// and the insert defers to it.
	Created bool
}

// LinkSource records that a memory rests on a source.
//
// ── The order of the checks, and why it is this order ──────────────────
//  1. The memory is resolved IN THIS WORKSPACE. A foreign memory answers
//     not-found, identically to a fabricated id.
//  2. The source's sensitivity is read, which resolves it in the same
//     workspace and answers not-found on the same terms. Only the level
//     is read, not the transcript: see SourceRepo.SensitivityOf.
//  3. The floor rule is applied. A memory less withheld than its
//     evidence is refused, never silently raised.
//  4. Only then is anything written.
//
// Both ends are resolved before the write even though the composite
// foreign keys would also refuse a cross-workspace link. The keys are the
// backstop; this is what makes the answer a not-found instead of a
// constraint violation, and those two having different shapes is exactly
// what somebody would use to probe for a neighbour's rows.
func (s *Service) LinkSource(ctx context.Context, workspaceID, memoryID, sourceID uuid.UUID) (*LinkResult, error) {
	memory, err := s.memories.FindByID(ctx, workspaceID, memoryID)
	if err != nil {
		return nil, err
	}

	evidence, err := s.sources.SensitivityOf(ctx, workspaceID, sourceID)
	if err != nil {
		return nil, err
	}

	// ── The floor ──────────────────────────────────────────────────────
	// Refused, never repaired. Raising the memory would be the system
	// deciding, silently, that something the operator called ordinary is
	// now private, and they would find out when they went looking for it
	// in a listing where it no longer is.
	if err := domain.ValidateSensitivityFloor(memory.Sensitivity, evidence); err != nil {
		return nil, err
	}

	created, err := s.provenance.Link(ctx, workspaceID, memoryID, sourceID)
	if err != nil {
		return nil, err
	}
	return &LinkResult{Created: created}, nil
}

// SourcesFor returns the evidence behind one memory, oldest link first.
//
// ── Why the memory is resolved first ───────────────────────────────────
// So that a fabricated or foreign memory id answers not-found rather than
// an empty list. An empty list is a real and different answer: a memory
// nobody has cited evidence for.
//
// ── Why the result carries no sensitivity filter ───────────────────────
// Because there cannot be anything to withhold. Every link passed the
// floor rule and no source can be relabelled afterwards, so a memory is
// always at least as withheld as every source behind it. A caller
// holding the memory has already been admitted to something at least as
// restricted as anything this returns.
func (s *Service) SourcesFor(ctx context.Context, workspaceID, memoryID uuid.UUID) ([]*domain.Source, error) {
	if _, err := s.memories.FindByID(ctx, workspaceID, memoryID); err != nil {
		return nil, err
	}
	return s.provenance.SourcesFor(ctx, workspaceID, memoryID)
}

/* ── the floor, on the other door ────────────────────────────────────── */

// provenanceFloor returns the most withheld level among the evidence
// behind a memory, and whether there is any.
//
// ══════════════════════════════════════════════════════════════════════
//
//	WHY THIS EXISTS: A RULE ENFORCED AT ONE DOOR IS NOT A RULE
//
// ══════════════════════════════════════════════════════════════════════
//
// LinkSource refuses a memory that sits below its evidence. That closes
// the door where the link is made, and leaves another one open: link a
// private source to a private memory, then relabel the memory as
// `normal`. Nothing in the link path runs again, and the invariant it
// established is gone. The conclusion is now in every default listing
// while the material behind it is withheld, which is the exact leak the
// floor exists to prevent, reached by a different route.
//
// So UpdateMemory asks the same question before lowering a memory's
// level, and the answer comes from the same rule in the same domain
// function.
//
// ── Why the refusal is permanent, and why that is correct ──────────────
// Provenance is append-only, so there is no operation that removes the
// floor. A memory that cites private evidence can be raised freely and
// can never be lowered below private, for as long as it exists. That is
// stated in the message the caller receives, because somebody told only
// "refused" would reasonably go looking for an unlink that is not there.
func (s *Service) provenanceFloor(ctx context.Context, workspaceID, memoryID uuid.UUID) (domain.Sensitivity, bool, error) {
	levels, err := s.provenance.SourceSensitivities(ctx, workspaceID, memoryID)
	if err != nil {
		return domain.DefaultSensitivity, false, err
	}
	floor, ok := domain.MaxSensitivity(levels)
	return floor, ok, nil
}
