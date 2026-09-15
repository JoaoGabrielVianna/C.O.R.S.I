package domain

import "strings"

// Sensitivity is how exposed a piece of this context's content may be.
//
// ── Three levels, and what each one actually changes ───────────────────
//
//	normal            ordinary content of this workspace
//	private           the operator's own. Readable by the workspace, and
//	                  the label says it is not casual material
//	highly_sensitive  withheld from any listing that did not ask for it
//	                  explicitly. Reachable by id
//
// ── Why `private` changes no behaviour, and is still worth storing ─────
// Because it is the honest answer to "how private is this" for most of
// what a person writes down, and collapsing it into `normal` would force
// every genuinely private note to be labelled `highly_sensitive` just to
// be labelled at all. The level that hides things would then be worn by
// content that nobody needs hidden, and the hiding would stop meaning
// anything. A label with no mechanism today is still a label the operator
// wrote on purpose, and it is what a future mechanism will read.
//
// ── Why only the top level is withheld ─────────────────────────────────
// A listing is how a model finds anything at all, and one that withheld
// `private` would make most of this context invisible to the capability
// that exists to search it. The line is drawn where the cost of an
// accidental appearance is worse than the cost of an extra call: a broad
// listing is precisely how content enters a context nobody asked for it
// to be in, and "lista tudo da sala X" is not a request to see the most
// private thing in it.
type Sensitivity string

const (
	SensitivityNormal          Sensitivity = "normal"
	SensitivityPrivate         Sensitivity = "private"
	SensitivityHighlySensitive Sensitivity = "highly_sensitive"
)

// Sensitivities is the vocabulary in ascending order of exposure.
//
// The same three names appear in the CHECK constraints of
// migrations/palace/0001_init.up.sql, which are backstops. This package
// is the only copy that validates.
var Sensitivities = []Sensitivity{
	SensitivityNormal,
	SensitivityPrivate,
	SensitivityHighlySensitive,
}

// DefaultSensitivity is what content gets when nobody said.
//
// `normal`, and that is a deliberate default rather than a cautious one:
// defaulting to `private` would label everything private, which has the
// same effect as labelling nothing. The operator says when something is
// more than ordinary.
const DefaultSensitivity = SensitivityNormal

func (s Sensitivity) Valid() bool {
	switch s {
	case SensitivityNormal, SensitivityPrivate, SensitivityHighlySensitive:
		return true
	}
	return false
}

func (s Sensitivity) String() string { return string(s) }

// HiddenFromBroadListing reports whether a listing that did not ask for
// this level must leave it out.
//
// ── Why this is a predicate and not a comparison at each call site ─────
// Because it is the ONE place the rule is decided. Every repository
// filter, every tool that lists, and every future surface asks this
// question, and three spellings of `!= highly_sensitive` scattered across
// layers is how one of them ends up missing the day a fourth level
// exists. The capabilities express the opt-in as
// `include_highly_sensitive`, named after exactly this predicate.
func (s Sensitivity) HiddenFromBroadListing() bool {
	return s == SensitivityHighlySensitive
}

// SensitivityNames is the vocabulary as plain strings, for a schema
// description or an error message.
func SensitivityNames() []string { return names(Sensitivities) }

/* ── the order ───────────────────────────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	normal  <  private  <  highly_sensitive
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why an order exists at all ─────────────────────────────────────────
// Because provenance creates a floor. A memory that rests on private
// evidence cannot itself be ordinary: the conclusion would appear in
// listings that the material behind it is withheld from, which leaks the
// material by describing it. Answering "is this level at least that one"
// requires the three words to be comparable, so they are.
//
// ── Why the order is declared and not inferred from the slice ──────────
// `Sensitivities` happens to be in ascending order and that is a
// convenience for rendering, not a promise. A vocabulary reordered for
// presentation must not silently reorder the rule that decides what may
// be withheld.

// Rank is the position of a level on the exposure scale. Higher is more
// restricted.
//
// An invalid level ranks -1, which is below everything, so any comparison
// involving one fails closed: see AtLeastAsRestrictiveAs.
func (s Sensitivity) Rank() int {
	switch s {
	case SensitivityNormal:
		return 0
	case SensitivityPrivate:
		return 1
	case SensitivityHighlySensitive:
		return 2
	}
	return -1
}

// AtLeastAsRestrictiveAs reports whether s withholds at least as much as
// other.
//
// Fails closed: an invalid level on either side answers false, so a
// caller checking a floor refuses rather than admits. A permission
// predicate that said yes on a value it did not recognise would be
// wrong in the one direction that costs something.
func (s Sensitivity) AtLeastAsRestrictiveAs(other Sensitivity) bool {
	if !s.Valid() || !other.Valid() {
		return false
	}
	return s.Rank() >= other.Rank()
}

// MaxSensitivity returns the most restricted level in a set, and whether
// the set had one.
//
// ── Why this is computed here and not in SQL ───────────────────────────
// Because the order is a domain rule, and a `CASE WHEN sensitivity = …`
// in a query would be a second copy of it, free to disagree the day a
// fourth level exists. The repository returns the distinct levels it
// found, which is at most three rows, and the comparison happens where
// the order is defined.
//
// An invalid level in the set makes the answer invalid, for the reason
// AtLeastAsRestrictiveAs fails closed: a floor computed from a value
// nobody recognises is not a floor.
func MaxSensitivity(levels []Sensitivity) (Sensitivity, bool) {
	if len(levels) == 0 {
		return DefaultSensitivity, false
	}
	highest := levels[0]
	for _, l := range levels[1:] {
		if !l.Valid() {
			return l, true
		}
		if !highest.Valid() {
			continue
		}
		if l.Rank() > highest.Rank() {
			highest = l
		}
	}
	return highest, true
}

// ParseSensitivity turns caller input into a level.
//
// Lenient about case and surrounding space, strict about everything else,
// and pointedly unwilling to guess. "sensitive" is not
// "highly_sensitive": an approximate match here would either hide
// ordinary content or, far worse, file something the operator called
// sensitive at a level that shows it in every listing.
func ParseSensitivity(raw string) (Sensitivity, error) {
	s := Sensitivity(fold(raw))
	if !s.Valid() {
		return "", Invalid("unknown sensitivity %s; the levels are %s",
			quoteToken(raw), strings.Join(SensitivityNames(), ", "))
	}
	return s, nil
}
