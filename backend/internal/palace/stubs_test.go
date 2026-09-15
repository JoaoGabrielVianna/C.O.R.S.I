package palace

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// Stub repositories for the constructor tests.
//
// ── Why they embed the interface instead of implementing it ────────────
// Because they exist to be PRESENT, not to be called. Embedding a nil
// interface satisfies the contract and panics on any method, which is the
// right failure for a double that a test was never supposed to exercise:
// a stub that silently returned zero values could make a future test pass
// while touching nothing.
//
// They are also struct VALUES rather than pointers, deliberately. That is
// what found the bug in the dependency check: reflect.Value.IsNil panics
// on a kind that cannot be nil, so a validator written without a kind
// switch would have rejected, loudly and wrongly, a correctly wired
// service.
type stubRooms struct{ ports.RoomRepo }
type stubMemories struct{ ports.MemoryRepo }
type stubArtifacts struct{ ports.ArtifactRepo }
type stubItems struct{ ports.ItemRepo }
type stubSources struct{ ports.SourceRepo }
type stubProvenance struct{ ports.ProvenanceRepo }
type stubRelations struct{ ports.RelationRepo }
type stubSessions struct{ ports.SessionRepo }

// stubClock is implemented rather than embedded: the constructor tests do
// not call it, and a clock is one method, so writing it costs less than
// explaining why it panics.
type stubClock struct{}

func (stubClock) Now(context.Context) (time.Time, error) { return time.Time{}, nil }

// Referenced so the domain import is honest: the stubs speak in its
// types through the embedded interfaces.
var _ = domain.LifecycleActive
var _ = uuid.Nil
