package domain

import (
	"strings"
	"testing"
)

func TestDeriveTitle(t *testing.T) {
	long := strings.Repeat("palavra ", 30) // far past the rune budget

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"short message is used verbatim", "Quanto gastei em maio?", "Quanto gastei em maio?"},
		{"newlines collapse to spaces", "primeira linha\nsegunda linha", "primeira linha segunda linha"},
		{"runs of whitespace collapse", "muito     espaço", "muito espaço"},
		{"blank input gets a placeholder", "   \n  ", "Nova conversa"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveTitle(tc.input); got != tc.want {
				t.Fatalf("DeriveTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}

	t.Run("long message is cut on a word boundary", func(t *testing.T) {
		got := DeriveTitle(long)
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("expected an ellipsis, got %q", got)
		}
		if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
			t.Fatalf("trailing space before the ellipsis: %q", got)
		}
		if n := len([]rune(got)); n > maxTitleRunes+1 {
			t.Fatalf("title is %d runes, want <= %d", n, maxTitleRunes+1)
		}
	})

	t.Run("unbroken text still gets cut", func(t *testing.T) {
		got := DeriveTitle(strings.Repeat("x", 200))
		if n := len([]rune(got)); n != maxTitleRunes+1 {
			t.Fatalf("title is %d runes, want %d", n, maxTitleRunes+1)
		}
	})

	t.Run("accents are counted as single runes", func(t *testing.T) {
		// 40 accented chars fit the 60-rune budget, though not the byte one.
		in := strings.Repeat("çã", 20)
		if got := DeriveTitle(in); got != in {
			t.Fatalf("accented title was truncated: %q", got)
		}
	})
}
