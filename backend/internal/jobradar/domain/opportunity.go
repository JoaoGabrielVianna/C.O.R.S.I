// Package domain is the Job Radar bounded context: opportunities, the
// companies behind them, and the pipeline they move through.
//
// ── Why this package exists ────────────────────────────────────────────
// Because the pipeline was previously a React hook. Stage names, the
// Discover/Pipeline split and the "entering a stage stamps a timestamp"
// rule were all real rules, but they were enforced by whichever component
// happened to call them. Moving them here makes them properties of the
// system rather than of the caller — which is the precondition for letting
// anything other than the UI (a tool, an importer, a future collector)
// write to the pipeline without each writer re-deriving the rules.
//
// This package imports nothing from the platform and nothing from any other
// module. It is plain data and the rules over it.
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

/* ── the stage vocabulary ────────────────────────────────────────────── */

// PipelineStage is where an opportunity sits once it is being tracked.
//
// ── This list is canonical, and it is the only copy that validates ─────
// The same six names appear in the CHECK constraint of
// migrations/jobradar/0001_init.up.sql and in the frontend's types.ts. That
// is not three sources of truth: the database constraint is a backstop that
// refuses a row this package would never build, and the frontend list is a
// rendering order for a board. Every WRITE path — HTTP, tool, importer —
// validates here and nowhere else. In particular the tools package must not
// declare its own list; it calls ParseStage.
type PipelineStage string

const (
	StageSaved     PipelineStage = "saved"
	StageApplied   PipelineStage = "applied"
	StageInterview PipelineStage = "interview"
	StageTechnical PipelineStage = "technical"
	StageOffer     PipelineStage = "offer"
	StageRejected  PipelineStage = "rejected"
)

// PipelineStages is the board's left-to-right order.
//
// Order is presentation, not permission: see ParseStage's note on why no
// transition is forbidden.
var PipelineStages = []PipelineStage{
	StageSaved,
	StageApplied,
	StageInterview,
	StageTechnical,
	StageOffer,
	StageRejected,
}

func (s PipelineStage) Valid() bool {
	switch s {
	case StageSaved, StageApplied, StageInterview, StageTechnical, StageOffer, StageRejected:
		return true
	}
	return false
}

func (s PipelineStage) String() string { return string(s) }

// StageNames is the vocabulary as plain strings, for a schema description
// or an error message. Built from the same slice so a stage added later
// cannot be added to the enum and forgotten in the model's instructions.
func StageNames() []string {
	out := make([]string, len(PipelineStages))
	for i, s := range PipelineStages {
		out[i] = string(s)
	}
	return out
}

// ParseStage turns caller input into a stage, or explains what was wrong.
//
// ── Why it is lenient about case and space and strict about nothing else ─
// The callers are a person typing into a form and a model producing an
// argument. Both will send "Applied" or " applied ", and refusing those
// would be pedantry that teaches the model nothing. What it will NOT do is
// guess: "apply" is not "applied", and an approximate match here would move
// a real opportunity to a stage nobody asked for.
//
// ── Why every transition is allowed ────────────────────────────────────
// There is no state machine, deliberately. A real job search goes backwards
// (an offer falls through, a rejection is reversed) and skips forwards (a
// referral lands straight at interview). Encoding an order would mean the
// system refusing the truth because it disagreed with a diagram. The
// history in stage_events records what actually happened, which is the
// honest form of this information.
func ParseStage(raw string) (PipelineStage, error) {
	s := PipelineStage(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", Invalid("unknown stage %q; the pipeline stages are %s",
			raw, strings.Join(StageNames(), ", "))
	}
	return s, nil
}

/* ── company ─────────────────────────────────────────────────────────── */

type Company struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"-"`
	Name        string    `json:"name"`
	Domain      string    `json:"domain,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const maxCompanyName = 200

// NormalizeCompanyName trims a name and refuses an empty one. The lookup
// that dedupes companies is case-insensitive (see the UNIQUE index), so
// this does not lowercase: the stored casing is what the user typed, and
// "Stripe" should not become "stripe" on screen.
func NormalizeCompanyName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", Invalid("a company name is required")
	}
	if len([]rune(name)) > maxCompanyName {
		return "", Invalid("company name is longer than %d characters", maxCompanyName)
	}
	return name, nil
}

