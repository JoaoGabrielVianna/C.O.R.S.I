// Package repo is the Postgres implementation of the Palace storage
// ports.
//
// ── Every query is workspace-first ─────────────────────────────────────
// The predicate that guarantees isolation is in the SQL, in `$1`, on
// every statement that reads or writes. Not in a check the caller is
// trusted to have run, not in a filter over the result. See the package
// note in ports.
//
// ── The workspace argument is the authority ────────────────────────────
// Create and Update take a workspace AND an entity that knows its own.
// The argument wins and the disagreement is refused, which is what turns
// "a service saved an entity it loaded for somebody else" from a silent
// cross-workspace write into a loud failure. See assertWorkspace.
//
// ── Nothing here formats an entity ─────────────────────────────────────
// No log line in this package takes a Room or a Memory, and no error
// message quotes a field. The entities redact themselves if somebody
// tries (see domain/redaction.go), and this package does not test that
// safety net by leaning on it: the rule is that content does not enter a
// message at all. Database errors are sanitised for the same reason and a
// sharper one, which errors.go states.
package repo

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/ports"
)

// Repos bundles the implementations so the composition root wires one
// value.
//
// Eight repositories and a clock: every entity the Palace Core v1 schema
// carries.
type Repos struct {
	Rooms      ports.RoomRepo
	Memories   ports.MemoryRepo
	Artifacts  ports.ArtifactRepo
	Items      ports.ItemRepo
	Sources    ports.SourceRepo
	Provenance ports.ProvenanceRepo
	Relations  ports.RelationRepo
	Sessions   ports.SessionRepo
	Clock      ports.Clock
}

func New(pool *pgxpool.Pool) *Repos {
	return &Repos{
		Rooms:      &RoomRepo{pool: pool},
		Memories:   &MemoryRepo{pool: pool},
		Artifacts:  &ArtifactRepo{pool: pool},
		Items:      &ItemRepo{pool: pool},
		Sources:    &SourceRepo{pool: pool},
		Provenance: &ProvenanceRepo{pool: pool},
		Relations:  &RelationRepo{pool: pool},
		Sessions:   &SessionRepo{pool: pool},
		Clock:      &ClockRepo{pool: pool},
	}
}

/* ── listing bounds ──────────────────────────────────────────────────── */

const (
	// defaultLimit bounds a listing that did not ask for one.
	//
	// Twenty-five, matching Threads rather than Job Radar's fifty, and for
	// the same reason: every row a listing returns becomes prompt tokens
	// on the next provider call of the same turn, and a Palace row carries
	// text.
	defaultLimit = 25
	// maxLimit is the ceiling a caller cannot argue past.
	maxLimit = 100
)

// bound lowers a requested limit into range. A caller asking for more
// than the ceiling gets the ceiling rather than a refusal: it wants as
// much as it can have, and an error would teach it nothing it can act on.
func bound(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

/* ── the workspace guard ─────────────────────────────────────────────── */

// assertWorkspace refuses a write whose entity disagrees with the call.
//
// ── Why this is checked rather than reconciled ─────────────────────────
// Stamping the argument onto the entity would make the mismatch
// disappear, and with it the evidence that a service loaded a row for one
// workspace and saved it while serving another. That bug writes somebody
// else's private memory into a stranger's palace, and it is invisible in
// review because both lines look correct on their own.
//
// The message carries no ids. Which two workspaces were confused is a
// fact about two operators, and it belongs in neither an audit trail nor
// a log line that this package cannot see the destination of.
func assertWorkspace(op string, arg, entity uuid.UUID) error {
	if arg == uuid.Nil {
		return fmt.Errorf("palace: %s: no workspace was supplied", op)
	}
	if entity != uuid.Nil && entity != arg {
		return fmt.Errorf("palace: %s: the entity belongs to a different workspace than the call", op)
	}
	return nil
}
