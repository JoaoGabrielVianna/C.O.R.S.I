// Package app is the Palace application layer: the use cases every
// caller shares.
//
// ── Why this layer is the one anything above must call ─────────────────
// There is no caller today and there will be several: a tool, a screen if
// one is ever built, a channel that is not a browser. They must not each
// re-derive what "create a memory" means. The rules live below this line
// exactly once: defaults are applied, references are RESOLVED IN THE
// CALLER'S WORKSPACE, the domain validates, the repository persists. A
// caller that reached the repository directly would skip the first three;
// one that reached the database directly would skip all four. Neither is
// possible from outside this package, because nothing outside it holds a
// repository.
//
// ── The rule that matters most here ────────────────────────────────────
// Every reference between Palace entities is resolved against the
// caller's workspace BEFORE the write. A memory that names a room is not
// written until this layer has confirmed that the room is one this
// workspace has. The composite foreign key would refuse the row anyway,
// which is the backstop; this is the part that turns a foreign id into
// `not_found` instead of a constraint violation, and it is what makes the
// answer identical to the one a fabricated id gets.
//
// ── Nothing here logs an entity ────────────────────────────────────────
// No log line in this package takes a Room or a Memory, and no error
// quotes a field. The entities redact themselves if somebody tries (see
// domain/redaction.go), and this package does not lean on that net: the
// rule is that content never enters a message.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"time"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// Deps is what the Palace application layer is built from.
//
// ── Why a struct and not positional arguments ──────────────────────────
// Because there are nine collaborators and they are all interfaces over
// the same kind of thing. Positionally, that is nine chances to swap two
// repositories that satisfy different interfaces but read identically at
// the call site, and the compiler catches only some of those. It is also
// the shape every module in this codebase already uses for its own
// construction.
type Deps struct {
	Rooms      ports.RoomRepo
	Memories   ports.MemoryRepo
	Artifacts  ports.ArtifactRepo
	Items      ports.ItemRepo
	Sources    ports.SourceRepo
	Provenance ports.ProvenanceRepo
	Relations  ports.RelationRepo
	Sessions   ports.SessionRepo
	Clock      ports.Clock
	Logger     *slog.Logger
}

// Service is the Palace application layer. It holds one repository per
// entity the Palace Core v1 schema carries, plus the clock.
type Service struct {
	rooms      ports.RoomRepo
	memories   ports.MemoryRepo
	artifacts  ports.ArtifactRepo
	items      ports.ItemRepo
	sources    ports.SourceRepo
	provenance ports.ProvenanceRepo
	relations  ports.RelationRepo
	sessions   ports.SessionRepo
	clock      ports.Clock
	log        *slog.Logger
}

// NewService builds the application layer, or refuses.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A SERVICE THAT EXISTS IS A SERVICE THAT WORKS
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The failure this closes ────────────────────────────────────────────
// With nine collaborators, a composition root that forgets one used to
// get a Service that constructed cleanly and then panicked on a nil
// pointer at the first call that needed the missing repository. That is
// not hypothetical: it happened while this context was being built, and
// the symptom was a nil dereference in a session test, several layers
// away from the line that actually forgot something.
//
// ── Why the check is here and not spread across the methods ────────────
// Because a nil guard in every method is the same mistake repeated
// thirty times: it turns one wiring bug into thirty runtime refusals,
// each of which reads like a product rule ("sessions are unavailable")
// rather than like what it is. Construction is the only moment at which
// the answer is simple and total.
//
// ── Why an error and not a panic ───────────────────────────────────────
// The same shape the tool registry already uses: New returns an error so
// the caller can report the problem properly, and MustNewService panics
// for a composition root where there is nothing useful to do but stop.
// A duplicate tool name and a missing repository are the same kind of
// fault, and they should fail the same way.
func NewService(d Deps) (*Service, error) {
	// Named in the order they were added, so a failure reads as a
	// checklist rather than a puzzle. The logger is deliberately absent:
	// a Service with no logger logs nothing, which is a degraded service
	// and not a broken one.
	for _, required := range []struct {
		name string
		got  any
	}{
		{"Rooms", d.Rooms},
		{"Memories", d.Memories},
		{"Artifacts", d.Artifacts},
		{"Items", d.Items},
		{"Sources", d.Sources},
		{"Provenance", d.Provenance},
		{"Relations", d.Relations},
		{"Sessions", d.Sessions},
		{"Clock", d.Clock},
	} {
		if isMissing(required.got) {
			return nil, fmt.Errorf("palace: %s is required", required.name)
		}
	}

	return &Service{
		rooms:      d.Rooms,
		memories:   d.Memories,
		artifacts:  d.Artifacts,
		items:      d.Items,
		sources:    d.Sources,
		provenance: d.Provenance,
		relations:  d.Relations,
		sessions:   d.Sessions,
		clock:      d.Clock,
		log:        logger(d.Logger),
	}, nil
}

