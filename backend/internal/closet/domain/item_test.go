package domain

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/google/uuid"
)

/* ── items ───────────────────────────────────────────────────────────── */

func validItem() *ClosetItem {
	return &ClosetItem{
		Name:         "Camiseta preta",
		Category:     CategoryTops,
		PrimaryColor: "preto",
		Status:       StatusActive,
	}
}

func TestItemValidateAcceptsTheMinimum(t *testing.T) {
	t.Parallel()
	// Name, category and one colour. Everything else is optional, because a
	// wardrobe that demanded a brand would be a wardrobe nobody finishes
	// entering.
	if err := validItem().Validate(); err != nil {
		t.Fatalf("a minimal piece was refused: %v", err)
	}
}

func TestItemValidateRefusesWhatWouldBeUnreadable(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*ClosetItem){
		"blank name":       func(i *ClosetItem) { i.Name = "  " },
		"blank colour":     func(i *ClosetItem) { i.PrimaryColor = "" },
		"unknown category": func(i *ClosetItem) { i.Category = Category("hats") },
		"unknown status":   func(i *ClosetItem) { i.Status = Status("gone") },
		"name too long":    func(i *ClosetItem) { i.Name = strings.Repeat("x", maxName+1) },
		"notes too long":   func(i *ClosetItem) { i.Notes = strings.Repeat("x", maxItemNotes+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			item := validItem()
			mutate(item)
			if err := item.Validate(); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestSlotIsDerivedFromCategory(t *testing.T) {
	t.Parallel()
	// Derived and not stored: a column would be a second place the answer
	// lives, and the two would disagree the day a category's slot is fixed.
	item := validItem()
	slot, ok := item.Slot()
	if !ok || slot != SlotTop {
		t.Fatalf("a top fills slot %q (ok=%v), want %q", slot, ok, SlotTop)
	}

	item.Category = CategoryWatches
	slot, ok = item.Slot()
	if !ok || slot != SlotWatch {
		t.Fatalf("a watch fills slot %q (ok=%v), want %q", slot, ok, SlotWatch)
	}
}

func TestCompositionImageWalksTheFallbackChain(t *testing.T) {
	t.Parallel()
	// Tops prefer folded, then open. A piece photographed only from the
	// hanger still draws.
	item := validItem()
	item.Images = []ItemImage{
		{ID: uuid.New(), View: ViewHangerFront},
		{ID: uuid.New(), View: ViewOpen},
	}
	got, ok := item.CompositionImage()
	if !ok {
		t.Fatal("a piece with two images produced no composition image")
	}
	if got.View != ViewOpen {
		t.Fatalf("composition picked %q, want %q — open outranks hanger_front", got.View, ViewOpen)
	}

	folded := ItemImage{ID: uuid.New(), View: ViewFolded}
	item.Images = append(item.Images, folded)
	got, _ = item.CompositionImage()
	if got.View != ViewFolded {
		t.Fatalf("composition picked %q once folded existed, want %q", got.View, ViewFolded)
	}
}

func TestAPieceWithNoImagesIsNotAnError(t *testing.T) {
	t.Parallel()
	// Cataloguing a garment and photographing it are separate acts. Forcing
	// them into one means a wardrobe you cannot start.
	if _, ok := validItem().CompositionImage(); ok {
		t.Fatal("a piece with no images reported a composition image")
	}
}

func TestSortImagesUsesTheCategoryOrderAndKeepsStrangers(t *testing.T) {
	t.Parallel()
	// A view the category no longer declares sorts LAST rather than
	// vanishing: somebody uploaded that photograph, and a taxonomy edit is
	// not a reason to stop showing it.
	images := []ItemImage{
		{View: ViewFolded},
		{View: ViewDetail}, // not a `tops` view
		{View: ViewOpen},
	}
	got := SortImages(CategoryTops, images)
	want := []ImageView{ViewOpen, ViewFolded, ViewDetail}
	for i := range want {
		if got[i].View != want[i] {
			t.Fatalf("index %d is %q, want %q (full: %+v)", i, got[i].View, want[i], got)
		}
	}
}

/* ── image bytes ─────────────────────────────────────────────────────── */

// pngBytes renders a real PNG of the requested size. Real rather than a
// fixture, so the test exercises the same decoder the server uses.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeImageAcceptsARealPNG(t *testing.T) {
	t.Parallel()
	raw := pngBytes(t, 512, 640)
	asset, err := DecodeImage(raw)
	if err != nil {
		t.Fatalf("a real PNG was refused: %v", err)
	}
	if asset.ContentType != ContentTypePNG {
		t.Fatalf("content type = %q, want %q", asset.ContentType, ContentTypePNG)
	}
	if asset.Width != 512 || asset.Height != 640 {
		t.Fatalf("dimensions = %dx%d, want 512x640", asset.Width, asset.Height)
	}
	if asset.ByteSize != len(raw) {
		t.Fatalf("byte size = %d, want %d", asset.ByteSize, len(raw))
	}
	if len(asset.SHA256) != 32 {
		t.Fatalf("digest is %d bytes, want 32", len(asset.SHA256))
	}
}

func TestDecodeImageIsDeterministic(t *testing.T) {
	t.Parallel()
	// The digest is what makes a repeated upload resolve to the row that
	// already exists. Two decodes of the same bytes must agree.
	raw := pngBytes(t, 64, 64)
	a, err := DecodeImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.SHA256, b.SHA256) {
		t.Fatal("the same bytes produced two different digests")
	}
}

func TestDecodeImageRefusesAFileThatMerelyCLAIMSToBeAPNG(t *testing.T) {
	t.Parallel()
	// ══════════════════════════════════════════════════════════════════
	// What a file SAYS it is never decides what it IS. This is the whole
	// upload defence, and it is one decode rather than a list of rules.
	// ══════════════════════════════════════════════════════════════════
	cases := map[string][]byte{
		"plain text":             []byte("this is definitely a png, trust me"),
		"a PNG signature only":   {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"empty":                  {},
		"a jpeg-looking prefix":  {0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10},
		"html from a proxy page": []byte("<!doctype html><html><body>nope</body></html>"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeImage(raw); err == nil {
				t.Fatalf("%s was accepted as an image", name)
			}
		})
	}
}

func TestDecodeImageRefusesDimensionsOutsideTheContract(t *testing.T) {
	t.Parallel()
	// A one-pixel PNG is a valid PNG and is not a photograph of a shirt.
	if _, err := DecodeImage(pngBytes(t, 4, 4)); err == nil {
		t.Fatal("a 4x4 image was accepted")
	}
	if _, err := DecodeImage(pngBytes(t, MaxImageDimension+1, 32)); err == nil {
		t.Fatal("an image wider than the ceiling was accepted")
	}
}

func TestDecodeImageRefusalsExplainThemselves(t *testing.T) {
	t.Parallel()
	// An error that only says "invalid" sends the operator to read the
	// source. It must not, however, repeat the decoder's own message: that
	// describes a byte offset in a file nobody wrote by hand.
	_, err := DecodeImage([]byte("not an image"))
	if err == nil {
		t.Fatal("garbage was accepted")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "PNG") {
		t.Fatalf("the refusal does not say what is accepted: %s", err)
	}
}
