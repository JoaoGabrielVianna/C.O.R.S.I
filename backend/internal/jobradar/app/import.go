package app

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/jobradar/domain"
)

// Importing a legacy document.
//
// ── Why this is a method here and not a second importer ────────────────
// It goes through the same application layer, the same domain validation
// and the same repository as everything else; what it adds is the three
// powers a migration needs and an ordinary create must never have — a
// stable identity, historical timestamps, and a pre-existing timeline.
//
// Those powers are the reason for a separate INPUT TYPE rather than three
// more optional fields on CreateInput. The HTTP layer already keeps two
// DTOs apart, but "the normal handler happens not to read those fields" is
// the kind of guarantee that quietly stops being true. A type that cannot
// express backdating is one nobody has to remember not to use.

// ImportInput is one legacy record, as the migration states it.
type ImportInput struct {
	// CreateInput carries everything an ordinary creation carries, so the
	// rules for a role, a stack or a stage are written once and an import
	// cannot drift from them.
	CreateInput

	// Identity is which legacy record this is. Without it the import has no
	// way to recognise a retry, so it is required here even though the
	// column allows its absence for hand-created rows.
	Identity domain.ImportIdentity
	// CompanyDomain is the employer's website, when the document knew one.
	// Filled into a company that has none; never overwrites one that does.
	CompanyDomain string
	// CreatedAt and UpdatedAt are the record's own clock. Nil means the
	// document did not say, and the database's default applies.
	CreatedAt *time.Time
	UpdatedAt *time.Time
	// History is the legacy `tracking.history`: where the record has been,
	// in order. Empty is legitimate — a record that never moved has no
	// transitions — and is not the same as absent history on a record that
	// did move, which this layer cannot detect and does not pretend to.
	History []domain.StageVisit
}

// ImportResult is what happened to one item.
type ImportResult struct {
	Opportunity *domain.Opportunity
	// AlreadyImported is true when this legacy record was recognised as one
	// this workspace has already migrated. The Opportunity is the row it
	// became, so a caller can report what it points at rather than only
	// that nothing happened.
	AlreadyImported bool
}

// ImportOpportunity migrates one legacy record, or recognises that it has
// already been migrated.
//
// ── The idempotency argument, in full ──────────────────────────────────
// The check is a lookup on (workspace, import_source, import_external_id)
// backed by a UNIQUE index on the same three columns. The lookup makes the
// common retry cheap and gives a good answer; the index is what makes the
// guarantee true, because two concurrent imports of one document would both
// pass the lookup and only one can pass the constraint.
//
// Note what is NOT used: company, role, URL, or any other visible field.
// Two genuinely different postings for the same role at the same employer
// are two legacy ids, and they stay two rows. That is the requirement that
// rules out every heuristic anybody would otherwise reach for.
func (s *Service) ImportOpportunity(ctx context.Context, workspaceID uuid.UUID, in ImportInput) (*ImportResult, error) {
	if err := in.Identity.Validate(); err != nil {
		return nil, err
	}
	if in.Identity.Zero() {
		// Refused rather than imported anonymously. A row with no identity
		// could never be recognised on a retry, so accepting it would be
		// accepting a duplicate on the next run — the exact failure this
		// whole mechanism exists to remove.
		return nil, domain.Invalid(
			"this record carries no legacy id, so a repeated import could not " +
				"recognise it; every imported record needs one")
	}

	existing, err := s.opportunities.FindByImportIdentity(ctx, workspaceID, in.Identity)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &ImportResult{Opportunity: existing, AlreadyImported: true}, nil
	}

	// ── Everything that can be refused without touching the database ──
	//
	// The stage vocabulary is checked BEFORE the company is resolved, and
	// the order is load-bearing: FindOrCreate writes. An item that was going
	// to fail on an unknown stage would otherwise leave a company row behind
	// for an opportunity that was never created — a partial write from an
	// operation that reported failure, which is exactly the kind of residue
	// nobody goes looking for.
	if err := validateHistoryStages(in.History); err != nil {
		return nil, err
	}

	company, err := s.companies.FindOrCreate(ctx, workspaceID, in.CompanyName, in.CompanyDomain)
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
		Import:       in.Identity,
	}
	if in.CreatedAt != nil {
		o.CreatedAt = in.CreatedAt.UTC()
	}
	if in.UpdatedAt != nil {
		o.UpdatedAt = in.UpdatedAt.UTC()
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

	timeline, err := s.importTimeline(o, in.History)
	if err != nil {
		return nil, err
	}

	if err := s.opportunities.ImportCreate(ctx, o, timeline); err != nil {
		return nil, err
	}
	return &ImportResult{Opportunity: o}, nil
}

// validateHistoryStages rejects a history this domain cannot express,
// before anything is written.
//
// It is deliberately only the vocabulary check. The rest of the timeline's
// rules — the shape of the transitions, and whether it agrees with the
// record's current stage — need the entity, which needs the company, which
// is a write. Splitting the cheap half out is what keeps a doomed item from
// leaving a row behind.
func validateHistoryStages(visits []domain.StageVisit) error {
	_, err := domain.BuildStageTimeline(visits, "")
	if err == nil {
		return nil
	}
	// The final "does it agree with the current stage" check cannot pass
	// here — there is no current stage yet — so its complaint is not a
	// verdict at this point and is left for importTimeline to reach with the
	// real value.
	if isTimelineDisagreement(err) {
		return nil
	}
	return err
}

// isTimelineDisagreement tells the end-of-history mismatch apart from a
// genuine vocabulary failure, so the pre-check does not reject on a
// question it was not asked to answer.
func isTimelineDisagreement(err error) bool {
	var de *domain.Error
	if !errorsAs(err, &de) {
		return false
	}
	return strings.Contains(de.Message, "current stage is")
}

func errorsAs(err error, target **domain.Error) bool {
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

// importTimeline decides what history this record arrives with.
//
// Three cases, and the middle one is the whole improvement:
//
//	no tracking          → no events. A record in Discover never entered the
//	                       pipeline, so there is nothing to record.
//	history supplied     → the real transitions, rebuilt by the domain. An
//	                       unknown stage fails this ITEM, loudly.
//	tracking, no history → one event for the stage it is in, stamped with
//	                       the moment it was entered. This is what the old
//	                       importer did for EVERY record, and it remains
//	                       correct for a document that genuinely has no
//	                       history to give.
func (s *Service) importTimeline(o *domain.Opportunity, visits []domain.StageVisit) ([]domain.StageEvent, error) {
	stage, tracked := o.Stage()
	if !tracked {
		if len(visits) > 0 {
			return nil, domain.Invalid(
				"this record has stage history but is not in the pipeline; " +
					"one of the two is wrong and this import will not guess which")
		}
		return nil, nil
	}

	if len(visits) > 0 {
		return domain.BuildStageTimeline(visits, stage)
	}

	return []domain.StageEvent{{
		// Nil `from`: entering the pipeline comes from Discover, and naming
		// the stage as its own predecessor would invent a visit.
		ToStage:    stage,
		OccurredAt: o.Tracking.StageEnteredAt,
	}}, nil
}
