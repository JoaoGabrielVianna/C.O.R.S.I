package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
)

// Sources: evidence.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THERE IS NO UpdateSource IN THIS FILE, OR ANYWHERE ABOVE IT
//
// ══════════════════════════════════════════════════════════════════════
//
// Not a narrow one, not a guarded one, not one that only touches the
// sensitivity. A source answers "what was actually said or seen", and
// evidence that can be rewritten after the fact answers nothing: the
// moment a transcript can be corrected in place, no memory resting on it
// can be audited, because the thing it rested on is gone.
//
// The absence is layered, so no single edit restores it: the domain has
// no Change type for a Source, the port declares no Update, and the
// repository contains no UPDATE statement. Three files would have to
// change together, which is what turns "immutable" from a habit into a
// property.
//
// A corrected transcript is a NEW source. The memory can cite both, and
// the disagreement between them is itself information.

// CreateSourceInput is one new piece of evidence.
type CreateSourceInput struct {
	Kind    domain.SourceKind
	Content string
	// ExternalRef says where it came from when that is outside this
	// product. Required by the domain for SourceExternal: evidence about
	// a system we do not own that cannot say WHICH system is not
	// provenance.
	ExternalRef string
	// CapturedAt is when the evidence was PRODUCED, which is not when the
	// row is written. A transcript of yesterday's voice note is captured
	// today.
	//
	// Nil means now, read from the database clock rather than from this
	// process: one authority, for the reason ports.Clock states. A caller
	// that knows when the thing happened should say so, and the two being
	// separate fields is what stops everything imported landing in the
	// present.
	CapturedAt *time.Time
	// Sensitivity is optional. Nil means DefaultSensitivity.
	//
	// ── Worth stating, because it has consequences elsewhere ───────────
	// This value becomes a FLOOR. Every memory that cites this source
	// must be at least this withheld, permanently, because provenance is
	// append-only and there is no operation that relabels a source. A
	// transcript filed as `highly_sensitive` can never back an ordinary
	// memory.
	Sensitivity *domain.Sensitivity
}

func (s *Service) CreateSource(ctx context.Context, workspaceID uuid.UUID, in CreateSourceInput) (*domain.Source, error) {
	sensitivity := domain.DefaultSensitivity
	if in.Sensitivity != nil {
		sensitivity = *in.Sensitivity
	}

	captured := in.CapturedAt
	if captured == nil {
		// The database's clock, never this process's. Finance states the
		// argument in full on ports.Clock and Palace inherits it: two
		// clocks only have to disagree by a fraction of a second for a
		// record written a moment ago to sort into the future.
		now, err := s.clock.Now(ctx)
		if err != nil {
			return nil, err
		}
		captured = &now
	}

	src := &domain.Source{
		WorkspaceID: workspaceID,
		Kind:        in.Kind,
		Content:     in.Content,
		ExternalRef: in.ExternalRef,
		CapturedAt:  *captured,
		Sensitivity: sensitivity,
	}
	if err := src.Validate(); err != nil {
		return nil, err
	}
	if err := s.sources.Create(ctx, workspaceID, src); err != nil {
		return nil, err
	}
	return src, nil
}

func (s *Service) GetSource(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Source, error) {
	return s.sources.FindByID(ctx, workspaceID, id)
}
