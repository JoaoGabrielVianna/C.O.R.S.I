// Package app is the Job Radar application layer: the use cases every
// writer shares.
//
// ── Why this layer is the one the tools call ───────────────────────────
// There are three writers now — the UI, the importer, and an agent's tool —
// and they must not each re-derive what "move an opportunity" means. The
// rules live below this line exactly once: the company is resolved, the
// entity is validated, the domain performs the transition, the repository
// persists it with its event. A tool that reached the repository directly
// would skip the first two; a tool that reached the database directly would
// skip all four. Neither is possible from outside this package, because
// nothing outside it holds a repository.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/jobradar/domain"
	"github.com/corsi/backend/internal/jobradar/ports"
)

type Service struct {
	opportunities ports.OpportunityRepo
	companies     ports.CompanyRepo
	log           *slog.Logger
	// now is the clock, injectable so a test can assert on a timestamp
	// instead of asserting that one exists.
	now func() time.Time
}

func NewService(o ports.OpportunityRepo, c ports.CompanyRepo, log *slog.Logger) *Service {
	return &Service{opportunities: o, companies: c, log: log, now: time.Now}
}

// WithClock returns a copy that reads time from fn. Used by tests.
func (s *Service) WithClock(fn func() time.Time) *Service {
	cp := *s
	cp.now = fn
	return &cp
}

/* ── reads ───────────────────────────────────────────────────────────── */

// ListOpportunities returns matching records and the unbounded total.
//
// The total is returned alongside because every caller needs to know
// whether it is looking at everything: a board that shows 50 of 130 has to
// say so, and a model that received a truncated list and believed it was
// complete would answer "you have 50 opportunities" with confidence.
func (s *Service) ListOpportunities(ctx context.Context, workspaceID uuid.UUID, f ports.OpportunityFilter) ([]*domain.Opportunity, int64, error) {
	items, err := s.opportunities.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.opportunities.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetOpportunity(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Opportunity, error) {
	return s.opportunities.FindByID(ctx, workspaceID, id)
}

// GetOpportunityHistory returns one record together with its transitions.
func (s *Service) GetOpportunityHistory(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Opportunity, []domain.StageEvent, error) {
	o, err := s.opportunities.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, nil, err
	}
	events, err := s.opportunities.ListStageEvents(ctx, workspaceID, id)
	if err != nil {
		return nil, nil, err
	}
	return o, events, nil
}

func (s *Service) ListCompanies(ctx context.Context, workspaceID uuid.UUID) ([]*domain.Company, error) {
	return s.companies.List(ctx, workspaceID)
}

/* ── writes ──────────────────────────────────────────────────────────── */

// CreateInput is one new opportunity, as any caller states it.
//
// The company arrives as a NAME rather than an id because that is what
// every caller actually has: a person types "Stripe", an importer read a
// string, a model heard one. Resolving it to a row is this layer's job.
type CreateInput struct {
	CompanyName  string
	Role         string
	Salary       string
	Location     string
	Stack        []string
	Description  string
	Source       string
	SourceURL    string
	MatchPercent *int
	PostedAt     *time.Time
	// Stage optionally creates the record already in the pipeline. Nil
	// leaves it in Discover, which is where a newly discovered posting
	// belongs.
	Stage      *domain.PipelineStage
	NextAction string
	Notes      domain.Notes
	// TrackedAt and StageEnteredAt let a caller that already knows the
	// pipeline clock preserve it. Both are nil for a normal create, where
	// "now" is the truth.
	//
	// ── Why the importer needs them ────────────────────────────────────
	// The records being imported have been sitting in their stages for
	// weeks. Stamping them all with the import's own timestamp would reset
	// every clock at once, and "12 days in Applied" — the number that says
	// which application has gone quiet — would read as "0 days" for the
	// entire pipeline. That is data loss, not a rounding difference.
	TrackedAt      *time.Time
	StageEnteredAt *time.Time
}

