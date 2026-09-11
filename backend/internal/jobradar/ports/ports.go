// Package ports declares the driven side of the Job Radar context.
//
// Every method takes a workspace id and filters on it in SQL. An id that
// arrives from a URL or from a model must not be able to select a row it
// does not own, and the only way to guarantee that is for the predicate to
// be in the query rather than in a check the caller is trusted to have run.
package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/jobradar/domain"
)

// OpportunityFilter narrows a listing.
//
// ── Why these four and not a query language ────────────────────────────
// They are the questions the board and the agent actually ask: everything
// at one company, everything at one stage, a text match, and a bound on how
// much comes back. A general filter grammar would be a search engine, which
// this sprint explicitly is not building — and every field here maps to an
// index that already exists.
//
// The zero value lists the workspace's live opportunities, newest first.
type OpportunityFilter struct {
	// Company matches the company name, case-insensitively and by substring.
	// Substring rather than exact because the caller is often a model
	// working from "aquela da Stripe", and requiring the stored casing would
	// make a correct question fail.
	Company string
	// Stage narrows to one pipeline stage. Nil means every stage AND the
	// untracked records; see Untracked for the other half of that question.
	Stage *domain.PipelineStage
	// Untracked, when true, returns only records still in Discover. It is a
	// separate flag rather than a magic Stage value because "no stage" is
	// not a stage, and spelling it as one would put it in the enum.
	Untracked bool
	// Search matches role, company or location.
	Search string
	// Limit bounds the result. Zero means the repository's default.
	Limit  int
	Offset int
}

type OpportunityRepo interface {
	// List returns matching opportunities, newest first, with the company
	// name joined in.
	List(ctx context.Context, workspaceID uuid.UUID, f OpportunityFilter) ([]*domain.Opportunity, error)
	// Count is how many rows the same filter matches, ignoring Limit and
	// Offset — so a truncated listing can say how much it is not showing.
	Count(ctx context.Context, workspaceID uuid.UUID, f OpportunityFilter) (int64, error)
	// FindByID returns one live opportunity, or a not-found error. The
	// workspace is part of the lookup, not a check afterwards.
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Opportunity, error)
	Create(ctx context.Context, o *domain.Opportunity) error
	// FindByImportIdentity resolves a legacy record to the row it was
	// already imported as, if it was.
	//
	// It intentionally sees SOFT-DELETED rows too. "This legacy record has
	// been imported here" is a fact about history: a user who deleted an
	// imported opportunity meant to delete it, and re-running the import
	// must not quietly resurrect it. Returning nil,nil means "never seen".
	FindByImportIdentity(ctx context.Context, workspaceID uuid.UUID, id domain.ImportIdentity) (*domain.Opportunity, error)
	// ImportCreate writes an imported opportunity together with the whole
	// stage timeline it arrived with, in ONE transaction.
	//
	// Separate from Create for the reason the domain gives: it can backdate
	// created_at/updated_at and write historical transitions, and those are
	// powers no ordinary creation may have. One transaction because a row
	// whose history was half-written is worse than one that failed — the
	// caller can retry a failure, and the import identity makes that retry
	// safe.
	ImportCreate(ctx context.Context, o *domain.Opportunity, timeline []domain.StageEvent) error
	// Update writes the mutable fields of an opportunity. It does not touch
	// the tracking columns: a stage changes through Move, which also has to
	// record an event, and letting a generic update write a stage would be
	// the one path that could change a pipeline without leaving a trace.
	Update(ctx context.Context, o *domain.Opportunity) error
	// Move persists a transition and appends its stage event in one
	// transaction. Taking the already-transitioned entity plus the event
	// means the domain decided both, and the repository writes what it was
	// given rather than re-deriving the rule in SQL.
	Move(ctx context.Context, o *domain.Opportunity, event *domain.StageEvent) error
	// Untrack clears the three tracking columns, returning the record to
	// Discover. The stage events it already accumulated are kept: they are
	// what happened, and un-tracking is not a claim that it did not.
	Untrack(ctx context.Context, workspaceID, id uuid.UUID) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	// ListStageEvents returns one opportunity's transitions, oldest first.
	ListStageEvents(ctx context.Context, workspaceID, opportunityID uuid.UUID) ([]domain.StageEvent, error)
}

type CompanyRepo interface {
	// FindOrCreate resolves a company name to a row, creating it if this
	// workspace has never seen it.
	//
	// It is one method rather than a Find and a Create because the two are a
	// single decision with a race between them: two imports of the same
	// employer arriving together would both find nothing and both insert.
	// The implementation resolves that against the unique index instead of
	// hoping the caller serialises.
	// `domain` is the employer's website host when the caller knows one, and
	// empty when it does not. It is FILLED IN on a row that has none and
	// never overwrites one that does: an import carrying a blank must not
	// erase a domain someone typed, and the legacy document is not a more
	// authoritative source than the current record.
	FindOrCreate(ctx context.Context, workspaceID uuid.UUID, name, domain string) (*domain.Company, error)
	List(ctx context.Context, workspaceID uuid.UUID) ([]*domain.Company, error)
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Company, error)
}
