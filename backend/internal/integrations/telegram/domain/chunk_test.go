package domain

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// nonSpace is what "no content loss" is measured on.
//
// Chunking is allowed to drop whitespace AT a boundary it chose — that is
// what keeps a bubble from starting with a blank line — and is allowed to
// drop nothing else. Comparing the non-whitespace runes of the input
// against the non-whitespace runes of the joined output states exactly
// that, with no room for a "close enough".
func nonSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
}

func units(s string) int { return len(utf16.Encode([]rune(s))) }

// TestChunkLosesNothing.
//
// The guarantee that matters most, asserted on bytes rather than
// described. A splitter that silently dropped the tail would pass every
// "each chunk fits" test ever written.
func TestChunkLosesNothing(t *testing.T) {
	cases := map[string]string{
		"short":             "olá",
		"exactly at budget": strings.Repeat("a", chunkBudget),
		"one over":          strings.Repeat("a", chunkBudget+1),
		"paragraphs":        strings.Repeat("parágrafo de teste.\n\n", 500),
		"one long line":     strings.Repeat("palavra ", 2000),
		"no boundaries":     strings.Repeat("x", 20000),
		"accents":           strings.Repeat("ação coração não é só ", 900),
		"astral":            strings.Repeat("🇧🇷🙂", 3000),
		"mixed":             strings.Repeat("a🙂é\n", 4000),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := Chunk(in)
			if joined, want := nonSpace(strings.Join(got, "")), nonSpace(in); joined != want {
				t.Fatalf("content lost or added: got %d non-space runes, want %d",
					utf8.RuneCountInString(joined), utf8.RuneCountInString(want))
			}
		})
	}
}

// TestEveryChunkFitsTelegramsLimit.
//
// Measured in UTF-16 code units, which is the unit Telegram counts. A
// chunker that measured runes passes a rune-based assertion and gets a 400
// from the Bot API on the first message containing a flag emoji.
func TestEveryChunkFitsTelegramsLimit(t *testing.T) {
	for _, in := range []string{
		strings.Repeat("🇧🇷", 5000),
		strings.Repeat("a", 50000),
		strings.Repeat("é\n\n", 5000),
		strings.Repeat("palavra ", 5000),
	} {
		for i, c := range Chunk(in) {
			if u := units(c); u > MaxMessageUnits {
				t.Fatalf("chunk %d is %d utf-16 units, over telegram's %d", i, u, MaxMessageUnits)
			}
		}
	}
}

// TestChunkNeverSplitsARune.
//
// A hard split is the fallback for input with no boundaries in it, and
// the fallback is where a byte-oriented splitter cuts a multi-byte rune in
// half. The input here has no space, no newline and no punctuation, so
// every split is the fallback.
func TestChunkNeverSplitsARune(t *testing.T) {
	in := strings.Repeat("🙂", 6000)
	for i, c := range Chunk(in) {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is not valid utf-8; a rune was split", i)
		}
		for _, r := range c {
			if r == utf8.RuneError {
				t.Fatalf("chunk %d contains a replacement character; a rune was split", i)
			}
		}
	}
}

// TestChunkIsDeterministic. Same input, same chunks — no clock, no map
// iteration, no randomness anywhere in the path.
func TestChunkIsDeterministic(t *testing.T) {
	in := strings.Repeat("uma frase qualquer. outra frase.\n\n", 400)
	first := Chunk(in)
	for i := 0; i < 20; i++ {
		got := Chunk(in)
		if len(got) != len(first) {
			t.Fatalf("run %d produced %d chunks, first run produced %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d chunk %d differs from the first run", i, j)
			}
		}
	}
}

// TestChunkPrefersParagraphBoundaries.
//
// Not cosmetic. A splitter that cut mid-sentence on text that had blank
// lines in it would be choosing the worst available boundary, and nothing
// else in the suite would notice.
func TestChunkPrefersParagraphBoundaries(t *testing.T) {
	para := strings.Repeat("x", 500)
	in := strings.Repeat(para+"\n\n", 20) // ~10k units, several chunks

	for i, c := range Chunk(in) {
		// Every chunk should end at a paragraph end, which here means it
		// ends with the last character of a paragraph and contains only
		// whole paragraphs.
		for _, part := range strings.Split(c, "\n\n") {
			if p := strings.TrimSpace(part); p != "" && p != para {
				t.Fatalf("chunk %d contains a partial paragraph (%d chars, want %d)",
					i, len(p), len(para))
			}
		}
	}
}

// TestChunkOfEmptyIsNothing. An empty chunk would be a Bot API 400:
// Telegram refuses a message with no text.
func TestChunkOfEmptyIsNothing(t *testing.T) {
	if got := Chunk(""); got != nil {
		t.Fatalf("Chunk(\"\") = %#v, want nil", got)
	}
	if got := Chunk("   \n\n  "); len(got) != 0 && strings.TrimSpace(got[0]) == "" {
		t.Fatalf("Chunk of whitespace produced an empty bubble: %#v", got)
	}
}

// TestShortTextIsOneChunkUnchanged.
//
// The overwhelmingly common case. A chunker that reflowed, trimmed or
// normalised ordinary-length text would be editing the model's answer.
func TestShortTextIsOneChunkUnchanged(t *testing.T) {
	in := "Primeira linha.\n\n  Segunda linha, com espaços.  \n\tterceira"
	got := Chunk(in)
	if len(got) != 1 || got[0] != in {
		t.Fatalf("Chunk(%q) = %#v, want exactly the input in one chunk", in, got)
	}
}
