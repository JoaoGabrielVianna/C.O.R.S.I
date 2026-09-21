package domain

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

/* ── one answer, several transport messages ──────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	SPLIT THE TRANSPORT, NEVER THE RECORD
//
// ══════════════════════════════════════════════════════════════════════
//
// C.O.R.S.I. persists ONE assistant message, whatever its length. Telegram
// refuses one over its limit. Those are two different problems and only
// the second one is Telegram's: chunking happens on the way out, after the
// turn is written, and nothing in this file can reach the database.
//
// Writing four `chat.messages` rows because Telegram wanted four bubbles
// would corrupt the transcript for every other surface — the web client
// would render four answers, the context window would replay four
// assistant turns, and the token accounting would attribute one turn's
// cost to the first of them.

// MaxMessageUnits is Telegram's `sendMessage` text ceiling.
//
// ── Why the unit is UTF-16 and this is not pedantry ────────────────────
// The Bot API counts 4096 UTF-16 CODE UNITS, not bytes and not runes. The
// three agree for ASCII and disagree everywhere the operator actually
// writes:
//
//	"é"   1 rune   2 bytes   1 UTF-16 unit
//	"→"   1 rune   3 bytes   1 UTF-16 unit
//	"🇧🇷"  2 runes  8 bytes   4 UTF-16 units
//
// Counting runes would over-send on emoji and get a 400 from Telegram on
// exactly the messages an operator is most likely to notice. Counting
// bytes would under-send on ordinary Portuguese, cutting a 4000-character
// answer into three. So the measure is the one Telegram uses.
const MaxMessageUnits = 4096

// chunkBudget leaves headroom under the ceiling.
//
// The margin is not superstition: a chunk may gain a continuation marker,
// and a hard split must never land between a surrogate pair. Sitting 96
// units below the limit costs nothing and removes a class of off-by-one
// that only reproduces on somebody's phone.
const chunkBudget = MaxMessageUnits - 96

// Chunk splits text into pieces Telegram will accept, losing nothing.
//
// ── The guarantees, in order of how much they matter ───────────────────
//
//  1. NO CONTENT LOSS. Concatenating the result reproduces the input
//     exactly, except for whitespace at a boundary the split chose — and
//     the test asserts that on the exact bytes, not on a description.
//  2. DETERMINISTIC. Same input, same chunks, always. There is no
//     randomness, no locale, and no clock in here.
//  3. NEVER SPLITS A RUNE, and never a surrogate pair: the fallback
//     advances by runes and measures in UTF-16, so a 4-byte emoji is
//     either wholly in a chunk or wholly in the next.
//  4. PREFERS A BOUNDARY a reader would have chosen: paragraph, then
//     line, then sentence-ish, then word. Only when none exists inside
//     the budget — a 5000-character URL, a base64 blob — does it cut
//     mid-word, which is the honest thing to do with input that has no
//     boundaries in it.
//
// Input with nothing in it returns nil rather than one chunk, and
// "nothing in it" includes whitespace: Telegram will not accept a blank
// message, and a caller that sends whatever it gets back must not try. A
// turn that produced no words is handled where it belongs — see
// app/reply.go, which says so in a sentence rather than sending a blank
// bubble.
func Chunk(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if utf16Len(text) <= chunkBudget {
		return []string{text}
	}

	var out []string
	rest := text
	for rest != "" {
		if utf16Len(rest) <= chunkBudget {
			out = append(out, rest)
			break
		}
		head, tail := splitOnce(rest)
		// splitOnce always makes progress — see its own guard — so this
		// loop cannot spin. The check is here anyway because "cannot
		// happen" in a loop that talks to a user's phone is worth one
		// comparison.
		if head == "" {
			out = append(out, rest)
			break
		}
		out = append(out, head)
		rest = tail
	}

	// Trim only what the split itself introduced. A chunk that came back
	// as nothing but the whitespace at a boundary is dropped rather than
	// sent: Telegram refuses an empty message, and an empty bubble is not
	// content anybody lost.
	kept := out[:0]
	for _, c := range out {
		if strings.TrimSpace(c) != "" {
			kept = append(kept, c)
		}
	}
	return kept
}

// splitOnce takes the largest prefix that fits, cutting at the best
// boundary available inside the budget.
func splitOnce(s string) (head, tail string) {
	// The byte offset just past the last rune that still fits. Everything
	// below searches inside s[:limit] only, which is what makes every
	// candidate boundary safe by construction.
	limit := prefixWithin(s, chunkBudget)
	if limit >= len(s) {
		return s, ""
	}
	window := s[:limit]

	// Boundaries, best first. Each is looked for from the END of the
	// window so the chunk stays as full as possible: a splitter that
	// picked the FIRST paragraph break would turn a long answer into a
	// message per paragraph.
	for _, sep := range []string{"\n\n", "\n", ". ", "! ", "? ", "; ", " "} {
		if i := strings.LastIndex(window, sep); i > 0 {
			// The separator goes with the head for punctuation, and is
			// dropped for whitespace — cutting after ". " keeps the
			// sentence intact, and keeping a trailing newline would start
			// the next bubble with a blank line.
			cut := i + len(sep)
			return strings.TrimRight(s[:cut], " \n\r\t"), s[cut:]
		}
	}
	// No boundary anywhere in the window. Hard split on the rune
	// boundary, which is the only correctness guarantee left to keep.
	return window, s[limit:]
}

// prefixWithin returns the byte offset of the longest prefix of s whose
// UTF-16 length is at most `units`.
//
// It walks runes rather than bytes, so the offset it returns is always a
// rune boundary — the property the hard-split fallback relies on.
func prefixWithin(s string, units int) int {
	used, off := 0, 0
	for off < len(s) {
		r, size := utf8.DecodeRuneInString(s[off:])
		w := 1
		if r > 0xFFFF {
			// Outside the BMP: two UTF-16 units. An emoji taken as one
			// would be the off-by-one that only shows up in production.
			w = 2
		}
		if used+w > units {
			return off
		}
		used += w
		off += size
	}
	return len(s)
}

// utf16Len counts the units Telegram counts.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }
