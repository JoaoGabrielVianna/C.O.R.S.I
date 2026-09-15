package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// Artifact items: the structured state of an artifact.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EVERY OPERATION NAMES THE ARTIFACT, NOT JUST THE ENTRY
//
// ══════════════════════════════════════════════════════════════════════
//
// An entry id is a plain uuid. Addressing by it alone would let a caller
// holding one from a checklist tick a box on a different checklist, or on
// a stranger's, and the statement would succeed. So the artifact travels
// with the entry from this layer down into the WHERE clause, and an entry
// id from the wrong artifact is not-found: the same answer as one that
// never existed.

/* ── reads ───────────────────────────────────────────────────────────── */

// ListItems returns one artifact's entries and the unbounded total.
//
// ── Why the artifact is resolved first ─────────────────────────────────
// Because "this artifact has no entries" and "there is no such artifact"
// are different answers, and a caller that received an empty list for a
// fabricated id would go on believing the artifact exists. Resolving
// costs one narrow read and leaks nothing: a foreign artifact and one
// that never existed both answer not-found.
func (s *Service) ListItems(ctx context.Context, workspaceID, artifactID uuid.UUID, p ports.Page) ([]*domain.ArtifactItem, int64, error) {
	if err := s.requireArtifact(ctx, workspaceID, &artifactID); err != nil {
		return nil, 0, err
	}
	items, err := s.items.ListByArtifact(ctx, workspaceID, artifactID, p)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.items.CountByArtifact(ctx, workspaceID, artifactID)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetItem(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID) (*domain.ArtifactItem, error) {
	return s.items.FindByID(ctx, workspaceID, artifactID, itemID)
}

/* ── writes ──────────────────────────────────────────────────────────── */

// AddItemInput is one new entry.
type AddItemInput struct {
	Text string
	// Position is the requested slot, or nil to append after the last
	// entry. Nil is the ordinary case: a person adding to a list means
	// "at the end", and making them compute an index would be asking them
	// to know how long the list is.
	Position *int
	// Done is rare on creation and occasionally right: somebody writing
	// down a checklist of things they already did.
	Done bool
}

// AddItem writes one entry to an artifact.
//
// ══════════════════════════════════════════════════════════════════════
//
//	ArtifactKind.AcceptsItems IS THE AUTHORITY, AND IT IS CHECKED HERE
//
// ══════════════════════════════════════════════════════════════════════
//
// A note is prose: what it says is its body, and a checklist hanging off
// it would be a second, competing statement of what the note contains,
// free to disagree with the sentence above it. The other three kinds
// decompose by nature.
//
// ── Why the check is in this layer and not in the schema ───────────────
// Because it is a rule about two tables: whether an entry may exist
// depends on a column of a DIFFERENT row. Postgres could express it with
// a trigger, and a trigger is a second place that would have to learn
// `AcceptsItems` and could disagree with it. The kind vocabulary has one
// validator, in the domain, and this is the one caller that asks it.
//
// ── Why the kind is read rather than the artifact ──────────────────────
// The decision depends on one word. Loading the artifact would pull its
// title and body into a path whose output is a yes or a no, and a row in
// hand is a row that can be logged by accident.
func (s *Service) AddItem(ctx context.Context, workspaceID, artifactID uuid.UUID, in AddItemInput) (*domain.ArtifactItem, error) {
	kind, err := s.artifacts.KindOf(ctx, workspaceID, artifactID)
	if err != nil {
		// Not-found already, for a foreign artifact and for a fabricated
		// one alike.
		return nil, err
	}
	if !kind.AcceptsItems() {
		return nil, domain.Invalid(
			"an artifact of kind %s does not carry entries; the kinds that do are %s",
			kind, itemBearingKinds())
	}

	item := &domain.ArtifactItem{
		WorkspaceID: workspaceID,
		ArtifactID:  artifactID,
		Text:        strings.TrimSpace(in.Text),
		Done:        in.Done,
	}
	// Validated with the requested position when there is one, and with
	// zero when the repository is going to choose: a nil position cannot
	// be out of range, and the value the repository writes is a maximum
	// plus one, which is never negative.
	if in.Position != nil {
		item.Position = *in.Position
	}
	if err := item.Validate(); err != nil {
		return nil, err
	}

	if err := s.items.Create(ctx, workspaceID, artifactID, item, in.Position); err != nil {
		return nil, err
	}
	return item, nil
}

// ItemUpdateResult is a completed edit: the entry as it now is, plus what
// actually moved.
type ItemUpdateResult struct {
	Item *domain.ArtifactItem
	domain.ItemChangeResult
}

// UpdateItem applies a partial change to one entry.
//
// ── Why the artifact's kind is not re-checked here ─────────────────────
// Because an entry that exists under an artifact was admitted when it was
// added, and a kind cannot change. There is no state in which a live
// entry sits under a note, so checking would be asking a question whose
// answer is already settled by the row's existence.
func (s *Service) UpdateItem(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID, c domain.ItemChange) (*ItemUpdateResult, error) {
	if c.Empty() {
		return nil, domain.Invalid("an update must change at least one of text, done or position")
	}

	item, err := s.items.FindByID(ctx, workspaceID, artifactID, itemID)
	if err != nil {
		return nil, err
	}

	changed := item.Apply(c)
	if err := item.Validate(); err != nil {
		return nil, err
	}

	if changed.Unchanged() {
		return &ItemUpdateResult{Item: item, ItemChangeResult: changed}, nil
	}

	if err := s.items.Update(ctx, workspaceID, artifactID, item); err != nil {
		return nil, err
	}
	return &ItemUpdateResult{Item: item, ItemChangeResult: changed}, nil
}

// RemoveItem takes one entry off a checklist.
//
// Soft, and it is the one `deleted_at` in this schema that v1 actually
// writes: taking a line off a list is ordinary use, unlike deleting a
// memory. It is still not a hard delete, because an entry may be the
// subject of a memory tomorrow.
//
// Removing twice is not-found rather than a silent success, which is what
// lets a caller tell "I took it off" from "it was already gone".
func (s *Service) RemoveItem(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID) error {
	return s.items.SoftDelete(ctx, workspaceID, artifactID, itemID)
}

// itemBearingKinds names the kinds that carry entries, for the refusal
// above.
//
// Built from AcceptsItems rather than written out, so a kind whose answer
// changes cannot leave a stale list in an error message that then teaches
// a model the wrong thing.
func itemBearingKinds() string {
	var accepted []string
	for _, k := range domain.ArtifactKinds {
		if k.AcceptsItems() {
			accepted = append(accepted, k.String())
		}
	}
	return strings.Join(accepted, ", ")
}
