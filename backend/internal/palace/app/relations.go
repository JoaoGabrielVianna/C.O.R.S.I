package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// Relations: the controlled links between Palace entities.
//
// ══════════════════════════════════════════════════════════════════════
//
//	BOTH ENDS ARE RESOLVED HERE. THE DATABASE CANNOT DO IT
//
// ══════════════════════════════════════════════════════════════════════
//
// `palace.relations` carries no foreign key, because the endpoints are
// polymorphic and Postgres cannot reference one of three tables. Every
// other table in this context has a composite key standing behind the
// application layer as a backstop; this one has nothing. Whatever this
// service fails to check is not checked at all, and a fabricated id
// inserts cleanly and points at nothing for as long as the row lives.
//
// So the checks below are not belt and braces. They are the only braces.

// CreateRelationInput is one new edge, as any caller states it.
type CreateRelationInput struct {
	FromType domain.EntityType
	FromID   uuid.UUID
	Kind     domain.RelationKind
	ToType   domain.EntityType
	ToID     uuid.UUID
}

// RelationCreateResult is what a completed create reports back.
type RelationCreateResult struct {
	Relation *domain.Relation
	// Created is false when an identical edge already existed. The
	// relation returned is then the one that was already there, with its
	// own id, so a caller that meant to remove it still can.
	//
	// ── Why a duplicate is not an error ────────────────────────────────
	// Because asserting the same link twice is a request whose answer is
	// already true. Refusing would make a model apologise for a state
	// that is correct and retry; saying "já estava registrado" is the
	// honest sentence, and it is different from "registrei", which is
	// what Created carries.
	Created bool
}

// CreateRelation draws one controlled edge.
//
// ── The order of the checks, and why it is this order ──────────────────
//  1. SHAPE, in the domain: the vocabularies, both ids present, not
//     reflexive, and the pairing permitted by the closed matrix. Cheap,
//     and it refuses a malformed request without asking the database
//     anything.
//  2. The ORIGIN is resolved in this workspace, and must be live.
//  3. The TARGET is resolved in this workspace, and must be live.
//  4. DECISION_FOR, which needs the origin memory's kind, and is the one
//     rule the matrix cannot express on its own.
//  5. Only then is anything written.
//
// Steps 2 and 3 answer not-found for an endpoint that never existed, was
// deleted, or belongs to another workspace. The three are one answer on
// purpose: any difference between them is a way to confirm the existence
// of somebody else's record, and here there is no foreign key to fall
// back on.
func (s *Service) CreateRelation(ctx context.Context, workspaceID uuid.UUID, in CreateRelationInput) (*RelationCreateResult, error) {
	rel := &domain.Relation{
		WorkspaceID: workspaceID,
		FromType:    in.FromType,
		FromID:      in.FromID,
		Kind:        in.Kind,
		ToType:      in.ToType,
		ToID:        in.ToID,
	}
	if err := rel.Validate(); err != nil {
		return nil, err
	}

	// ── The endpoints ──────────────────────────────────────────────────
	// Resolved with narrow reads: a status, and a kind when the rule
	// needs one. Nothing loads a room's description, an artifact's body
	// or a memory's sentence to prove that it exists, because a row in
	// hand is a row that can be logged by accident, and none of these
	// paths has any use for the content.
	if err := s.requireLiveEndpoint(ctx, workspaceID, in.FromType, in.FromID); err != nil {
		return nil, err
	}
	if err := s.requireLiveEndpoint(ctx, workspaceID, in.ToType, in.ToID); err != nil {
		return nil, err
	}

	// ── decision_for ───────────────────────────────────────────────────
	// The normative rule, now executable. A decision_for must start at a
	// memory whose kind is decision; the matrix already guaranteed the
	// origin is a memory, so this is the remaining half.
	if rel.Kind == domain.RelationDecisionFor {
		kind, err := s.memories.KindOf(ctx, workspaceID, in.FromID)
		if err != nil {
			return nil, err
		}
		origin := &domain.Memory{Kind: kind}
		if err := rel.ValidateDecisionOrigin(origin); err != nil {
			return nil, err
		}
	}

	created, err := s.relations.Create(ctx, workspaceID, rel)
	if err != nil {
		return nil, err
	}
	return &RelationCreateResult{Relation: rel, Created: created}, nil
}

// ListRelations returns matching edges and the unbounded total.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THIS IS ADJACENCY. IT DOES NOT WALK THE GRAPH
//
// ══════════════════════════════════════════════════════════════════════
//
// Anchored at an entity, it returns the edges that TOUCH it, and
// nothing further. An edge from B to C is not returned because A points
// at B: following an edge is the caller's act, one listing at a time.
//
// ── Why the anchor is not resolved first ───────────────────────────────
// Unlike ListItems, which refuses a fabricated artifact, this listing
// returns an empty list for an anchor that does not exist. Two reasons,
// and the second is the load-bearing one.
//
// A fabricated anchor leaks nothing: the workspace predicate already
// makes a neighbour's entity indistinguishable from one that never was,
// and an empty list is what both produce.
//
// And relations can legitimately OUTLIVE their endpoints. This table has
// no foreign key and nothing cascades, so an edge whose endpoint was
// hard-deleted is still a true record of what was once connected.
// Requiring the anchor to resolve would make exactly those edges
// unreadable, which is the opposite of what a historical record is for.
func (s *Service) ListRelations(ctx context.Context, workspaceID uuid.UUID, f ports.RelationFilter) ([]*domain.Relation, int64, error) {
	if f.Anchor != nil && !f.Anchor.Type.Valid() {
		return nil, 0, domain.Invalid("unknown entity type for the anchor; the types are %s",
			joinTypes(domain.EntityTypeNames()))
	}
	if !f.Direction.Valid() {
		return nil, 0, domain.Invalid("unknown direction %q; use from, to, or leave it empty for both",
			f.Direction)
	}

	items, err := s.relations.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.relations.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetRelation(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Relation, error) {
	return s.relations.FindByID(ctx, workspaceID, id)
}

// RemoveRelation deletes one edge.
//
// ══════════════════════════════════════════════════════════════════════
//
//	IT REMOVES THE EDGE AND NOTHING ELSE
//
// ══════════════════════════════════════════════════════════════════════
//
// No entity is read, written, archived or touched. Removing the edge
// that said "B supersedes A" does not revive A, does not archive B, and
// does not change either one's lifecycle, sensitivity or content. The
// relation recorded a claim; deleting it withdraws the claim.
//
// This is also the FIRST STEP of the only correct way to reclassify a
// memory that governs something: remove the decision_for, then change the
// kind. The service refuses the reverse order deliberately rather than
// unpicking the relation for the caller, because a system that also
// destroyed a link nobody mentioned would be deciding, on their behalf,
// about the record of why something was done.
func (s *Service) RemoveRelation(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.relations.Delete(ctx, workspaceID, id)
}

/* ── endpoint resolution ─────────────────────────────────────────────── */

// requireLiveEndpoint resolves one end of a relation in this workspace
// and refuses it if it has been archived.
//
// ── Why archived is refused for a NEW relation ─────────────────────────
// Archiving says "not this, for now". Drawing a fresh line to something
// retired is either a mistake or a sign that it should be active again,
// and both are better answered now than by a record nobody can interpret
// later.
//
// ── And why nothing happens to relations that already exist ────────────
// Archiving an entity does not touch the edges already attached to it.
// They stay listable, nothing cascades, and no edge is removed: the
// history of what was connected is not invalidated by somebody tidying
// up, and a cascade would silently destroy the record that often
// explains why the thing was archived at all.
func (s *Service) requireLiveEndpoint(ctx context.Context, workspaceID uuid.UUID, t domain.EntityType, id uuid.UUID) error {
	var (
		status domain.Lifecycle
		err    error
	)
	switch t {
	case domain.EntityRoom:
		status, err = s.rooms.StatusOf(ctx, workspaceID, id)
	case domain.EntityArtifact:
		status, err = s.artifacts.StatusOf(ctx, workspaceID, id)
	case domain.EntityMemory:
		status, err = s.memories.StatusOf(ctx, workspaceID, id)
	default:
		// Unreachable through CreateRelation, which validates the
		// vocabulary first. Kept as a refusal rather than a panic because
		// a future caller reaching here with a new entity type should be
		// told, not crashed.
		return domain.Invalid("unknown entity type %q for a relation endpoint", t)
	}
	if err != nil {
		// Already this context's not-found: never existed, deleted, or
		// another workspace's, indistinguishably.
		return err
	}
	return domain.ValidateEndpointAlive(t, status)
}

// joinTypes renders the entity vocabulary for an error message. A local
// helper so this file does not import strings for one call.
func joinTypes(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
