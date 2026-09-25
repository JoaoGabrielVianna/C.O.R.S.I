// Package domain is the Closet bounded context: the pieces of a real
// wardrobe, the angles they were photographed from, and the looks they are
// combined into.
//
// It imports nothing from the platform and nothing from any other module.
// It is plain data, the vocabulary over it, and the rules that vocabulary
// implies.
package domain

import (
	"strings"
)

/* ── slot ────────────────────────────────────────────────────────────── */

// Slot is a POSITION in a composition. The list is closed.
//
// ── Why this one is closed and Category is not ─────────────────────────
// A slot is structural: it is where a piece sits when a look is drawn, and
// every surface that renders a look — the builder, the gallery card, a
// future export — has to agree on the set and on its order. Adding one
// changes what a look IS.
//
// A category is descriptive: hats, bags, belts and scarves are all things a
// wardrobe grows, and every one of them fills a slot that already exists.
// That is the whole reason the two are separate types rather than one
// string used twice.
type Slot string

const (
	SlotTop       Slot = "top"
	SlotBottom    Slot = "bottom"
	SlotLayer     Slot = "layer"
	SlotShoes     Slot = "shoes"
	SlotWatch     Slot = "watch"
	SlotAccessory Slot = "accessory"
)

// SlotDef describes one slot's behaviour in a composition.
type SlotDef struct {
	Slot Slot
	// Capacity is how many pieces the slot holds. One for everything that
	// is worn once — you have one pair of legs — and more for `accessory`,
	// which is the only slot where "a chain AND a cap" is an outfit rather
	// than a contradiction.
	Capacity int
}

// slotOrder is the order a look is drawn in: the garments from the
// outermost layer down to the shoes, and then the things that are not
// garments.
//
// ── Why the order lives here and not in the frontend ───────────────────
// Because a look saved today and re-opened next year has to look the same,
// and a stacking order kept in a component is one refactor away from
// changing what a stored record appears to be. The screen reads this order
// top to bottom; it does not choose it.
var slotOrder = []SlotDef{
	{Slot: SlotLayer, Capacity: 1},
	{Slot: SlotTop, Capacity: 1},
	{Slot: SlotBottom, Capacity: 1},
	{Slot: SlotShoes, Capacity: 1},
	{Slot: SlotWatch, Capacity: 1},
	// The one multi-occupancy slot. Four is a bound rather than a taste:
	// it keeps a look's payload small and a malformed client from filing a
	// hundred rows under one composition.
	{Slot: SlotAccessory, Capacity: 4},
}

// Slots returns the slot definitions in composition order.
func Slots() []SlotDef {
	out := make([]SlotDef, len(slotOrder))
	copy(out, slotOrder)
	return out
}

// SlotCapacity reports how many pieces a slot holds, and whether the slot
// exists at all.
func SlotCapacity(s Slot) (int, bool) {
	for _, d := range slotOrder {
		if d.Slot == s {
			return d.Capacity, true
		}
	}
	return 0, false
}

func (s Slot) Valid() bool {
	_, ok := SlotCapacity(s)
	return ok
}

func (s Slot) String() string { return string(s) }

// SlotNames is the vocabulary as plain strings, built from the same slice
// so a slot cannot be added to the order and forgotten in an error message.
func SlotNames() []string {
	out := make([]string, len(slotOrder))
	for i, d := range slotOrder {
		out[i] = string(d.Slot)
	}
	return out
}

// ParseSlot turns caller input into a slot.
//
// Lenient about case and surrounding space, strict about everything else:
// it will not guess that "shoe" meant "shoes", because a near-match here
// would put a garment in a position nobody chose.
func ParseSlot(raw string) (Slot, error) {
	s := Slot(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", Invalid("unknown slot %q; the slots are %s",
			raw, strings.Join(SlotNames(), ", "))
	}
	return s, nil
}

/* ── view ────────────────────────────────────────────────────────────── */

// ImageView is the angle a photograph was taken from.
//
// ── Why the angle is named and not numbered ────────────────────────────
// Because the builder asks a question about meaning: "show me this folded".
// An images[0] would answer a question about upload order, which is not
// information about the garment. Naming the angle is also what lets a watch
// refuse `hanger_front` rather than accept it and render something absurd.
type ImageView string

const (
	// Garment views.
	//
	// ViewOpen is the piece laid out flat and open — the catalogue shot.
	ViewOpen ImageView = "open"
	// ViewHangerFront and ViewHangerSide are the piece on a hanger, which is
	// how most of a wardrobe is actually photographed.
	ViewHangerFront ImageView = "hanger_front"
	ViewHangerSide  ImageView = "hanger_side"
	// ViewFolded is the piece folded. It is the composition view for
	// garments: a folded shape stacks into a legible outfit where an open
	// one sprawls. See CategoryDef.Composition.
	ViewFolded ImageView = "folded"

	// Views for things that are neither hung nor folded.
	ViewFront  ImageView = "front"
	ViewSide   ImageView = "side"
	ViewTop    ImageView = "top"
	ViewDetail ImageView = "detail"
)