// isMissing reports whether a dependency was not supplied.
//
// ── Why this is not just `== nil` ──────────────────────────────────────
// An interface holding a nil pointer is NOT a nil interface. A caller
// that wired `var r *repo.RoomRepo` and never assigned it produces a
// non-nil `ports.RoomRepo` whose every call panics, which is exactly the
// bug this validation exists to catch and exactly the one a plain nil
// check misses.
//
// ── Why the kind is checked before IsNil ───────────────────────────────
// Because reflect.Value.IsNil PANICS on a kind that cannot be nil, and a
// repository implemented as a struct value rather than a pointer is
// perfectly legal: a test double is usually one. A validator that
// panicked on a correctly wired service would be worse than no validator,
// and this was found by exactly that case.
func isMissing(dep any) bool {
	if dep == nil {
		return true
	}
	v := reflect.ValueOf(dep)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		// A struct, or anything else that cannot be nil, was supplied. It
		// is present by construction.
		return false
	}
}

// MustNewService is NewService for a composition root, where an
// incomplete wiring is fatal anyway and there is nothing useful to do but
// stop. Same arrangement as the tool registry's MustNew.
func MustNewService(d Deps) *Service {
	s, err := NewService(d)
	if err != nil {
		panic(err)
	}
	return s
}

// logger returns a logger that discards, when none was supplied.
//
// ── Why the logger is optional when everything else is required ────────
// Because a missing repository makes an operation impossible and a
// missing logger makes it quiet. Refusing to construct over the second
// would be treating a preference as a dependency; panicking on it later
// would be worse. This is the one field where a default is the honest
// answer.
func logger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Now is the one authority on the current instant for anything above this
// layer.
//
// ── Why a caller should ask instead of reading its own clock ───────────
// Because the alternative is two clocks, and the failure they produce is
// silent: a moment stamped from an API process whose clock runs slightly
// ahead of the database sorts into the future of every row written beside
// it. Finance states the argument in full on ports.Clock and Palace
// inherits it rather than re-deriving it.
//
// One caller so far: CreateSource, when the caller did not say when the
// evidence was captured. The timestamps Room, Memory and Artifact carry
// are stamped by the SQL that writes them, which is the same clock and
// strictly better than passing one in. It stays exported so the surfaces
// that DO stamp a moment, a tool resolving "hoje" or a session recording
// activity, find one authority already here rather than reaching for
// time.Now on the day they arrive.
func (s *Service) Now(ctx context.Context) (time.Time, error) {
	return s.clock.Now(ctx)
}

// isNotFound reports whether err is this context's not-found.
//
// ── Why the application layer needs to ask ─────────────────────────────
// Because one use case turns absence into an ordinary result rather than
// an error: closing when nothing is open is a request that has already
// been satisfied, not a failure. Everywhere else a not-found travels
// straight up, and reading it with errors.As at that one call site would
// put the shape of domain.Error in a file about sessions.
func isNotFound(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.KindNotFound
}
