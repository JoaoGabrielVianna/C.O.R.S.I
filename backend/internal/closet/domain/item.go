package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

/* ── status ──────────────────────────────────────────────────────────── */

// Status is whether a piece is still in the wardrobe.
//
// ── Why archived and not deleted ───────────────────────────────────────
// Because a look is a record of a decision, and a decision made about a
// coat you no longer own is still a decision you made. Deleting the coat
// would either take the look with it or leave the look pointing at nothing;
// archiving keeps both true — the piece stops being offered in the
// selector, and every look that already used it still resolves.
//
// This is why there is no delete endpoint for an item anywhere in this
// module, and why look_items.item_id is ON DELETE RESTRICT.
type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
)

func (s Status) Valid() bool {
	return s == StatusActive || s == StatusArchived
}

func (s Status) String() string { return string(s) }

func ParseStatus(raw string) (Status, error) {
	s := Status(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", Invalid("unknown status %q; the statuses are active, archived", raw)
	}
	return s, nil
}

/* ── the item ────────────────────────────────────────────────────────── */

const (
	maxName      = 120
	maxSubtype   = 60
	maxColor     = 40
	maxBrand     = 80
	maxItemNotes = 4000
)

// ClosetItem is one real garment.
//
// Every field here is something a person typed or photographed. Nothing is
// inferred, scored or generated: this sprint builds the wardrobe a stylist
// would later read, and a wardrobe with invented attributes is not one.
type ClosetItem struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"-"`

	Name     string   `json:"name"`
	Category Category `json:"category"`
	Subtype  string   `json:"subtype,omitempty"`

	PrimaryColor   string `json:"primary_color"`
	SecondaryColor string `json:"secondary_color,omitempty"`
	Brand          string `json:"brand,omitempty"`
	Notes          string `json:"notes,omitempty"`

	Favorite bool   `json:"favorite"`
	Status   Status `json:"status"`

	// Images are the angles this piece carries, in the order its category
	// declares. Populated on read; a listing that showed pieces without
	// them would be a table, which is the thing this module is not.
	Images []ItemImage `json:"images"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Slot is the position this piece fills in a look, derived from its
// category rather than stored.
//
// ── Why derived ───────────────────────────────────────────────────────
// A column would be a second place the answer lives, and the two would
// disagree the first time a category's slot is corrected. The look records
// the slot it was filed under — see LookItem — which is a different fact:
// that one is what was DECIDED, this one is what the taxonomy says today.
func (i *ClosetItem) Slot() (Slot, bool) {
	def, ok := LookupCategory(i.Category)
	if !ok {
		return "", false
	}
	return def.Slot, true
}

// CompositionImage picks the image the look builder should draw, by walking
// the category's preference chain and taking the first angle that exists.
//
// Returning false is a real answer and the screens render it as one: a
// piece with no photographs yet is a piece the builder shows by name. It is
// NOT an error — cataloguing a garment and photographing it are separate
// acts, and forcing them into one would mean a wardrobe you cannot start.
func (i *ClosetItem) CompositionImage() (ItemImage, bool) {
	def, ok := LookupCategory(i.Category)
	if !ok {
		return ItemImage{}, false
	}
	for _, want := range def.Composition {
		for _, img := range i.Images {
			if img.View == want {
				return img, true
			}
		}
	}
	return ItemImage{}, false
}

// Validate checks a piece that is about to be written.
//
// It validates the SHAPE, not the taste: a lime green formal shirt is a
// perfectly possible garment, and a piece with no brand and no notes is the
// common case. The only things refused are values that would make the row
// unreadable or unbounded, plus the two that carry meaning elsewhere in the
// system — the category, because a slot is derived from it, and the status.
func (i *ClosetItem) Validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return Invalid("a name is required")
	}
	if len([]rune(i.Name)) > maxName {
		return Invalid("name is longer than %d characters", maxName)
	}
	if _, ok := LookupCategory(i.Category); !ok {
		return Invalid("unknown category %q; the categories are %s",
			i.Category, strings.Join(CategoryNames(), ", "))
	}
	if len([]rune(i.Subtype)) > maxSubtype {
		return Invalid("subtype is longer than %d characters", maxSubtype)
	}
	if strings.TrimSpace(i.PrimaryColor) == "" {
		return Invalid("a primary colour is required")
	}
	if len([]rune(i.PrimaryColor)) > maxColor {
		return Invalid("primary colour is longer than %d characters", maxColor)
	}
	if len([]rune(i.SecondaryColor)) > maxColor {
		return Invalid("secondary colour is longer than %d characters", maxColor)
	}
	if len([]rune(i.Brand)) > maxBrand {
		return Invalid("brand is longer than %d characters", maxBrand)
	}
	if len([]rune(i.Notes)) > maxItemNotes {
		return Invalid("notes are longer than %d characters", maxItemNotes)
	}
	if !i.Status.Valid() {
		return Invalid("unknown status %q", i.Status)
	}
	return nil
}

// Normalize trims the free-text fields in place.
//
// Separate from Validate because trimming is a change and validating is a
// question, and a function that silently did both would make "is this
// valid" depend on having called it.
func (i *ClosetItem) Normalize() {
	i.Name = strings.TrimSpace(i.Name)
	i.Subtype = strings.TrimSpace(i.Subtype)
	i.PrimaryColor = strings.TrimSpace(i.PrimaryColor)
	i.SecondaryColor = strings.TrimSpace(i.SecondaryColor)
	i.Brand = strings.TrimSpace(i.Brand)
	i.Notes = strings.TrimSpace(i.Notes)
}

/* ── images ──────────────────────────────────────────────────────────── */

// ItemImage is one angle of one piece, pointing at the bytes.
//
// It carries the asset's dimensions and size but never its bytes. A listing
// of forty pieces with four views each is a hundred and sixty rows, and a
// hundred and sixty images inlined into one JSON response would be tens of
// megabytes to render a grid. The bytes are fetched one at a time from
// `GET /closet/assets/{id}`, which is also the only place the cache headers
// have to be right.
type ItemImage struct {
	ID      uuid.UUID `json:"id"`
	ItemID  uuid.UUID `json:"item_id"`
	View    ImageView `json:"view"`
	AssetID uuid.UUID `json:"asset_id"`

	ContentType string `json:"content_type"`
	ByteSize    int    `json:"byte_size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SortImages orders a piece's images by its category's declared view order.
//
// ── Why order is computed rather than stored ───────────────────────────
// Because a stored position would be a third place the order lives —
// beside the category's Views and beside whatever the screen does — and
// three places is two places too many. A view the category no longer
// declares sorts last rather than being dropped: the image exists, somebody
// uploaded it, and a taxonomy edit is not a reason to stop showing it.
func SortImages(c Category, images []ItemImage) []ItemImage {
	def, ok := LookupCategory(c)
	if !ok {
		return images
	}
	rank := make(map[ImageView]int, len(def.Views))
	for idx, v := range def.Views {
		rank[v] = idx
	}
	rankOf := func(v ImageView) int {
		if r, ok := rank[v]; ok {
			return r
		}
		return len(def.Views)
	}
	// Insertion sort: a piece has at most a handful of views, and this keeps
	// the order stable for two images that rank the same.
	out := make([]ItemImage, len(images))
	copy(out, images)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && rankOf(out[j].View) < rankOf(out[j-1].View); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
