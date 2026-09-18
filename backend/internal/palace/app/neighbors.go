package app

// What is connected to one artifact, resolved safely.
//
// ══════════════════════════════════════════════════════════════════════
//
//	RELATIONS CONSUME SURFACE VISIBILITY. THEY DO NOT DEFINE IT
//
// ══════════════════════════════════════════════════════════════════════
//
// A relation is two ids, two type words and a kind. It carries no foreign
// key, because the endpoints are polymorphic and Postgres cannot reference
// one of three tables, so an id in `palace.relations` is whatever somebody
// once linked and has never been vetted since. That makes this the one
// read in the context where a caller holds ids it did not choose, and the
// obvious implementation is the dangerous one:
//
//	for each relation: FindByID(other end); if visible { show it }
//
// Wrong twice. It is a read per edge, and it LOADS the withheld row before
// deciding to drop it, which puts the operator's private title into memory,
// into a trace and into whatever records a slow query. A rule enforced
// after the content arrives is a rule that only governs the response body.
//
// So the resolution is a batch read under the surface's own predicate: the
// ineligible row is never selected. What comes back IS what may be shown,
// and every endpoint the batch did not return is dropped without a word.
//
// ── What silence means here ────────────────────────────────────────────
// No total, no count, no placeholder, no id of an unresolved endpoint. If
// ten relations exist and three neighbours are eligible, the caller knows
// about three, and there is nothing in the answer from which the other
// seven can be inferred. A raw relation count would be the whole leak in
// one integer.
//
// ── What this is not ───────────────────────────────────────────────────
// Not a graph query. One hop, no recursion, no path, no depth. The
// repository's own filter says the same thing and for the same reason.

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// neighborRelationCeiling bounds how many edges one artifact contributes.
//
// ── Why a ceiling instead of paging until exhausted ────────────────────
// Because the query count has to be constant in the number of relations.
// A loop would be one read per hundred edges, which is the N+1 this design
// exists to avoid, just with a bigger step.
//
// The repository lowers anything above its own ceiling, so this asks for
// as much as it will give. The consequence is stated rather than hidden:
// an artifact with more edges than the ceiling contributes only the first
// page of them, and the response cannot say so, because a response that
// could say so would be the count this endpoint must not carry. For a
// single operator's inspector that bound is far away; if it ever binds,
// the fix is a decision about what an inspector should show, not a silent
// loop added later.
const neighborRelationCeiling = 1000

// NeighborSet is a mixed group of endpoints, for the relation kinds whose
// other end may be either type.
type NeighborSet struct {
	Artifacts []*domain.Artifact
	Memories  []*domain.Memory
}

// ArtifactNeighbors is everything connected to one artifact, classified by
// what the link MEANS rather than by which column the anchor sat in.
//
// The fields are typed to the closed matrix rather than being six copies
// of the same mixed slice: `supersedes` can only ever be artifacts, and a
// field that admitted a memory would be a field somebody eventually fills
// with one. See domain/relation.go for the matrix.
type ArtifactNeighbors struct {
	// Supersedes: artifacts this one REPLACES. The anchor is the newer end.
	Supersedes []*domain.Artifact
	// SupersededBy: artifacts that replace this one. The anchor is older.
	SupersededBy []*domain.Artifact
	// Mentions: artifacts this one names.
	Mentions []*domain.Artifact
	// MentionedBy: whatever names this one. Memories and artifacts both.
	MentionedBy NeighborSet
	// Related: the undirected association, either way round.
	Related NeighborSet
	// Decisions: memories of kind `decision` that govern this artifact.
	Decisions []*domain.Memory
}

// ArtifactNeighbors resolves one artifact's immediate neighbours.
//
// ── The order of operations is the security property ───────────────────
//  1. The ANCHOR is resolved through the surface. A withheld anchor is a
//     not-found, and NO relation is read: asking "what is connected to
//     this" about something the caller may not see is already a question
//     the answer to which leaks, even if every neighbour were public.
//  2. Only then are the edges read.
//  3. The endpoints are resolved in two batch reads that carry the
//     surface's predicate into SQL.
//  4. Anything the batches did not return is dropped in silence.
//
// At most five statements, whatever the number of relations: the artifact,
// its room, the edges, the artifact endpoints, the memory endpoints.
func (s *Service) ArtifactNeighbors(ctx context.Context, workspaceID, artifactID uuid.UUID, v ports.Visibility) (*ArtifactNeighbors, error) {
	// 1. The anchor, first and alone.
	anchor, err := s.ArtifactForSurface(ctx, workspaceID, artifactID, v)
	if err != nil {
		return nil, err
	}

	// 2. The edges. Read through the repository rather than ListRelations,
	// because that use case also produces a total, and the total here is a
	// raw count of edges INCLUDING the ones whose endpoint will be
	// withheld. Computing a number this endpoint must never emit is a
	// number that eventually gets emitted.
	relations, err := s.relations.List(ctx, workspaceID, ports.RelationFilter{
		Anchor:    &ports.RelationAnchor{Type: domain.EntityArtifact, ID: anchor.ID},
		Direction: ports.DirectionAny,
		Page:      ports.Page{Limit: neighborRelationCeiling},
	})
	if err != nil {
		return nil, err
	}

	// 3. Partition the far ends by type, de-duplicated: two relations may
	// point at the same entity, and resolving it twice would be a wasted
	// row in the batch rather than a wrong answer.
	artifactIDs, memoryIDs := partitionEndpoints(anchor.ID, relations)

	resolvedArtifacts, err := s.artifacts.ListByIDs(ctx, workspaceID, artifactIDs, v)
	if err != nil {
		return nil, err
	}
	resolvedMemories, err := s.memories.ListByIDs(ctx, workspaceID, memoryIDs, v)
	if err != nil {
		return nil, err
	}

	// 4. Index what came back. Everything else is gone, and nothing counts
	// it on the way out.
	artifactsByID := make(map[uuid.UUID]*domain.Artifact, len(resolvedArtifacts))
	for _, a := range resolvedArtifacts {
		artifactsByID[a.ID] = a
	}
	memoriesByID := make(map[uuid.UUID]*domain.Memory, len(resolvedMemories))
	for _, m := range resolvedMemories {
		memoriesByID[m.ID] = m
	}

	out := &ArtifactNeighbors{}
	for _, rel := range relations {
		classifyNeighbor(out, anchor.ID, rel, artifactsByID, memoriesByID)
	}
	return out, nil
}

