package app

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursor_RoundTrip(t *testing.T) {
	t.Parallel()
	want := Cursor{
		OccurredAt: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC),
		ID:         uuid.MustParse("11111111-2222-3333-4444-555555555555"),
	}
	got, err := DecodeCursor(EncodeCursor(want))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OccurredAt.Equal(want.OccurredAt) {
		t.Fatalf("occurred_at = %s, want %s", got.OccurredAt, want.OccurredAt)
	}
	if got.ID != want.ID {
		t.Fatalf("id = %s, want %s", got.ID, want.ID)
	}
}

func TestCursor_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"bad base64", "!@#$"},
		{"valid base64 but bad json", "Zm9v"}, // "foo"
		{"empty json", "e30"},                 // "{}"
		{"missing id", "eyJvIjoiMjAyNi0wNS0xOVQxMDowMDowMFoifQ"},                               // {"o":"..."}
		{"missing occurred_at", "eyJpIjoiMTExMTExMTEtMjIyMi0zMzMzLTQ0NDQtNTU1NTU1NTU1NTU1In0"}, // {"i":"..."}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := DecodeCursor(c.in); err == nil {
				t.Fatalf("expected error for input %q", c.in)
			}
		})
	}
}

func TestCursor_OpaqueNonEmpty(t *testing.T) {
	t.Parallel()
	s := EncodeCursor(Cursor{
		OccurredAt: time.Now(),
		ID:         uuid.New(),
	})
	if s == "" {
		t.Fatal("encode produced empty string")
	}
	if strings.ContainsAny(s, "{}\":/") {
		t.Fatalf("cursor leaked structured chars: %q", s)
	}
}
