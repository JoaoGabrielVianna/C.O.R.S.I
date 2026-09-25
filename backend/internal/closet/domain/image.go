package domain

import (
	"bytes"
	"crypto/sha256"
	"image"
	// Registers the PNG decoder with the image package. Blank import
	// because nothing here names the package: `image.DecodeConfig` finds it
	// through the registry, which is exactly the indirection that keeps
	// this file from knowing anything format-specific beyond the constant
	// below.
	_ "image/png"

	"github.com/google/uuid"
	"time"
)

/* ── the asset ───────────────────────────────────────────────────────── */

// Asset is the stored bytes of one image, plus what the server determined
// about them.
//
// Bytes are deliberately NOT a JSON field. This struct crosses the
// repository boundary with them and never crosses the HTTP boundary at all
// — the handler writes them to the response body directly.
type Asset struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"-"`

	// SHA256 is the digest of Bytes, and the identity that makes a repeated
	// upload resolve to the row that already exists.
	SHA256      []byte `json:"-"`
	ContentType string `json:"content_type"`
	ByteSize    int    `json:"byte_size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`

	Bytes []byte `json:"-"`

	CreatedAt time.Time `json:"created_at"`
}

/* ── the image contract ──────────────────────────────────────────────── */

// ContentTypePNG is the one format this closet accepts.
//
// The contract the operator works to, stated once: a PNG, cut out, with a
// transparent background, framed consistently across the wardrobe. The
// cutting-out happens OUTSIDE this product — this sprint ships no image
// processing of any kind, and the server's entire role is to verify that
// what arrived is a real PNG and to hand the same bytes back unchanged.
const ContentTypePNG = "image/png"

// MaxImageBytes bounds one upload.
//
// Eight megabytes is far above a cut-out garment on a transparent
// background, which runs in the hundreds of kilobytes, and low enough that
// a mistaken upload of something else — a RAW file, a video renamed — is
// refused at the door rather than after it has been read into memory and
// written to a row.
//
// It is enforced in TWO places on purpose, and neither is redundant: the
// HTTP layer caps the request body so an oversized upload is never fully
// read, and this check catches anything that reaches the domain by another
// path. A limit that only exists in a handler is a limit the next caller
// does not have.
const MaxImageBytes = 8 << 20 // 8 MiB

// MaxImageDimension bounds either side of an image.
//
// A closet is rendered at a few hundred pixels per piece. Anything past
// this is a photograph nobody cropped, and storing it would mean paying for
// it on every read of the row.
const MaxImageDimension = 4096

// MinImageDimension refuses a file that decodes but is not a picture — a
// one-pixel PNG is a valid PNG and is not a photograph of a shirt.
const MinImageDimension = 16

// DecodeImage verifies uploaded bytes and reports what they are.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE CALLER'S CLAIM ABOUT THE FILE IS NEVER USED. NOT ITS NAME, NOT
//	ITS EXTENSION, NOT ITS DECLARED CONTENT-TYPE
//
// ══════════════════════════════════════════════════════════════════════
//
// All three are strings a client chose. A multipart part can declare
// `image/png` and carry anything; a filename can declare `.png` and, worse,
// can declare `../../etc/passwd`. Deciding what a file IS from what it SAYS
// is the shape of most upload vulnerabilities, and the only reason this
// product has no filename-handling bug is that it never handles a filename:
// the asset's identity is a uuid the database issues and a digest this
// function computes, and no part of either comes from the request.
//
// What it actually does is decode the image header with the standard
// library. A file that is not a PNG does not decode as one, whatever it
// claims; a PNG that decodes is a PNG.
func DecodeImage(raw []byte) (Asset, error) {
	if len(raw) == 0 {
		return Asset{}, Invalid("the uploaded file is empty")
	}
	if len(raw) > MaxImageBytes {
		return Asset{}, Invalid("the image is larger than %d MB", MaxImageBytes>>20)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		// The decoder's own error is not repeated to the caller. It
		// describes a byte offset in a file the operator did not write, and
		// the actionable fact is the only one stated here.
		return Asset{}, Invalid("that file is not a readable image; the closet accepts PNG")
	}
	if format != "png" {
		return Asset{}, Invalid("the closet accepts PNG, and that file is %s", format)
	}
	if cfg.Width < MinImageDimension || cfg.Height < MinImageDimension {
		return Asset{}, Invalid("the image is %dx%d, which is smaller than %dx%d",
			cfg.Width, cfg.Height, MinImageDimension, MinImageDimension)
	}
	if cfg.Width > MaxImageDimension || cfg.Height > MaxImageDimension {
		return Asset{}, Invalid("the image is %dx%d, and neither side may exceed %d pixels",
			cfg.Width, cfg.Height, MaxImageDimension)
	}

	sum := sha256.Sum256(raw)
	return Asset{
		SHA256:      sum[:],
		ContentType: ContentTypePNG,
		ByteSize:    len(raw),
		Width:       cfg.Width,
		Height:      cfg.Height,
		Bytes:       raw,
	}, nil
}
