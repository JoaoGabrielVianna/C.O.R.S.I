package domain

import (
	"time"

	"github.com/google/uuid"
)

// Provenance: which evidence a memory rests on.
//
// ══════════════════════════════════════════════════════════════════════
//
//	APPEND-ONLY IS A PALACE CORE v1 DECISION, NOT AN ETERNAL INVARIANT
//
// ══════════════════════════════════════════════════════════════════════
//
// In this version a link is created and never edited or removed. There
// is no Change type in this file and no unlink operation anywhere above
// it.
//
// ── Why, for this version ──────────────────────────────────────────────
// Because the question provenance answers is "why did I believe this",
// asked later, by somebody who was not there. A link that can be taken
// back casually answers a different question: "why do I believe this
// NOW", which is a thing the memory's own content already says. The
// value of the record is that it is not routinely tidied.
//
// It is also what makes the sensitivity floor below hold in practice: a
// floor that can be lifted by unlinking is a floor that exists until it
// is inconvenient.
//
// ── What is explicitly left open ───────────────────────────────────────
// A future EXPLICIT correction of provenance may be added. The case is
// real and foreseeable: a consolidator, or a person moving quickly,
// cites the wrong transcript, and the record then says a belief rests on
// something it never rested on. Refusing to ever fix that would be
// preserving a falsehood in the name of preserving history.
//
// What that correction must NOT be, whenever it arrives:
//
//   - a silent unlink, or one that happens as a side effect of editing
//     something else;
//   - a way to lower a memory's sensitivity by quietly removing the
//     evidence that raised the floor;
//   - a general Update, which would let the link be rewritten to point
//     somewhere else and leave no trace that it ever pointed elsewhere.
//
// It has to be a deliberate, named operation, and it has to reckon with
// the floor it removes. None of that is designed here. What is decided
// is only this: the absence of unlink in v1 is a scope decision, and a
// future session may add an explicit correction without treating it as a
// violation of this file.
//
// ── What is NOT provenance ─────────────────────────────────────────────
// A memory's Room and its Artifact are CONTEXTUAL links: they say what
// the memory is filed under and what it is about. Provenance says what it
// rests on. Only the second one constrains anything, and conflating them
// would either make filing a note in a private project silently re-label
// the note, or make evidence as weak as a folder.

// MemorySource is one provenance link.
//
// ── Why it is a typed row and not a Relation ───────────────────────────
// Because `palace.relations` is polymorphic and therefore cannot carry a
// foreign key of any kind: a fabricated source id would insert cleanly
// and point at nothing. This is the one link in the context with a hard
// integrity requirement, so it gets a table where the database
// guarantees both ends exist AND belong to the same workspace. See the
// header of relation.go, which records the same decision from the other
// side.
type MemorySource struct {
	WorkspaceID uuid.UUID
	MemoryID    uuid.UUID
	SourceID    uuid.UUID
	CreatedAt   time.Time
}

func (ms *MemorySource) Validate() error {
	if ms.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if ms.MemoryID == uuid.Nil {
		return Invalid("a memory is required")
	}
	if ms.SourceID == uuid.Nil {
		return Invalid("a source is required")
	}
	return nil
}

/* ── the sensitivity floor ───────────────────────────────────────────── */

// ValidateSensitivityFloor refuses a memory that is less withheld than
// the evidence behind it.
//
// ── The leak this closes ───────────────────────────────────────────────
// A memory that rests on a private source and is itself `normal` appears
// in every default listing. The source does not, and that asymmetry is
// the leak: the conclusion describes the material. "Ele contou que vai
// sair da empresa em março" filed as ordinary, drawn from a transcript
// filed as private, publishes the private thing in the act of
// summarising it.
//
// ── Why the memory is refused rather than raised ───────────────────────
// Because raising it would be the system deciding, silently, that
// something the operator called ordinary is now private, and the operator
// would find out when they went looking for it in a listing where it no
// longer is. Sensitivity is a statement somebody made on purpose. A
// refusal costs one more call and leaves the decision where it belongs.
//
// ── Why the refusal cannot be worked around, in this version ───────────
// Provenance is append-only here, so the floor a link establishes cannot
// be lifted by detaching the evidence. A memory that rests on private
// evidence can be raised at any time and cannot be lowered below private
// while the link stands. That is stated in the message, because a caller
// told only "refused" would reasonably go looking for an unlink that
// does not exist.
//
// If an explicit provenance correction is ever added, it inherits this:
// removing a link may lower a floor, which is a consequence that has to
// be decided on rather than discovered. See the header of this file.
//
// ── Why the message is safe ────────────────────────────────────────────
// It names two levels and nothing else. Both are closed vocabulary; the
// memory's content and the source's content are not mentioned, and must
// not be. See errors.go.
func ValidateSensitivityFloor(memory, evidence Sensitivity) error {
	if !evidence.Valid() {
		return Invalid("the evidence carries an unrecognised sensitivity, so no floor could be established")
	}
	if !memory.Valid() {
		return Invalid("unknown sensitivity %s; the levels are %s",
			quoteToken(string(memory)), joinNames(SensitivityNames()))
	}
	if memory.AtLeastAsRestrictiveAs(evidence) {
		return nil
	}
	return Invalid(
		"a memory of sensitivity %s cannot rest on evidence of sensitivity %s; "+
			"raise the memory to at least %s first. Provenance is append-only in this "+
			"version, so this floor cannot be lifted by detaching the evidence",
		memory, evidence, evidence)
}

// MayRestOn reports whether this memory is allowed to cite this source.
//
// The named form of the floor check, for the one caller that has both
// entities in hand. It exists so the link path and the relabel path
// cannot drift into two slightly different rules.
func (m *Memory) MayRestOn(s *Source) error {
	if s == nil {
		return Invalid("no evidence was supplied")
	}
	return ValidateSensitivityFloor(m.Sensitivity, s.Sensitivity)
}