/* ── tracking ────────────────────────────────────────────────────────── */

// Tracking is the pipeline half of an opportunity. Nil means Discover.
type Tracking struct {
	Stage PipelineStage `json:"stage"`
	// TrackedAt is the first time this record ever entered the pipeline. It
	// survives every later move — that is what makes "in the pipeline for
	// three weeks" different from "in this stage for two days".
	TrackedAt time.Time `json:"tracked_at"`
	// StageEnteredAt is when the CURRENT stage was entered.
	StageEnteredAt time.Time `json:"stage_entered_at"`
	NextAction     string    `json:"next_action"`
}

// StageEvent is one transition, as stored.
type StageEvent struct {
	ID uuid.UUID `json:"id"`
	// FromStage is nil for the first entry into the pipeline.
	FromStage  *PipelineStage `json:"from_stage"`
	ToStage    PipelineStage  `json:"to_stage"`
	OccurredAt time.Time      `json:"occurred_at"`
}

/* ── opportunity ─────────────────────────────────────────────────────── */

type Notes struct {
	General   string `json:"general"`
	Interview string `json:"interview"`
	Technical string `json:"technical"`
	Personal  string `json:"personal"`
}

type Opportunity struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"-"`
	CompanyID   uuid.UUID `json:"company_id"`
	// CompanyName is joined in on read. It is not a column of this entity;
	// it is here because every surface that shows an opportunity shows the
	// employer, and a list that made the caller resolve ids itself would be
	// a list nobody could read.
	CompanyName string `json:"company_name"`

	Role        string   `json:"role"`
	Salary      string   `json:"salary"`
	Location    string   `json:"location"`
	Stack       []string `json:"stack"`
	Description string   `json:"description"`

	Source       string `json:"source"`
	SourceURL    string `json:"source_url,omitempty"`
	MatchPercent *int   `json:"match_percent"`

	PostedAt time.Time `json:"posted_at"`

	// Tracking is nil when the record lives in Discover.
	Tracking *Tracking `json:"tracking"`
	Notes    Notes     `json:"notes"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Import records which legacy record this row IS, for rows that came
	// from a migrated document. Zero for everything created normally.
	//
	// ── Why it is on the entity and not in a side table ────────────────
	// Because it is an attribute of this opportunity's identity, not a
	// relationship to something else, and the uniqueness that makes a
	// re-import safe has to be a constraint on THIS row. A join table would
	// put the guarantee one hop away from the thing it guarantees.
	//
	// It is deliberately never used as a lookup key by anything except the
	// importer. Every other read addresses an opportunity by its uuid.
	Import ImportIdentity `json:"-"`
}

// Stage reports the current stage, and whether there is one at all. It
// exists so callers stop writing `if o.Tracking != nil` before every read
// of a stage — the nil check is the lifecycle, and it belongs in one place.
func (o *Opportunity) Stage() (PipelineStage, bool) {
	if o.Tracking == nil {
		return "", false
	}
	return o.Tracking.Stage, true
}

const (
	maxRole        = 200
	maxSalary      = 120
	maxLocation    = 200
	maxDescription = 20000
	maxSourceURL   = 1000
	maxNextAction  = 500
	maxNote        = 20000
	// MaxStackEntries bounds the tag list. Twenty is far past any honest
	// posting and short enough that a malformed import cannot turn one row
	// into a megabyte.
	MaxStackEntries  = 20
	maxStackEntryLen = 60
)