func (v ImageView) String() string { return string(v) }

/* ── category ────────────────────────────────────────────────────────── */

// Category is what a piece IS. The vocabulary is open by design.
type Category string

const (
	CategoryTops        Category = "tops"
	CategoryBottoms     Category = "bottoms"
	CategoryLayers      Category = "layers"
	CategoryShoes       Category = "shoes"
	CategoryWatches     Category = "watches"
	CategoryAccessories Category = "accessories"
)

func (c Category) String() string { return string(c) }

// CategoryDef is everything the rest of the system needs to know about a
// category, in one record.
//
// ══════════════════════════════════════════════════════════════════════
//
//	ADDING A CATEGORY IS ONE ENTRY IN `catalog` BELOW. NOTHING ELSE.
//
// ══════════════════════════════════════════════════════════════════════
//
// No migration, because the database bounds the LENGTH of `category` and
// not its value. No new slot, because a new category names a slot that
// already exists. No change to the image tables, because the views a
// category allows are read from here. That property is the point of the
// whole file: a wardrobe taxonomy grows, and a growth that costs a schema
// change and a coordinated deploy is a growth that does not happen.
type CategoryDef struct {
	Category Category
	// Slot is the position a piece of this category fills in a look. Many
	// categories may name the same slot — `accessories` and a future `hats`
	// both fill `accessory` — and none may name a slot that does not exist.
	Slot Slot
	// Views is every angle a piece of this category may carry, in the order
	// a capture flow should offer them.
	//
	// It is a whitelist, and the refusal it produces is the point: a watch
	// has no `hanger_front`, and a system that accepted one would be
	// pretending a watch hangs in a closet.
	Views []ImageView
	// Composition is the fallback chain the look builder walks to decide
	// which image to draw, first present wins.
	//
	// ── Why it is separate from Views ──────────────────────────────────
	// Because capture order and drawing preference are different questions
	// with different answers. Capturing a shirt starts with the open shot,
	// which is the one that shows what it is; DRAWING a shirt in a stacked
	// outfit wants the folded one, which is the one that stacks. Deriving
	// one from the other would force a compromise that is wrong for both.
	//
	// Every entry must also appear in Views. `catalog` is checked for that
	// at package init, so a typo is a panic at start-up rather than a piece
	// that silently never renders.
	Composition []ImageView
}

// catalog is the vocabulary. It is the ONLY copy that validates.
//
// The frontend has a list too, and that list is a rendering order for the
// category rail — it arrives from `GET /closet/catalog`, which serves this
// slice, so it cannot drift. Nothing else declares categories.
var catalog = []CategoryDef{
	{
		Category: CategoryTops,
		Slot:     SlotTop,
		Views:    []ImageView{ViewOpen, ViewHangerFront, ViewHangerSide, ViewFolded},
		// Folded first: an outfit reads as a stack of folded shapes. Open is
		// the fallback because it is the view most likely to exist on a
		// piece that was photographed once.
		Composition: []ImageView{ViewFolded, ViewOpen, ViewHangerFront, ViewHangerSide},
	},
	{
		Category:    CategoryBottoms,
		Slot:        SlotBottom,
		Views:       []ImageView{ViewOpen, ViewHangerFront, ViewHangerSide, ViewFolded},
		Composition: []ImageView{ViewFolded, ViewOpen, ViewHangerFront, ViewHangerSide},
	},
	{
		Category:    CategoryLayers,
		Slot:        SlotLayer,
		Views:       []ImageView{ViewOpen, ViewHangerFront, ViewHangerSide, ViewFolded},
		Composition: []ImageView{ViewFolded, ViewOpen, ViewHangerFront, ViewHangerSide},
	},
	{
		Category: CategoryShoes,
		Slot:     SlotShoes,
		// No hanger and no folded. A shoe is photographed from the side,
		// from above and head-on, and offering it a hanger view would be the
		// taxonomy lying about the object.
		Views:       []ImageView{ViewSide, ViewTop, ViewFront},
		Composition: []ImageView{ViewSide, ViewFront, ViewTop},
	},
	{
		Category:    CategoryWatches,
		Slot:        SlotWatch,
		Views:       []ImageView{ViewFront, ViewDetail},
		Composition: []ImageView{ViewFront, ViewDetail},
	},
	{
		Category: CategoryAccessories,
		Slot:     SlotAccessory,
		// The widest set, because this category is the one that holds the
		// most different KINDS of object — a cap, a chain, a belt. When one
		// of those earns its own category it takes the subset it needs and
		// leaves this one alone.
		Views:       []ImageView{ViewFront, ViewDetail, ViewSide},
		Composition: []ImageView{ViewFront, ViewSide, ViewDetail},
	},
}

