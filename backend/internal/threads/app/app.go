// Package app is the Threads application layer: the use cases every writer
// shares.
//
// ── Why this layer is the one the tools call ───────────────────────────
// There is one writer today — an agent's tool — and there will be more: a
// screen when one is built, a Telegram channel, an importer. They must not
// each re-derive what "update a thread" means. The rules live below this
// line exactly once: the entity is loaded, the domain applies the change
// and reports what moved, the entity is validated, the repository persists
// it. A tool that reached the repository directly would skip the middle
// two; a tool that reached the database directly would skip all four.
// Neither is possible from outside this package, because nothing outside it
// holds a repository.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/threads/domain"
	"github.com/corsi/backend/internal/threads/ports"
)

type Service struct {
	threads ports.ThreadRepo
	log     *slog.Logger
	// now is the clock, injectable so a test can assert on a timestamp
	// instead of asserting that one exists.
	now func() time.Time
}

func NewService(t ports.ThreadRepo, log *slog.Logger) *Service {
	return &Service{threads: t, log: log, now: time.Now}
}

// WithClock returns a copy that reads time from fn. Used by tests.
func (s *Service) WithClock(fn func() time.Time) *Service {
	cp := *s
	cp.now = fn
	return &cp
}

/* ── reads ───────────────────────────────────────────────────────────── */

// ListThreads returns matching threads and the unbounded total.
//
// The total is returned alongside because every caller needs to know
// whether it is looking at everything: a model that received a truncated
// list and believed it was complete would answer "you have four drafts"
// with confidence.
func (s *Service) ListThreads(ctx context.Context, workspaceID uuid.UUID, f ports.ThreadFilter) ([]*domain.Thread, int64, error) {
	items, err := s.threads.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.threads.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetThread(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Thread, error) {
	return s.threads.FindByID(ctx, workspaceID, id)
}

/* ── writes ──────────────────────────────────────────────────────────── */

// CreateInput is one new thread, as any caller states it.
//
// ── Why the status is optional and defaults to idea ────────────────────
// Because the canonical first sentence is "guarda essa ideia", and a caller
// that has to name a state in order to save a thought is a caller that will
// name the wrong one. A create that says nothing lands in `idea`, which is
// where a thought that has not been written yet belongs.
type CreateInput struct {
	Title   string
	Content string
	// Status optionally creates the thread already further along — the user
	// pasting a finished post they are about to publish. Nil means idea.
	Status *domain.Status
}

func (s *Service) CreateThread(ctx context.Context, workspaceID uuid.UUID, in CreateInput) (*domain.Thread, error) {
	status := domain.StatusIdea
	if in.Status != nil {
		status = *in.Status
	}

	t := &domain.Thread{
		WorkspaceID: workspaceID,
		Title:       domain.NormalizeTitle(in.Title),
		Content:     in.Content,
		Status:      status,
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateResult is a completed edit: the thread as it now is, plus what
// actually moved.
type UpdateResult struct {
	Thread *domain.Thread
	domain.ChangeResult
}

// UpdateThread applies a partial change to one thread.
//
// ── Why it reads before it writes ──────────────────────────────────────
// Three reasons, and all three are load-bearing. The domain needs the
// current values to report what CHANGED rather than what was sent. An
// update naming a field it is not touching must leave the stored value
// alone, which means the stored value has to be in hand. And a thread this
// workspace cannot see must fail here — as a not-found indistinguishable
// from a fabricated id — rather than as an UPDATE that silently affects
// zero rows and reports success.
//
// ── Why an empty change is refused ─────────────────────────────────────
// Because it is always a mistake at the point it is made: a caller that
// meant to change nothing had no reason to call, and a model that produced
// it misunderstood the contract. Accepting it would move `updated_at`,
// which reorders every listing — an edit that did nothing, announcing
// itself as the most recent work.
func (s *Service) UpdateThread(ctx context.Context, workspaceID, id uuid.UUID, c domain.Change) (*UpdateResult, error) {
	if c.Empty() {
		return nil, domain.Invalid("an update must change at least one of title, content or status")
	}

	t, err := s.threads.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	changed := t.Apply(c)
	if err := t.Validate(); err != nil {
		return nil, err
	}

	// A change that moved nothing is not written. The row is already in the
	// requested state, and touching `updated_at` would claim activity that
	// did not happen. The caller is told, and says so.
	if changed.Unchanged() {
		return &UpdateResult{Thread: t, ChangeResult: changed}, nil
	}

	if err := s.threads.Update(ctx, t); err != nil {
		return nil, err
	}
	return &UpdateResult{Thread: t, ChangeResult: changed}, nil
}

// DeleteThread soft-deletes one thread. There is no second removal
// semantics: this is the operation any surface calls.
func (s *Service) DeleteThread(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.threads.SoftDelete(ctx, workspaceID, id)
}
