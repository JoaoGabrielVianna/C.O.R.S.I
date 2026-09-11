package domain

import (
	"fmt"
	"time"
)

// Importing a legacy document: identity, and the history it carries.
//
// ── What makes this different from creating ────────────────────────────
// A create states a fact about now. An import states a fact about a past
// that happened somewhere else, and it therefore needs three things an
// ordinary create must never be allowed to express:
//
//	identity    which legacy record this row IS, so the same document
//	            arriving twice is recognised rather than duplicated
//	clock       the moments the record was created, updated and moved,
//	            which are weeks old and are the point of migrating at all
//	history     the transitions it already went through
//
// Every one of those is a loaded gun pointed at the audit story if a normal
// client can reach it — backdating a creation, or writing a transition that
// never happened, are things nothing except an import should be able to do.
// That is why they live on their own input type and their own service
// method rather than as optional fields on the ordinary one.

/* ── identity ────────────────────────────────────────────────────────── */

// ImportSourceLocalStorageV1 names the browser document this product
// migrates from: `corsi.module.jobradar.v1`.
//
// The version is part of the name on purpose. A future `v2` document would
// number its records in its own space, and an id that means one record
// there and another one here must not be able to collide.
const ImportSourceLocalStorageV1 = "jobradar.localstorage.v1"

const (
	maxImportSource     = 120
	maxImportExternalID = 200
)

// ImportIdentity is where a row came from, when it came from somewhere.
//
// The zero value means "not imported", which is what every hand-created
// opportunity carries and is a real answer rather than a missing one.
type ImportIdentity struct {
	// Source is the document format, e.g. ImportSourceLocalStorageV1.
	Source string `json:"import_source,omitempty"`
	// ExternalID is the identifier the record already had inside that
	// document — `op_mpbh75to_fl6bu` in the legacy browser store.
	//
	// ── Why the legacy id and not the source URL ───────────────────────
	// Because it is the only field guaranteed to exist and to be unique
	// inside the document. A source URL is frequently empty, is sometimes
	// shared by two postings, and is edited by users; the local id is
	// generated once and never touched. Where it is absent, there is no
	// identity, and the import says so rather than inventing one.
	ExternalID string `json:"import_external_id,omitempty"`
}

// Zero reports an opportunity that was not imported.
func (i ImportIdentity) Zero() bool { return i.Source == "" && i.ExternalID == "" }

// Validate refuses half an identity.
//
// Both or neither: a source with no id would make every record from one
// document collide, and an id with no source would let two importers claim
// the same string. The database enforces the same rule; this is the check
// that produces a sentence a person can act on.
func (i ImportIdentity) Validate() error {
	if i.Zero() {
		return nil
	}
	if i.Source == "" || i.ExternalID == "" {
		return Invalid("an import identity needs both a source and an external id")
	}
	if len(i.Source) > maxImportSource {
		return Invalid("import_source is too long")
	}
	if len(i.ExternalID) > maxImportExternalID {
		return Invalid("import_external_id is too long")
	}
	return nil
}

/* ── history ─────────────────────────────────────────────────────────── */

// StageVisit is one entry of the legacy document's `tracking.history`:
// a stage, and when it was entered.
//
// It is the SOURCE shape, not the stored one. The browser recorded where
// the record arrived; `stage_events` records the transition between two
// stages. Turning one into the other is BuildStageTimeline's whole job, and
// keeping the two types apart is what stops the conversion being done ad
// hoc by whoever needs it next.
type StageVisit struct {
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
}

// BuildStageTimeline turns a legacy visit list into the transitions this
// domain stores.
//
//	visits:  saved@T1        applied@T2         interview@T3
//	events:  ∅→saved @T1     saved→applied @T2  applied→interview @T3
//
// ── Why the first event has no `from` ──────────────────────────────────
// Because it did not come from anywhere. NULL means "entered the pipeline
// from Discover", and writing the first stage as its own predecessor would
// invent a visit that never happened — the schema says so in as many words.
//
// ── Why an unknown stage fails the whole item ──────────────────────────
// The legacy document contains stages this domain does not have; the sample
// found in the browser moves through `recruiter`, which is not one of the
// six. There are three things one could do with that and only one of them
// is honest:
//
//	map it silently      → invents a history the user never had, and the
//	                       invention is invisible forever after
//	drop the entry       → produces a timeline with a hole, which reads as
//	                       a transition that did not happen
//	fail the item        → the record is not imported, the reason names the
//	                       stage, and a person decides what it should map to
//
// Only the third leaves the decision with someone who can actually make it.
// It fails the ITEM and not the import, so one unmappable record out of
// forty does not block the other thirty-nine.
//
// ── Why the current stage must match the last visit ────────────────────
// A row whose stage is `interview` and whose timeline ends at `applied`
// describes two different pasts. Appending a transition to reconcile them
// would be inventing one; storing them as they are would ship a record that
// contradicts itself. So it is refused, and the message says both values.
func BuildStageTimeline(visits []StageVisit, current PipelineStage) ([]StageEvent, error) {
	if len(visits) == 0 {
		return nil, nil
	}

	events := make([]StageEvent, 0, len(visits))
	var previous *PipelineStage

	for i, v := range visits {
		stage, err := ParseStage(v.Stage)
		if err != nil {
			return nil, Invalid(
				"stage history entry %d names %q, which is not a stage this system has (%s); "+
					"decide what it should become before importing this record",
				i, v.Stage, stageList())
		}
		if v.At.IsZero() {
			return nil, Invalid("stage history entry %d (%s) has no timestamp", i, stage)
		}
		// A repeated stage is not an error: a record really can go back to a
		// stage it already visited, and the legacy sample does exactly that.
		// It is only skipped when it would be a transition to itself, which
		// is not a transition.
		if previous != nil && *previous == stage {
			continue
		}
		from := previous
		events = append(events, StageEvent{
			FromStage:  from,
			ToStage:    stage,
			OccurredAt: v.At.UTC(),
		})
		s := stage
		previous = &s
	}

	if len(events) == 0 {
		return nil, nil
	}
	if last := events[len(events)-1].ToStage; last != current {
		return nil, Invalid(
			"stage history ends at %q but the record's current stage is %q; "+
				"one of the two is wrong and this import will not guess which",
			last, current)
	}
	return events, nil
}

func stageList() string {
	names := StageNames()
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return fmt.Sprintf("one of: %s", out)
}
