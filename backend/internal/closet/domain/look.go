package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxLookNotes = 4000

/* ── the look ────────────────────────────────────────────────────────── */

// Look is a set of pieces chosen together.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A LOOK HOLDS REFERENCES, NEVER COPIES
//
// ══════════════════════════════════════════════════════════════════════
//
// No garment name, no colour, no brand and above all no image bytes are
// stored on a look. Re-opening one reads today's pieces through their ids,
// which is what makes three things true at once: renaming a shirt renames
// it inside every look, replacing one piece is one row rather than a
// rebuild, and a photograph re-shot at a better angle appears in looks that
// were saved before it existed.
//
// The alternative — snapshotting what the pieces looked like — would
// produce a gallery that slowly stopped matching the wardrobe, and would do
// it silently.
type Look struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"-"`

	Name     string   `json:"name"`
	Occasion Occasion `json:"occasion"`
	Favorite bool     `json:"favorite"`
	Notes    string   `json:"notes,omitempty"`
	Status   Status   `json:"status"`

	// Items are the filled slots, in composition order. Populated on read.
	Items []LookItem `json:"items"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LookItem is one filled slot.
//
// ── Why the slot is stored and not read off the item's category ────────
// Because they answer different questions. The item's category says what
// the taxonomy calls that garment TODAY; this column says which position it
// was filed into when the look was composed. They agree now and will not
// always: moving a category to a different slot is exactly the kind of
// correction a growing wardrobe invites, and a look is a record of a
// decision that was already made.
type LookItem struct {
	Slot Slot `json:"slot"`
	// Position orders the pieces inside a multi-occupancy slot. Always 0
	// for the five single slots.
	Position int       `json:"position"`
	ItemID   uuid.UUID `json:"item_id"`

	// Item is the piece as it is right now, joined in on read. Nil on the
	// write path, where only the id is meaningful.
	//
	// It is here for the same reason jobradar joins the company name: a
	// look that made the caller resolve six uuids itself would be a look
	// nobody could draw.
	Item *ClosetItem `json:"item,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// Validate checks a look that is about to be written. Shape only — a look
// with one piece is a perfectly good draft, and refusing it would mean the
// builder could not save work in progress.
func (l *Look) Validate() error {
	if strings.TrimSpace(l.Name) == "" {
		return Invalid("a name is required")
	}
	if len([]rune(l.Name)) > maxName {
		return Invalid("name is longer than %d characters", maxName)
	}
	if !l.Occasion.Valid() {
		return Invalid("unknown occasion %q; the occasions are %s",
			l.Occasion, strings.Join(OccasionNames(), ", "))
	}
	if len([]rune(l.Notes)) > maxLookNotes {
		return Invalid("notes are longer than %d characters", maxLookNotes)
	}
	if !l.Status.Valid() {
		return Invalid("unknown status %q", l.Status)
	}
	return nil
}

func (l *Look) Normalize() {
	l.Name = strings.TrimSpace(l.Name)
	l.Notes = strings.TrimSpace(l.Notes)
}

/* ── composing ───────────────────────────────────────────────────────── */

// Composition is a look's filled slots, arranged for a caller that has to
// draw them.
type Composition struct {
	items []LookItem
}

// NewComposition builds one from stored rows, in the canonical order.
func NewComposition(items []LookItem) *Composition {
	c := &Composition{}
	c.items = append(c.items, items...)
	c.sort()
	return c
}

// Items returns the filled slots in composition order.
func (c *Composition) Items() []LookItem {
	out := make([]LookItem, len(c.items))
	copy(out, c.items)
	return out
}

// Place puts a piece into a slot and reports what it displaced.
//
// ══════════════════════════════════════════════════════════════════════
//
//	CLICKING A SECOND SHIRT REPLACES THE FIRST. IT DOES NOT STACK
//
// ══════════════════════════════════════════════════════════════════════
//
// That is the whole interaction this module exists to get right, and it is
// a rule about the DOMAIN rather than about a click handler. A single
// slot holds one piece: placing into a full one evicts the occupant and
// says so, so the caller can tell the difference between "added a layer"
// and "swapped the layer". A multi-occupancy slot appends until it is full,
// and then the oldest entry is the one that leaves — so a person adding a
// fifth accessory gets a fifth accessory rather than a refusal they have to
// go and resolve.
//
// `replaced` is nil when nothing was displaced.
func (c *Composition) Place(slot Slot, itemID uuid.UUID, now time.Time) (replaced []LookItem, err error) {
	capacity, ok := SlotCapacity(slot)
	if !ok {
		return nil, Invalid("unknown slot %q; the slots are %s",
			slot, strings.Join(SlotNames(), ", "))
	}

	// The same piece twice in one look is not a composition, it is a
	// double click. Moving it to the requested slot rather than refusing:
	// the caller asked for this garment in this position, and that is a
	// statement about where it goes, not an error.
	for idx, existing := range c.items {
		if existing.ItemID == itemID {
			if existing.Slot == slot {
				return nil, nil
			}
			c.items = append(c.items[:idx], c.items[idx+1:]...)
			break
		}
	}

	occupants := c.inSlot(slot)
	if len(occupants) >= capacity {
		// Evict from the front: for a single slot that is the one occupant,
		// and for `accessory` it is the piece chosen longest ago.
		evictCount := len(occupants) - capacity + 1
		for _, victim := range occupants[:evictCount] {
			replaced = append(replaced, victim)
			c.remove(victim.Slot, victim.Position, victim.ItemID)
		}
	}

	c.items = append(c.items, LookItem{
		Slot:      slot,
		ItemID:    itemID,
		CreatedAt: now,
	})
	c.sort()
	c.renumber(slot)
	return replaced, nil
}

// Remove clears a piece from the composition. Removing something that is
// not there is a no-op rather than an error: the caller's intent —
// "this slot should be empty" — is satisfied either way, and a screen that
// double-fired a remove should not show an error for having got what it
// asked for.
func (c *Composition) Remove(itemID uuid.UUID) bool {
	for idx, existing := range c.items {
		if existing.ItemID == itemID {
			slot := existing.Slot
			c.items = append(c.items[:idx], c.items[idx+1:]...)
			c.renumber(slot)
			return true
		}
	}
	return false
}

// ClearSlot empties a whole slot, which for the five single slots is the
// same as removing its occupant and for `accessory` is "take it all off".
func (c *Composition) ClearSlot(slot Slot) int {
	kept := c.items[:0]
	removed := 0
	for _, existing := range c.items {
		if existing.Slot == slot {
			removed++
			continue
		}
		kept = append(kept, existing)
	}
	c.items = kept
	return removed
}

func (c *Composition) inSlot(slot Slot) []LookItem {
	var out []LookItem
	for _, existing := range c.items {
		if existing.Slot == slot {
			out = append(out, existing)
		}
	}
	return out
}

func (c *Composition) remove(slot Slot, position int, itemID uuid.UUID) {
	for idx, existing := range c.items {
		if existing.Slot == slot && existing.Position == position && existing.ItemID == itemID {
			c.items = append(c.items[:idx], c.items[idx+1:]...)
			return
		}
	}
}

// renumber closes the gaps in one slot's positions.
//
// Positions are part of the primary key, so a slot that went 0,2 after a
// removal would make the next append collide with 2 or skip to 3 forever.
// Closing the gap keeps them dense, which is the only property anything
// downstream relies on.
func (c *Composition) renumber(slot Slot) {
	next := 0
	for idx := range c.items {
		if c.items[idx].Slot != slot {
			continue
		}
		c.items[idx].Position = next
		next++
	}
}

// sort orders by slot — in the canonical composition order — and then by
// the moment each piece was chosen, so a slot's positions follow the order
// the operator picked them.
func (c *Composition) sort() {
	rank := make(map[Slot]int, len(slotOrder))
	for idx, def := range slotOrder {
		rank[def.Slot] = idx
	}
	rankOf := func(s Slot) int {
		if r, ok := rank[s]; ok {
			return r
		}
		return len(slotOrder)
	}
	for i := 1; i < len(c.items); i++ {
		for j := i; j > 0; j-- {
			a, b := c.items[j-1], c.items[j]
			if rankOf(b.Slot) > rankOf(a.Slot) {
				break
			}
			if rankOf(b.Slot) == rankOf(a.Slot) && !b.CreatedAt.Before(a.CreatedAt) {
				break
			}
			c.items[j-1], c.items[j] = c.items[j], c.items[j-1]
		}
	}
}
