package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── reads ───────────────────────────────────────────────────────────── */

// ListMemories returns matching memories and the unbounded total, which
// obeys the same sensitivity rule as the list. See ListRooms.
func (s *Service) ListMemories(ctx context.Context, workspaceID uuid.UUID, f ports.MemoryFilter) ([]*domain.Memory, int64, error) {
	items, err := s.memories.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.memories.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetMemory(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Memory, error) {
	return s.memories.FindByID(ctx, workspaceID, id)
}

/* ── writes ──────────────────────────────────────────────────────────── */

// CreateMemoryInput is one new memory, as any caller states it.
//
// ── ArtifactID, and why it is here now ─────────────────────────────────
// It was absent until an Artifact repository existed to RESOLVE it. That
// was not caution: every reference in this context is resolved in the
// caller's workspace before the write, and accepting a field this layer
// could not resolve would have meant either trusting an id from a model,
// which the isolation model refuses outright, or leaning on the composite
// foreign key, which answers with a constraint violation where a
// fabricated id answers not-found. Those two having different shapes is
// exactly how somebody probes for a neighbour's rows.
//
// The field arrived as predicted: an input that gained a field, with
// nothing removed and no behaviour replaced.
type CreateMemoryInput struct {
	Kind    domain.MemoryKind
	Content string
	Summary string
	// Importance is optional. Nil means DefaultImportance: a caller who
	// did not rank something has not called it trivial either.
	Importance *int
	// Confidence is optional. Nil means DefaultConfidence.
	Confidence *domain.Confidence
	// OccurredAt is when the thing this memory is about happened.
	// Optional, because plenty of knowledge has no date.
	//
	// It is taken from the caller rather than defaulted to now, and the
	// difference is the point: a memory written today about a decision
	// made in March is dated March, and a memory with no date is not
	// pretending to have one. A caller that means "now" asks Service.Now,
	// which reads the database clock.
	OccurredAt *time.Time
	// RoomID optionally files this memory. Resolved in the caller's
	// workspace before anything is written.
	RoomID *uuid.UUID
	// ArtifactID optionally says what this memory is ABOUT. Resolved in
	// the caller's workspace before anything is written.
	//
	// ── This is context, not provenance, and the difference matters ────
	// Attaching a memory to an artifact does NOT constrain its
	// sensitivity, and does not inherit the artifact's. Only provenance
	// establishes a floor, because only provenance says the memory rests
	// on the other thing: a note filed against a private project is a
	// note about that project, not a note derived from its contents.
	//
	// The consequence is worth naming rather than discovering: a `normal`
	// memory attached to a `highly_sensitive` artifact appears in default
	// listings while the artifact does not. That is correct under this
	// design and it is the operator's call, which is why the two links
	// are separate operations with separate words.
	ArtifactID *uuid.UUID
	// Sensitivity is optional. Nil means DefaultSensitivity.
	Sensitivity *domain.Sensitivity
}

func (s *Service) CreateMemory(ctx context.Context, workspaceID uuid.UUID, in CreateMemoryInput) (*domain.Memory, error) {
	importance := domain.DefaultImportance
	if in.Importance != nil {
		importance = *in.Importance
	}
	confidence := domain.DefaultConfidence
	if in.Confidence != nil {
		confidence = *in.Confidence
	}
	sensitivity := domain.DefaultSensitivity
	if in.Sensitivity != nil {
		sensitivity = *in.Sensitivity
	}

	m := &domain.Memory{
		WorkspaceID: workspaceID,
		Kind:        in.Kind,
		Content:     in.Content,
		Summary:     in.Summary,
		Importance:  importance,
		Confidence:  confidence,
		OccurredAt:  in.OccurredAt,
		RoomID:      in.RoomID,
		ArtifactID:  in.ArtifactID,
		Status:      domain.DefaultLifecycle,
		Sensitivity: sensitivity,
	}
	// Shape first, then the references. A malformed memory naming a room
	// this workspace does not have should be told what is wrong with the
	// memory, and asking the database about a room for a row that could
	// never be written is a query nobody needed.
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := s.requireRoom(ctx, workspaceID, in.RoomID); err != nil {
		return nil, err
	}
	if err := s.requireArtifact(ctx, workspaceID, in.ArtifactID); err != nil {
		return nil, err
	}
	if err := s.memories.Create(ctx, workspaceID, m); err != nil {
		return nil, err
	}
	return m, nil
}

// MemoryUpdateResult is a completed edit: the memory as it now is, plus
// what actually moved.
type MemoryUpdateResult struct {
	Memory *domain.Memory
	domain.MemoryChangeResult
}

// UpdateMemory applies a partial change to one memory.
//
// Reads before it writes, and refuses an empty change, for the reasons
// UpdateRoom states in full.
//
// ── A memory that governs something cannot stop being a decision ───────
// The rule was normative from the moment Relation was designed and is
// executable now that Relation has a repository: a memory that is the
// ORIGIN of a live DECISION_FOR may not be reclassified to any kind
// other than `decision` while that relation stands.
//
// Explicitly NOT the fix: cascading the delete, dropping the relation,
// converting it to RELATED_TO, or any other silent repair. The operator
// asked to change one thing, and a system that also destroyed a link
// they did not mention would be deciding on their behalf about the
// record of why something was done.
//
// The way through is two explicit steps, in this order:
//
//	palace.relation.remove  →  memory.update(kind = …)
func (s *Service) UpdateMemory(ctx context.Context, workspaceID, id uuid.UUID, c domain.MemoryChange) (*MemoryUpdateResult, error) {
	if c.Empty() {
		return nil, domain.Invalid("an update must change at least one field of the memory")
	}

	m, err := s.memories.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	// The references are resolved BEFORE the change is applied, so an
	// edit naming a row this workspace does not have leaves the entity in
	// memory untouched. Applying first and failing after would be
	// harmless today, because nothing is written, and would stop being
	// harmless the first time somebody reused the entity after the error.
	if roomID, ok := c.Room.ID(); ok {
		if err := s.requireRoom(ctx, workspaceID, &roomID); err != nil {
			return nil, err
		}
	}
	if artifactID, ok := c.Artifact.ID(); ok {
		if err := s.requireArtifact(ctx, workspaceID, &artifactID); err != nil {
			return nil, err
		}
	}

	// ── The sensitivity floor, on the door LinkSource does not watch ───
	// Linking refuses a memory that sits below its evidence. Relabelling
	// would reach the same state by another route: link a private source
	// to a private memory, then call it `normal`, and the conclusion is
	// in every default listing while the material behind it is withheld.
	// A rule enforced at one door is not a rule.
	//
	// Asked only when the level is actually being LOWERED. Raising it, or
	// resending the stored value, cannot violate a floor that already
	// held, and a query on every memory edit would be a cost paid for
	// nothing. See provenanceFloor.
	if c.Sensitivity != nil && !c.Sensitivity.AtLeastAsRestrictiveAs(m.Sensitivity) {
		floor, hasEvidence, err := s.provenanceFloor(ctx, workspaceID, id)
		if err != nil {
			return nil, err
		}
		if hasEvidence {
			if err := domain.ValidateSensitivityFloor(*c.Sensitivity, floor); err != nil {
				return nil, err
			}
		}
	}

	// ── The decision_for guard ─────────────────────────────────────────
	// Asked only when the requested kind is something other than
	// `decision`. A memory that is not moving away from `decision` cannot
	// break the rule, and a query on every memory edit would be a cost
	// paid for nothing.
	if c.Kind != nil && *c.Kind != domain.MemoryDecision {
		governs, err := s.relations.HasDecisionFor(ctx, workspaceID, id)
		if err != nil {
			return nil, err
		}
		if governs {
			return nil, domain.Invalid(
				"this memory is the origin of a %s relation, so it must stay a memory of "+
					"kind %s; remove that relation first if the reclassification is intended",
				domain.RelationDecisionFor, domain.MemoryDecision)
		}
	}

	changed := m.Apply(c)
	if err := m.Validate(); err != nil {
		return nil, err
	}

	if changed.Unchanged() {
		return &MemoryUpdateResult{Memory: m, MemoryChangeResult: changed}, nil
	}

	if err := s.memories.Update(ctx, workspaceID, m); err != nil {
		return nil, err
	}
	return &MemoryUpdateResult{Memory: m, MemoryChangeResult: changed}, nil
}

/* ── reference resolution ────────────────────────────────────────────── */

// requireRoom confirms that this workspace has this room.
//
// ── Why the answer for a foreign room is `not_found` ───────────────────
// Because it must be the same answer a fabricated id gets. A room that
// exists in another workspace and a room that never existed are one
// response on purpose: any difference between them, an error code, a
// wording, even a latency, is a way to confirm the existence of somebody
// else's record.
//
// ── Why this exists when the foreign key would also refuse ─────────────
// The composite key `(workspace_id, room_id)` is the backstop and it does
// refuse the row. What it cannot do is refuse it as a not-found: it
// raises a constraint violation, which is a different answer with a
// different shape. This check is what makes the two indistinguishable,
// and it is why the sanitised database error in adapters/repo is a
// backstop rather than the mechanism.
//
// nil is not a reference and is not an error: a memory that belongs to no
// room is an ordinary memory.
func (s *Service) requireRoom(ctx context.Context, workspaceID uuid.UUID, roomID *uuid.UUID) error {
	if roomID == nil {
		return nil
	}
	found, err := s.rooms.Exists(ctx, workspaceID, *roomID)
	if err != nil {
		return err
	}
	if !found {
		return domain.NotFound("room %s was not found", *roomID)
	}
	return nil
}
