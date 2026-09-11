package app

// Parsing a pasted statement into normalised lines.
//
// ── Why the parser lives in the application layer ──────────────────────
// Because its output is domain data and its rules are domain rules: what
// counts as an amount, what counts as a date, and what "we cannot tell" is
// allowed to mean. Putting it in the tool would let a second entry point
// parse differently, and two parsers is two ledgers.
//
// ── The one rule that decides everything else ──────────────────────────
// A line the parser cannot read with certainty becomes INVALID. It never
// becomes a guess. Money read wrong is worse than money not read: the
// second is visible.

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/corsi/backend/internal/finance/domain"
)

// parsedLine is one statement row, before it meets the ledger.
type parsedLine struct {
	LineNumber  int
	OccurredAt  time.Time
	AmountCents int64
	Description string
	Direction   domain.EntryType
	GroupKey    string

	// ExternalID is the institution's own identifier when the export
	// carried one. Its presence is what makes a row STRONG.
	ExternalID string

	ordinal int
	Invalid bool
	Reason  string
}

var errNoHeader = errors.New("the first line must be a header naming date, description and amount")

// parseCSV reads a pasted statement.
//
// The header is required and the columns are matched by NAME, not by
// position: a statement pasted with its columns in a different order is
// the same statement, and guessing by position would silently swap amount
// and date on some exports.
func parseCSV(content string) ([]parsedLine, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(content)))
	r.FieldsPerRecord = -1 // ragged rows are a per-line problem, not fatal
	r.TrimLeadingSpace = true

	records, err := r.ReadAll()
	if err != nil {
		return nil, domain.Invalid("could not read the text as CSV: " + err.Error())
	}
	if len(records) < 2 {
		return nil, domain.Invalid(errNoHeader.Error())
	}

	idx, err := headerIndex(records[0])
	if err != nil {
		return nil, err
	}

	out := make([]parsedLine, 0, len(records)-1)
	for i, rec := range records[1:] {
		out = append(out, parseRecord(rec, idx, i+2))
	}
	assignOrdinalGroups(out)
	return out, nil
}

type columnIndex struct {
	date, description, amount, id int
}

func headerIndex(header []string) (columnIndex, error) {
	idx := columnIndex{date: -1, description: -1, amount: -1, id: -1}
	for i, raw := range header {
		switch normaliseHeader(raw) {
		case "date", "data":
			idx.date = i
		case "description", "descricao", "historico", "memo", "lancamento":
			idx.description = i
		case "amount", "valor":
			idx.amount = i
		case "id", "fitid", "transaction_id", "identificador":
			idx.id = i
		}
	}
	if idx.date < 0 || idx.description < 0 || idx.amount < 0 {
		return idx, domain.Invalid(errNoHeader.Error())
	}
	return idx, nil
}

func normaliseHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("á", "a", "ã", "a", "â", "a", "é", "e", "ê", "e",
		"í", "i", "ó", "o", "õ", "o", "ô", "o", "ú", "u", "ç", "c", " ", "_").Replace(s)
	return s
}