// partitionEndpoints collects the FAR end of every relation, by type.
//
// The anchor's own id is never collected: a relation cannot be reflexive
// (the domain and a CHECK both refuse it), so the far end is simply
// whichever side is not the anchor.
func partitionEndpoints(anchorID uuid.UUID, relations []*domain.Relation) (artifactIDs, memoryIDs []uuid.UUID) {
	seenArtifacts := map[uuid.UUID]bool{}
	seenMemories := map[uuid.UUID]bool{}

	for _, rel := range relations {
		farType, farID := farEnd(anchorID, rel)
		switch farType {
		case domain.EntityArtifact:
			if !seenArtifacts[farID] {
				seenArtifacts[farID] = true
				artifactIDs = append(artifactIDs, farID)
			}
		case domain.EntityMemory:
			if !seenMemories[farID] {
				seenMemories[farID] = true
				memoryIDs = append(memoryIDs, farID)
			}
		}
		// A room endpoint is possible in the matrix (decision_for may point
		// at a room) but never when the anchor is an artifact, and this
		// endpoint is about an artifact. Unhandled on purpose rather than
		// resolved into a third batch nobody asked for.
	}
	return artifactIDs, memoryIDs
}

// farEnd names the side of a relation that is not the anchor.
func farEnd(anchorID uuid.UUID, rel *domain.Relation) (domain.EntityType, uuid.UUID) {
	if rel.FromType == domain.EntityArtifact && rel.FromID == anchorID {
		return rel.ToType, rel.ToID
	}
	return rel.FromType, rel.FromID
}

// classifyNeighbor files one relation under what it MEANS for the anchor.
//
// ── SUPERSEDES: the direction comes from the domain, never from here ───
// Both ends of a SUPERSEDES are artifacts, so nothing about the row says
// which is the replacement. The domain froze the answer and exposes it as
// `Relation.Superseding()`, and this function asks rather than reading
// `from` and `to` itself. A reader that guesses backwards gets no error:
// it gets a coherent, confident, inverted history in which the abandoned
// version is the current one, and no test after the fact can tell, because
// both readings are internally consistent.
func classifyNeighbor(
	out *ArtifactNeighbors,
	anchorID uuid.UUID,
	rel *domain.Relation,
	artifacts map[uuid.UUID]*domain.Artifact,
	memories map[uuid.UUID]*domain.Memory,
) {
	if newer, older, ok := rel.Superseding(); ok {
		// The anchor is one of the two ends. If it is the newer one, it
		// supersedes the other; otherwise the other supersedes it.
		if newer == anchorID {
			if a, found := artifacts[older]; found {
				out.Supersedes = append(out.Supersedes, a)
			}
			return
		}
		if a, found := artifacts[newer]; found {
			out.SupersededBy = append(out.SupersededBy, a)
		}
		return
	}

	farType, farID := farEnd(anchorID, rel)
	anchorIsOrigin := rel.FromType == domain.EntityArtifact && rel.FromID == anchorID

	switch rel.Kind {
	case domain.RelationMentions:
		if anchorIsOrigin {
			// mentions from an artifact can only reach an artifact.
			if a, found := artifacts[farID]; found {
				out.Mentions = append(out.Mentions, a)
			}
			return
		}
		addToSet(&out.MentionedBy, farType, farID, artifacts, memories)

	case domain.RelationRelatedTo:
		addToSet(&out.Related, farType, farID, artifacts, memories)

	case domain.RelationDecisionFor:
		// The matrix only allows memory → artifact, so the anchor is always
		// the target and the far end is always a memory.
		if m, found := memories[farID]; found {
			out.Decisions = append(out.Decisions, m)
		}
	}
}

func addToSet(
	set *NeighborSet,
	farType domain.EntityType,
	farID uuid.UUID,
	artifacts map[uuid.UUID]*domain.Artifact,
	memories map[uuid.UUID]*domain.Memory,
) {
	switch farType {
	case domain.EntityArtifact:
		if a, found := artifacts[farID]; found {
			set.Artifacts = append(set.Artifacts, a)
		}
	case domain.EntityMemory:
		if m, found := memories[farID]; found {
			set.Memories = append(set.Memories, m)
		}
	}
}