func (s *Service) CreateOpportunity(ctx context.Context, workspaceID uuid.UUID, in CreateInput) (*domain.Opportunity, error) {
	// No domain: an ordinary create is a person typing a company name, and
	// there is no field on that form for a website. Passing empty leaves an
	// existing domain untouched — see the repository.
	company, err := s.companies.FindOrCreate(ctx, workspaceID, in.CompanyName, "")
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	postedAt := now
	if in.PostedAt != nil {
		postedAt = in.PostedAt.UTC()
	}
	source := in.Source
	if source == "" {
		source = "manual"
	}

	o := &domain.Opportunity{
		WorkspaceID:  workspaceID,
		CompanyID:    company.ID,
		CompanyName:  company.Name,
		Role:         in.Role,
		Salary:       in.Salary,
		Location:     in.Location,
		Stack:        domain.NormalizeStack(in.Stack),
		Description:  in.Description,
		Source:       source,
		SourceURL:    in.SourceURL,
		MatchPercent: in.MatchPercent,
		PostedAt:     postedAt,
		Notes:        in.Notes,
	}
	if in.Stage != nil {
		trackedAt, stageEnteredAt := now, now
		if in.TrackedAt != nil {
			trackedAt = in.TrackedAt.UTC()
		}
		if in.StageEnteredAt != nil {
			stageEnteredAt = in.StageEnteredAt.UTC()
		}
		o.Tracking = &domain.Tracking{
			Stage:          *in.Stage,
			TrackedAt:      trackedAt,
			StageEnteredAt: stageEnteredAt,
			NextAction:     in.NextAction,
		}
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if err := s.opportunities.Create(ctx, o); err != nil {
		return nil, err
	}

	// A record created straight into the pipeline entered a stage, and the
	// history has to say so — otherwise its first transition would appear to
	// come from nowhere.
	if o.Tracking != nil {
		// Stamped with the moment the stage was actually entered, not with
		// the moment this row was written. For a normal create the two are
		// the same instant; for an import they are weeks apart, and using
		// `now` would file every historical transition under today.
		event := &domain.StageEvent{ToStage: o.Tracking.Stage, OccurredAt: o.Tracking.StageEnteredAt}
		if err := s.opportunities.Move(ctx, o, event); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// UpdateInput carries only the fields a caller may change. A nil pointer
// means "leave this alone", which is what lets a partial edit from the UI
// and a full replacement from an importer use one method without the first
// one silently blanking the fields it did not mention.
type UpdateInput struct {
	CompanyName  *string
	Role         *string
	Salary       *string
	Location     *string
	Stack        *[]string
	Description  *string
	SourceURL    *string
	MatchPercent *int
	NextAction   *string
	Notes        *domain.Notes
}

func (s *Service) UpdateOpportunity(ctx context.Context, workspaceID, id uuid.UUID, in UpdateInput) (*domain.Opportunity, error) {
	o, err := s.opportunities.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	if in.CompanyName != nil {
		company, err := s.companies.FindOrCreate(ctx, workspaceID, *in.CompanyName, "")
		if err != nil {
			return nil, err
		}
		o.CompanyID, o.CompanyName = company.ID, company.Name
	}
	if in.Role != nil {
		o.Role = *in.Role
	}
	if in.Salary != nil {
		o.Salary = *in.Salary
	}
	if in.Location != nil {
		o.Location = *in.Location
	}
	if in.Stack != nil {
		o.Stack = domain.NormalizeStack(*in.Stack)
	}
	if in.Description != nil {
		o.Description = *in.Description
	}
	if in.SourceURL != nil {
		o.SourceURL = *in.SourceURL
	}
	if in.MatchPercent != nil {
		o.MatchPercent = in.MatchPercent
	}
	if in.Notes != nil {
		o.Notes = *in.Notes
	}
	if in.NextAction != nil {
		if o.Tracking == nil {
			// A next action is a property of being in the pipeline. Accepting
			// one for a Discover record would store a value nothing reads and
			// that the next move would silently overwrite.
			return nil, domain.Conflict("this opportunity is not in the pipeline, so it has no next action")
		}
		o.Tracking.NextAction = *in.NextAction
	}

	if err := o.Validate(); err != nil {
		return nil, err
	}
	if err := s.opportunities.Update(ctx, o); err != nil {
		return nil, err
	}
	return s.opportunities.FindByID(ctx, workspaceID, id)
}

// MoveOpportunity is the capability this sprint exists for.
//
// It is the ONLY way a stage changes. The repository's generic Update does
// not touch the tracking columns, so there is no path to a stage change
// that skips the event this method records.
func (s *Service) MoveOpportunity(ctx context.Context, workspaceID, id uuid.UUID, stage domain.PipelineStage) (domain.MoveResult, error) {
	o, err := s.opportunities.FindByID(ctx, workspaceID, id)
	if err != nil {
		return domain.MoveResult{}, err
	}

	now := s.now().UTC()
	result, err := o.Move(stage, now)
	if err != nil {
		return domain.MoveResult{}, err
	}
	// Already there. Reported as success with Unchanged set rather than as a
	// conflict: the caller asked for a state the record is in, and the
	// requested state is the resulting state. Writing anyway would restart
	// the stage clock and log a transition that did not happen.
	if result.Unchanged {
		return result, nil
	}

	event := &domain.StageEvent{
		FromStage:  result.PreviousStage,
		ToStage:    stage,
		OccurredAt: now,
	}
	if err := s.opportunities.Move(ctx, o, event); err != nil {
		return domain.MoveResult{}, err
	}

	s.log.Info("jobradar: opportunity moved",
		"opportunity_id", o.ID, "workspace_id", workspaceID,
		"from", stageOrDiscover(result.PreviousStage), "to", stage)
	return result, nil
}

func stageOrDiscover(s *domain.PipelineStage) string {
	if s == nil {
		return "discover"
	}
	return string(*s)
}

// UntrackOpportunity returns a record to Discover.
//
// Kept out of this sprint's tool surface — it is reachable from the UI,
// which is where it already was.
func (s *Service) UntrackOpportunity(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Opportunity, error) {
	o, err := s.opportunities.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if o.Tracking == nil {
		return o, nil
	}
	o.Tracking = nil
	if err := s.opportunities.Untrack(ctx, workspaceID, id); err != nil {
		return nil, err
	}
	return s.opportunities.FindByID(ctx, workspaceID, id)
}

func (s *Service) DeleteOpportunity(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.opportunities.SoftDelete(ctx, workspaceID, id)
}