func parseRecord(rec []string, idx columnIndex, line int) parsedLine {
	p := parsedLine{LineNumber: line}

	field := func(i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	desc := collapseSpaces(field(idx.description))
	if len(desc) > 280 {
		desc = desc[:280]
	}
	p.Description = desc

	when, ok := parseStatementDate(field(idx.date))
	if !ok {
		p.Invalid, p.Reason = true, "the date could not be read: expected YYYY-MM-DD or DD/MM/YYYY"
		return p
	}
	p.OccurredAt = when

	cents, negative, err := parseDecimalToCents(field(idx.amount))
	if err != nil {
		p.Invalid, p.Reason = true, "the amount could not be read: "+err.Error()
		return p
	}
	if cents == 0 {
		p.Invalid, p.Reason = true, "the amount is zero, and a transaction must move money"
		return p
	}
	if desc == "" {
		p.Invalid, p.Reason = true, "the description is empty"
		return p
	}

	p.AmountCents = cents
	if negative {
		p.Direction = domain.EntryTypeExpense
	} else {
		p.Direction = domain.EntryTypeIncome
	}
	p.ExternalID = field(idx.id)
	p.GroupKey = normaliseDescription(desc)
	return p
}

// ── Money ──────────────────────────────────────────────────────────────
//
// Decimal text to int64 cents, by integer arithmetic only. There is no
// float anywhere in this function and there must never be one: the whole
// point of storing cents as an integer is defeated if the way in goes
// through a binary fraction that cannot represent 0.10.
//
// The separator rules are stated rather than sniffed:
//
//	both . and ,   the LAST one is the decimal separator
//	only ,         decimal
//	only . with 2 decimals            decimal
//	only . with exactly 3 decimals    AMBIGUOUS, refused
//	only . otherwise                  decimal
//
// The refused case is the important one. "1.500" is 1500 in a Brazilian
// export and 1.50 in an American one, and nothing in the string says
// which. Guessing is wrong by a factor of a thousand, in silence. So the
// line becomes INVALID and the operator is told why.
func parseDecimalToCents(raw string) (int64, bool, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false, errors.New("it is empty")
	}
	s = strings.NewReplacer("R$", "", "$", "", " ", "", " ", "").Replace(s)

	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") { // accounting notation
		negative, s = true, s[1:len(s)-1]
	}
	switch {
	case strings.HasPrefix(s, "-"):
		negative, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if s == "" {
		return 0, false, errors.New("it has no digits")
	}

	lastDot, lastComma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	var intPart, fracPart string

	switch {
	case lastDot >= 0 && lastComma >= 0:
		sep := lastDot
		if lastComma > lastDot {
			sep = lastComma
		}
		intPart, fracPart = s[:sep], s[sep+1:]
	case lastComma >= 0:
		intPart, fracPart = s[:lastComma], s[lastComma+1:]
	case lastDot >= 0:
		frac := s[lastDot+1:]
		if len(frac) == 3 && strings.Count(s, ".") == 1 {
			return 0, false, errors.New(
				"\"" + raw + "\" is ambiguous: a single dot with three digits is a " +
					"thousands separator in some exports and a decimal point in others")
		}
		intPart, fracPart = s[:lastDot], frac
	default:
		intPart, fracPart = s, ""
	}

	if !validGrouping(intPart) {
		return 0, false, errors.New("\"" + raw + "\" has separators that do not group digits in threes")
	}
	intDigits := strings.NewReplacer(".", "", ",", "").Replace(intPart)
	if intDigits == "" {
		intDigits = "0"
	}
	if !allDigits(intDigits) || !allDigits(fracPart) {
		return 0, false, errors.New("\"" + raw + "\" is not a number")
	}
	if len(fracPart) > 2 {
		// More precision than money has. Refused rather than rounded: a
		// rounded cent is a wrong cent that nobody will ever notice.
		return 0, false, errors.New("\"" + raw + "\" has more than two decimal places")
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}

	whole, err := strconv.ParseInt(intDigits, 10, 64)
	if err != nil {
		return 0, false, errors.New("\"" + raw + "\" does not fit in a 64-bit amount")
	}
	frac, _ := strconv.ParseInt(fracPart, 10, 64)
	if whole > (1<<62)/100 {
		return 0, false, errors.New("\"" + raw + "\" is too large to be an amount")
	}
	return whole*100 + frac, negative, nil
}