// init refuses to start with an inconsistent catalog.
//
// ── Why a panic and not a test ─────────────────────────────────────────
// There is a test too. The panic is here because the invariants it checks
// are what every other rule in this package assumes: that a category names
// a real slot, and that a composition preference is a view the category can
// actually hold. A catalog that violates either produces a piece that can
// be created and never drawn, which is the kind of defect that looks like a
// rendering bug for a week.
func init() {
	seen := map[Category]bool{}
	for _, def := range catalog {
		if seen[def.Category] {
			panic("closet: duplicate category in catalog: " + def.Category.String())
		}
		seen[def.Category] = true

		if !def.Slot.Valid() {
			panic("closet: category " + def.Category.String() +
				" names slot " + def.Slot.String() + ", which is not a slot")
		}
		if len(def.Views) == 0 {
			panic("closet: category " + def.Category.String() + " declares no views")
		}
		allowed := map[ImageView]bool{}
		for _, v := range def.Views {
			if allowed[v] {
				panic("closet: category " + def.Category.String() +
					" lists view " + v.String() + " twice")
			}
			allowed[v] = true
		}
		if len(def.Composition) == 0 {
			panic("closet: category " + def.Category.String() +
				" declares no composition preference")
		}
		for _, v := range def.Composition {
			if !allowed[v] {
				panic("closet: category " + def.Category.String() +
					" prefers view " + v.String() + " for composition, which it cannot carry")
			}
		}
	}
}

// Categories returns the catalog in rail order.
func Categories() []CategoryDef {
	out := make([]CategoryDef, len(catalog))
	copy(out, catalog)
	return out
}

// CategoryNames is the vocabulary as plain strings.
func CategoryNames() []string {
	out := make([]string, len(catalog))
	for i, d := range catalog {
		out[i] = string(d.Category)
	}
	return out
}

// LookupCategory resolves a category to its definition.
func LookupCategory(c Category) (CategoryDef, bool) {
	for _, d := range catalog {
		if d.Category == c {
			return d, true
		}
	}
	return CategoryDef{}, false
}

// ParseCategory turns caller input into a category.
//
// Lenient about case and space for the reason ParseSlot is: a person typing
// into a form and a client echoing a stored value will both produce
// "Tops" or " tops ". It will not guess beyond that.
func ParseCategory(raw string) (Category, error) {
	c := Category(strings.ToLower(strings.TrimSpace(raw)))
	if _, ok := LookupCategory(c); !ok {
		return "", Invalid("unknown category %q; the categories are %s",
			raw, strings.Join(CategoryNames(), ", "))
	}
	return c, nil
}

// ParseView resolves an angle IN THE CONTEXT OF A CATEGORY.
//
// There is deliberately no category-free version. A bare `ParseView` would
// answer "is this a view the system knows", which is the wrong question:
// `folded` is a perfectly good view and an impossible one for a watch, and
// only a check that carries the category can say so.
func ParseView(c Category, raw string) (ImageView, error) {
	def, ok := LookupCategory(c)
	if !ok {
		return "", Invalid("unknown category %q", c)
	}
	v := ImageView(strings.ToLower(strings.TrimSpace(raw)))
	for _, allowed := range def.Views {
		if allowed == v {
			return v, nil
		}
	}
	return "", Invalid("%q is not a view a %s can have; the views for %s are %s",
		raw, c, c, strings.Join(viewNames(def.Views), ", "))
}

func viewNames(views []ImageView) []string {
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = string(v)
	}
	return out
}

/* ── occasion ────────────────────────────────────────────────────────── */

// Occasion is a label on a look. Open vocabulary, owned here.
//
// It is a LABEL and never a rule: nothing refuses a combination because of
// it, nothing scores a look against it, and nothing recommends. It exists
// so a gallery of thirty looks has a way to find the one for the wedding.
type Occasion string

const (
	OccasionCasual Occasion = "casual"
	OccasionDate   Occasion = "date"
	OccasionWork   Occasion = "work"
	OccasionParty  Occasion = "party"
	OccasionFormal Occasion = "formal"
	// OccasionOther is the default, and it is a real answer rather than a
	// missing one: most looks are not for an occasion, and forcing a choice
	// would fill the field with whichever option sat first in a dropdown.
	OccasionOther Occasion = "other"
)

var occasions = []Occasion{
	OccasionCasual, OccasionDate, OccasionWork,
	OccasionParty, OccasionFormal, OccasionOther,
}

func Occasions() []Occasion {
	out := make([]Occasion, len(occasions))
	copy(out, occasions)
	return out
}

func OccasionNames() []string {
	out := make([]string, len(occasions))
	for i, o := range occasions {
		out[i] = string(o)
	}
	return out
}

func (o Occasion) Valid() bool {
	for _, known := range occasions {
		if known == o {
			return true
		}
	}
	return false
}

func (o Occasion) String() string { return string(o) }

// ParseOccasion turns caller input into an occasion. An EMPTY string
// resolves to `other` rather than failing: the field is optional on every
// surface that offers it, and a look with no occasion is the common case.
func ParseOccasion(raw string) (Occasion, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return OccasionOther, nil
	}
	o := Occasion(trimmed)
	if !o.Valid() {
		return "", Invalid("unknown occasion %q; the occasions are %s",
			raw, strings.Join(OccasionNames(), ", "))
	}
	return o, nil
}