// Validate checks an opportunity that is about to be written.
//
// It validates the SHAPE and not the story: a rejected opportunity with an
// interview note is perfectly possible, and so is a role with no salary.
// The only things refused are values that would make the record unreadable
// or unbounded.
func (o *Opportunity) Validate() error {
	if strings.TrimSpace(o.Role) == "" {
		return Invalid("a role is required")
	}
	if len([]rune(o.Role)) > maxRole {
		return Invalid("role is longer than %d characters", maxRole)
	}
	if len([]rune(o.Salary)) > maxSalary {
		return Invalid("salary is longer than %d characters", maxSalary)
	}
	if len([]rune(o.Location)) > maxLocation {
		return Invalid("location is longer than %d characters", maxLocation)
	}
	if len([]rune(o.Description)) > maxDescription {
		return Invalid("description is longer than %d characters", maxDescription)
	}
	if len([]rune(o.SourceURL)) > maxSourceURL {
		return Invalid("source url is longer than %d characters", maxSourceURL)
	}
	if len(o.Stack) > MaxStackEntries {
		return Invalid("an opportunity may carry at most %d stack entries", MaxStackEntries)
	}
	for _, s := range o.Stack {
		if len([]rune(s)) > maxStackEntryLen {
			return Invalid("stack entry %q is longer than %d characters", s, maxStackEntryLen)
		}
	}
	if o.MatchPercent != nil && (*o.MatchPercent < 0 || *o.MatchPercent > 100) {
		return Invalid("match percent must be between 0 and 100")
	}
	if strings.TrimSpace(o.Source) == "" {
		return Invalid("a source is required")
	}
	for name, note := range map[string]string{
		"general": o.Notes.General, "interview": o.Notes.Interview,
		"technical": o.Notes.Technical, "personal": o.Notes.Personal,
	} {
		if len([]rune(note)) > maxNote {
			return Invalid("the %s note is longer than %d characters", name, maxNote)
		}
	}
	if o.Tracking != nil {
		if !o.Tracking.Stage.Valid() {
			return Invalid("unknown stage %q", o.Tracking.Stage)
		}
		if len([]rune(o.Tracking.NextAction)) > maxNextAction {
			return Invalid("next action is longer than %d characters", maxNextAction)
		}
	}
	return nil
}

// NormalizeStack trims entries and drops empties, preserving order.
//
// Order is preserved because a posting lists its stack in a meaningful
// order ("Go, Postgres, React" is not a set), and sorting it would rewrite
// what the source said.
func NormalizeStack(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

/* ── the move ────────────────────────────────────────────────────────── */

// MoveResult is what a completed move reports back.
//
// ── Why the previous stage is in here ──────────────────────────────────
// Because the caller that asked for the move is the only party that can
// still see both sides of it, and it is what makes a confirmation
// meaningful: "moved from Saved to Applied" is checkable, "now Applied" is
// an assertion. PreviousStage is nil when the record was in Discover, which
// is a different event — an entry into the pipeline, not a move within it.
type MoveResult struct {
	Opportunity   *Opportunity
	PreviousStage *PipelineStage
	// Unchanged is true when the opportunity was already at the requested
	// stage. The move is then a no-op and no event is recorded: a history
	// that logged "applied → applied" would report activity that did not
	// happen, and the stage clock would restart on a move nobody made.
	Unchanged bool
}

// Move transitions an opportunity in memory and reports what changed.
//
// The mutation is in the domain and the persistence is in the repository,
// so the rule "entering a stage resets the stage clock but never the
// tracked-at" is stated once, here, rather than in each writer.
//
// `now` is passed in rather than read from the clock so the caller controls
// the timestamp and a test can assert on it.
func (o *Opportunity) Move(to PipelineStage, now time.Time) (MoveResult, error) {
	if !to.Valid() {
		return MoveResult{}, Invalid("unknown stage %q; the pipeline stages are %s",
			to, strings.Join(StageNames(), ", "))
	}

	// From Discover into the pipeline: this is the first tracking record,
	// so tracked_at and stage_entered_at are the same instant and there is
	// no previous stage.
	if o.Tracking == nil {
		o.Tracking = &Tracking{Stage: to, TrackedAt: now, StageEnteredAt: now}
		o.UpdatedAt = now
		return MoveResult{Opportunity: o}, nil
	}

	if o.Tracking.Stage == to {
		return MoveResult{Opportunity: o, PreviousStage: &to, Unchanged: true}, nil
	}

	previous := o.Tracking.Stage
	o.Tracking.Stage = to
	o.Tracking.StageEnteredAt = now
	o.UpdatedAt = now
	return MoveResult{Opportunity: o, PreviousStage: &previous}, nil
}
