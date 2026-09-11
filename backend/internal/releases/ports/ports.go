// Package ports declares the driven side of the releases context.
//
// The interface is read-heavy and write-thin on purpose: this version
// records history and shows it. There is no update, and there is no
// delete — not because they were forgotten, but because the only two
// mutations the product has are "record a draft" and "publish it".
package ports

import (
	"context"

	"github.com/corsi/backend/internal/releases/domain"
)

type ReleaseRepo interface {
	// ListModules returns every module in display order.
	ListModules(ctx context.Context) ([]*domain.Module, error)

	// GetModule returns one module by its key.
	GetModule(ctx context.Context, key string) (*domain.Module, error)

	// ListReleases returns a module's releases, newest version first.
	// Drafts are included: the caller decides what to show, and the
	// service is the one place that knows a draft is never current.
	ListReleases(ctx context.Context, moduleKey string) ([]*domain.Release, error)

	// ListAllReleases returns every release across every module, so the
	// module list can be built with one query instead of one per module.
	ListAllReleases(ctx context.Context) ([]*domain.Release, error)

	// GetRelease returns one release by module and version string.
	GetRelease(ctx context.Context, moduleKey, version string) (*domain.Release, error)

	// CreateRelease persists a new draft.
	CreateRelease(ctx context.Context, r *domain.Release) error

	// PublishRelease writes the published state of a release that the
	// caller has already transitioned in memory.
	//
	// It takes the whole entity rather than an id and a timestamp so the
	// row written is the one the domain produced, and the freeze trigger
	// sees a single UPDATE whose OLD row is still a draft.
	PublishRelease(ctx context.Context, r *domain.Release) error
}
