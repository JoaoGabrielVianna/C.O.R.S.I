package tools

import (
	"strconv"
	"strings"
)

// Money at the agent boundary.
//
// ── There is exactly one representation, and it is the domain's ────────
// `int64` cents. It is what `finance.transactions.amount_cents` stores,
// what `domain.Transaction` carries, what the HTTP API accepts and what
// every aggregation sums. Nothing in this package parses a currency string,
// holds a float, or scales a value — because a second representation is not
// a convenience, it is a second answer to "how much was it", and the two
// only have to disagree once.
//
// So the INPUT contract of every write tool is `amount_cents`, declared as
// an integer. That choice is doing real work: the schema validator rejects
// a JSON number with a fractional part outright (see decodeProperty in
// chat/domain/tool.go), so a model that sends 89.90 gets an actionable
// invalid-arguments failure instead of a row storing 89 cents.
//
// ── Why the OUTPUT also carries a formatted string ─────────────────────
// Because the remaining failure is one the schema cannot catch: a model
// that reads "R$ 89,90" and computes 89, or 899, sends a well-formed
// integer that happens to be wrong by two orders of magnitude. Nothing
// downstream can tell.
//
// Every tool result therefore states the amount BOTH ways — the canonical
// integer and a string rendered from that same integer. The model's
// confirmation to the user is then checkable against what was actually
// stored: it says "registrei R$ 89,00" and the person reading it sees the
// mistake in the same breath the mistake was made.
//
// The derivation runs in ONE direction only. `format` turns stored cents
// into text; nothing turns text back into cents. The string is never an
// argument, never parsed, and never round-tripped.

// currencySymbol is the one currency this context represents.
//
// ── Why it is a constant and not a column ──────────────────────────────
// Because Finance has no currency column, no exchange rate and no
// multi-currency rule anywhere in its domain — every amount in the schema
// is an unqualified integer of cents. Rendering a symbol here is naming
// the assumption the whole module already makes, in the one place a reader
// will look for it. Inventing a `currency` argument would be worse: it
// would let a model declare a value the database cannot store and the
// aggregations cannot honour.
const currencySymbol = "R$"

// formatCents renders stored cents for a person to read: "R$ 1.234,56".
//
// Integer arithmetic throughout — the value never becomes a float, not even
// briefly, because a float that is only wrong in the last bit still prints
// the wrong cent.
//
// Grouping is "." and the decimal separator is "," because that is how the
// operator's screens already render the same rows; a tool result that
// disagreed with the interface about how a number is spelled would make the
// two look like different amounts.
func formatCents(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	units := cents / 100
	frac := cents % 100

	var b strings.Builder
	b.WriteString(currencySymbol)
	b.WriteByte(' ')
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(group(strconv.FormatInt(units, 10)))
	b.WriteByte(',')
	if frac < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatInt(frac, 10))
	return b.String()
}

// group inserts a "." every three digits from the right.
func group(digits string) string {
	n := len(digits)
	if n <= 3 {
		return digits
	}
	var b strings.Builder
	lead := n % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(digits[:lead])
	for i := lead; i < n; i += 3 {
		b.WriteByte('.')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// amountFields is the pair every result carries for one amount: the
// canonical integer under `<name>_cents`, and the readable rendering under
// `<name>`.
//
// One function so the two can never be produced from different values,
// which is the entire safety property.
func amountFields(out map[string]any, name string, cents int64) {
	out[name+"_cents"] = cents
	out[name] = formatCents(cents)
}