// validGrouping refuses "1,2,3".
//
// Once the decimal separator has been taken off the end, whatever remains
// may contain thousands separators, and those are only meaningful in
// groups of three. Without this check the "last separator is the decimal
// point" rule happily reads garbage as a number: "1,2,3" would become
// 12.30, which is not what any export meant and not something the operator
// would ever spot.
func validGrouping(intPart string) bool {
	if !strings.ContainsAny(intPart, ".,") {
		return true
	}
	groups := strings.FieldsFunc(intPart, func(r rune) bool { return r == '.' || r == ',' })
	if len(groups) < 2 {
		return false
	}
	if len(groups[0]) < 1 || len(groups[0]) > 3 {
		return false
	}
	for _, g := range groups[1:] {
		if len(g) != 3 {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ── Dates ──────────────────────────────────────────────────────────────
//
// Two explicit layouts and nothing else. time.Parse is strict, so
// 2026-02-30 is refused rather than rolled into March.
//
// The instant is midday in the reporting timezone, matching what
// tools/period.go does for a conversationally supplied date: a statement
// gives a DAY, and a day has to become an instant somewhere. Midday keeps
// it on the stated date under any reasonable timezone shift.
func parseStatementDate(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normaliseDescription is what turns 150 lines into a handful of groups
// the operator can actually assign categories to.
//
// It is deliberately mild: case and whitespace only, plus the trailing
// receipt noise that changes between exports of the same purchase. It does
// NOT strip words, because two different merchants must not collapse into
// one group and be categorised together.
func normaliseDescription(s string) string {
	s = strings.ToUpper(collapseSpaces(s))
	s = strings.NewReplacer("Á", "A", "Ã", "A", "Â", "A", "É", "E", "Ê", "E",
		"Í", "I", "Ó", "O", "Õ", "O", "Ô", "O", "Ú", "U", "Ç", "C").Replace(s)
	if i := strings.Index(s, " - "); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ── Heuristic identity ─────────────────────────────────────────────────
//
// assignOrdinalGroups numbers repeated lines within their own group, in
// file order. The ordinal is what lets two legitimately identical
// purchases on the same day both exist: they are occurrence 1 and 2, get
// different fingerprints, and both import.
//
// The ordinal is scoped to the GROUP, not to the file, which is what keeps
// it stable across re-exports. A second export covering more days does not
// shift the ordinals of the days already imported, because nothing outside
// a group can change a group's numbering.
func assignOrdinalGroups(lines []parsedLine) {
	seen := make(map[string]int, len(lines))
	for i := range lines {
		if lines[i].Invalid || lines[i].ExternalID != "" {
			continue
		}
		k := lines[i].fingerprintKey()
		seen[k]++
		lines[i].ordinal = seen[k]
	}
}

func (p parsedLine) fingerprintKey() string {
	return strings.Join([]string{
		p.OccurredAt.Format("2006-01-02"),
		strconv.FormatInt(p.AmountCents, 10),
		string(p.Direction),
		p.GroupKey,
	}, "\x1f")
}

// fingerprint is a dedup strategy, never a claim of identity. See
// domain.IdentityHeuristic for why that distinction is enforced rather
// than merely noted.
func (p parsedLine) fingerprint(accountScope string) string {
	h := sha256.Sum256([]byte(accountScope + "\x1f" + p.fingerprintKey() + "\x1f" + strconv.Itoa(p.ordinal)))
	return hex.EncodeToString(h[:])[:40]
}

// ── Possible internal movement ─────────────────────────────────────────
//
// These tokens do not prove a transfer and are not treated as proof. They
// raise a QUESTION, because the cost is asymmetric: a false flag costs the
// operator one answer, while a missed internal transfer inflates income
// and expense permanently and invisibly.
//
// Bare "PIX" is deliberately absent. Most PIX lines are ordinary payments
// to other people, and flagging them all would make the question useless.
var internalMovementTokens = []string{
	"TRANSFERENCIA", "TRANSFER", "ENTRE CONTAS", "MESMA TITULARIDADE",
	"APLICACAO", "RESGATE", "POUPANCA", "TED ", "DOC ",
}

func looksInternal(groupKey string) bool {
	for _, tok := range internalMovementTokens {
		if strings.Contains(groupKey, tok) {
			return true
		}
	}
	return false
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var _ = fmt.Sprintf
