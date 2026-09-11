package app

import (
	"testing"
)

// The wire composition used to be tested here, against a private helper in
// send.go. That helper became the Context Builder in context.go, and the
// four cases it covered moved to TestBuildContextComposition with their
// expectations unchanged. Nothing was dropped in the move.

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 10); got != "abc" {
		t.Fatalf("short string was altered: %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc" {
		t.Fatalf("truncate = %q, want %q", got, "abc")
	}
}

func TestClampList(t *testing.T) {
	cases := []struct {
		limit, offset      int
		wantLimit, wantOff int
	}{
		{0, 0, 50, 0},    // unset falls back to the default page
		{500, 0, 50, 0},  // above the ceiling falls back too
		{20, 40, 20, 40}, // sane values pass through
		{20, -5, 20, 0},  // negative offset is clamped
	}
	for _, tc := range cases {
		got := clampList(tc.limit, tc.offset)
		if got.Limit != tc.wantLimit || got.Offset != tc.wantOff {
			t.Errorf("clampList(%d, %d) = {%d, %d}, want {%d, %d}",
				tc.limit, tc.offset, got.Limit, got.Offset, tc.wantLimit, tc.wantOff)
		}
	}
}
