// Package ports declares the driven side of the Threads context.
//
// Every method takes a workspace id and filters on it in SQL. An id that
// arrives from a model must not be able to select a row it does not own,
// and the only way to guarantee that is for the predicate to be in the
// query rather than in a check the caller is trusted to have run.
package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/threads/domain"
)

// ThreadFilter narrows a listing.
//
// ── Why these three and not a query language ───────────────────────────
// They are the questions actually asked: "what am I working on", "what is
// in review", and "the one about microservices". A general filter grammar
// would be a search engine, which this sprint is explicitly not building —
// and both fields map to an index that already exists.
//
// The zero value lists the workspace's live threads, most recently touched
// first.
type ThreadFilter struct {
	// Status narrows to one lifecycle state. Nil means every state,
	// including archived and published: a listing that quietly hid the
	// finished work would make "how many have I written" unanswerable.
	Status *domain.Status
	// Search matches the title or the content, case-insensitively and by
	// substring. Substring rather than exact because the caller is usually a
	// model working from "aquele de microservices", and requiring the stored
	// wording would make a correct question fail.
	Search string
	// Limit bounds the result. Zero means the repository's default.
	Limit  int
	Offset int
}

type ThreadRepo interface {
	// List returns matching threads, most recently updated first.
	List(ctx context.Context, workspaceID uuid.UUID, f ThreadFilter) ([]*domain.Thread, error)
	// Count is how many rows the same filter matches, ignoring Limit and
	// Offset — so a truncated listing can say how much it is not showing.
	Count(ctx context.Context, workspaceID uuid.UUID, f ThreadFilter) (int64, error)
	// FindByID returns one live thread, or a not-found error. The workspace
	// is part of the lookup, not a check afterwards.
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Thread, error)
	Create(ctx context.Context, t *domain.Thread) error
	// Update writes the mutable fields of a thread.
	//
	// It takes the already-changed entity because the domain decided what
	// moved — see Thread.Apply. A repository that took a Change and built
	// its own SET clause would be a second place that knows what "editing a
	// thread" means, and the two would disagree the first time a field was
	// added to one of them.
	Update(ctx context.Context, t *domain.Thread) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
}
