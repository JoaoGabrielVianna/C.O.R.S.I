// Package app holds the release-history use cases.
//
// The service is thin because the interesting rules live where they cannot
// be bypassed: SemVer parsing and the publish transition are in the
// domain, and immutability is additionally enforced by a database trigger.
// What this layer owns is the one derivation the product depends on —
// which release is *current* — and the guarantee that it is computed from
// published releases only.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/corsi/backend/internal/releases/domain"
	"github.com/corsi/backend/internal/releases/ports"
)

type Service struct {
	repo ports.ReleaseRepo
	log  *slog.Logger
	// now is injectable so tests can publish at a chosen instant without
	// sleeping or asserting on wall-clock drift.
	now func() time.Time
}

func NewService(repo ports.ReleaseRepo, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log, now: time.Now}
}

// WithClock returns a copy driven by the given clock. Used by tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	c := *s
	c.now = now
	return &c
}

// ModuleSummary is a module plus the two derived facts the overview card
// needs. Both are computed here, and neither is stored: a `current_version`
// column would be a second copy of something the timeline already says.
type ModuleSummary struct {
	*domain.Module
	Current      *domain.Release `json:"current_release"`
	ReleaseCount int             `json:"release_count"`
	// LastReleasedAt is the date of the newest published release, which is
	// not always the current one's: a patch to an older line published
	// later is more recent in time while ranking lower in version.
	LastReleasedAt *time.Time `json:"last_released_at"`
}

// ListModules builds every card in one pass. It reads all releases once
// rather than querying per module: the whole history of this product is a
// few dozen rows, and N+1 queries for a page that renders a handful of
// cards would be cost with no benefit.
func (s *Service) ListModules(ctx context.Context) ([]*ModuleSummary, error) {
	modules, err := s.repo.ListModules(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.repo.ListAllReleases(ctx)
	if err != nil {
		return nil, err
	}

	byModule := make(map[string][]*domain.Release, len(modules))
	for _, r := range all {
		byModule[r.ModuleKey] = append(byModule[r.ModuleKey], r)
	}

	out := make([]*ModuleSummary, 0, len(modules))
	for _, m := range modules {
		rs := byModule[m.Key]
		out = append(out, &ModuleSummary{
			Module:         m,
			Current:        domain.CurrentOf(rs),
			ReleaseCount:   publishedCount(rs),
			LastReleasedAt: lastReleasedAt(rs),
		})
	}
	return out, nil
}

// ModuleDetail is a module with its full timeline.
type ModuleDetail struct {
	*ModuleSummary
	// Releases is the timeline, newest version first. Drafts are included
	// and carry `status: "draft"`, because the owner needs to see that a
	// version has been recorded but not declared. Nothing downstream may
	// treat a draft as current: that is CurrentOf's job, above.
	Releases []*domain.Release `json:"releases"`
}

func (s *Service) GetModule(ctx context.Context, key string) (*ModuleDetail, error) {
	m, err := s.repo.GetModule(ctx, key)
	if err != nil {
		return nil, err
	}
	rs, err := s.repo.ListReleases(ctx, key)
	if err != nil {
		return nil, err
	}
	return &ModuleDetail{
		ModuleSummary: &ModuleSummary{
			Module:         m,
			Current:        domain.CurrentOf(rs),
			ReleaseCount:   publishedCount(rs),
			LastReleasedAt: lastReleasedAt(rs),
		},
		Releases: rs,
	}, nil
}

// GetRelease returns one recorded snapshot, exactly as it was stored.
//
// Nothing about the current state of the module reaches this value. That
// is the whole promise of the feature: reading Agents v1.0.0 in a year
// returns what shipped in v1.0.0.
func (s *Service) GetRelease(ctx context.Context, moduleKey, version string) (*domain.Release, error) {
	v, err := domain.ParseVersion(version)
	if err != nil {
		return nil, err
	}
	return s.repo.GetRelease(ctx, moduleKey, v.String())
}

// RecordInput is a new draft. Every list is optional; a release with no
// capabilities recorded is legal and says exactly that.
type RecordInput struct {
	ModuleKey string
	Version   string
	Summary   string
	// Stability is optional. Empty means the domain default, which is
	// `stable`: an unqualified release is an ordinary one, and beta or rc
	// is a claim someone makes deliberately.
	Stability      domain.Stability
	Capabilities   []domain.Capability
	Evidence       []domain.Metric
	Limitations    []domain.Note
	Decisions      []domain.Note
	TechnicalNotes []domain.Note
	DocRefs        []domain.DocRef
}

// Record creates a draft release. It never publishes: recording what a
// version contains and declaring it shipped are different acts, and
// collapsing them would remove the only moment at which the snapshot can
// be reviewed before it freezes.
func (s *Service) Record(ctx context.Context, in RecordInput) (*domain.Release, error) {
	if _, err := s.repo.GetModule(ctx, in.ModuleKey); err != nil {
		return nil, err
	}
	rel, err := domain.NewRelease(in.ModuleKey, in.Version, in.Summary)
	if err != nil {
		return nil, err
	}
	if in.Stability != "" {
		if !in.Stability.Valid() {
			return nil, domain.Invalid("stability must be one of stable, beta, rc")
		}
		rel.Stability = in.Stability
	}
	if in.Capabilities != nil {
		rel.Capabilities = in.Capabilities
	}
	if in.Evidence != nil {
		rel.Evidence = in.Evidence
	}
	if in.Limitations != nil {
		rel.Limitations = in.Limitations
	}
	if in.Decisions != nil {
		rel.Decisions = in.Decisions
	}
	if in.TechnicalNotes != nil {
		rel.TechnicalNotes = in.TechnicalNotes
	}
	if in.DocRefs != nil {
		rel.DocRefs = in.DocRefs
	}
	if err := s.repo.CreateRelease(ctx, rel); err != nil {
		return nil, err
	}
	s.log.Info("release recorded", "module", rel.ModuleKey, "version", rel.Version, "status", rel.Status)
	return rel, nil
}

// Publish declares a recorded draft. After this returns, the row is frozen
// by the database and this service offers no way to change it.
func (s *Service) Publish(ctx context.Context, moduleKey, version string) (*domain.Release, error) {
	v, err := domain.ParseVersion(version)
	if err != nil {
		return nil, err
	}
	rel, err := s.repo.GetRelease(ctx, moduleKey, v.String())
	if err != nil {
		return nil, err
	}
	if err := rel.Publish(s.now()); err != nil {
		return nil, err
	}
	if err := s.repo.PublishRelease(ctx, rel); err != nil {
		return nil, err
	}
	s.log.Info("release published", "module", rel.ModuleKey, "version", rel.Version, "released_at", rel.ReleasedAt)
	return rel, nil
}

func publishedCount(rs []*domain.Release) int {
	n := 0
	for _, r := range rs {
		if r.IsPublished() {
			n++
		}
	}
	return n
}

func lastReleasedAt(rs []*domain.Release) *time.Time {
	var latest *time.Time
	for _, r := range rs {
		if !r.IsPublished() || r.ReleasedAt == nil {
			continue
		}
		if latest == nil || r.ReleasedAt.After(*latest) {
			latest = r.ReleasedAt
		}
	}
	return latest
}
