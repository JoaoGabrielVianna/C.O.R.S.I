package app

import (
	"strings"
	"testing"
)

// Money is the one thing this parser is not allowed to get subtly wrong,
// so the table is exhaustive about the shapes a real export produces and
// explicit about the shape it must REFUSE.
func TestParseDecimalToCents(t *testing.T) {
	cases := []struct {
		in    string
		cents int64
		neg   bool
		bad   bool
	}{
		{in: "10.00", cents: 1000},
		{in: "10,00", cents: 1000},
		{in: "-89.90", cents: 8990, neg: true},
		{in: "(89,90)", cents: 8990, neg: true},
		{in: "R$ 1.234,56", cents: 123456},
		{in: "1,234.56", cents: 123456},
		{in: "0,01", cents: 1},
		{in: "8000", cents: 800000},
		{in: "19.99", cents: 1999},
		{in: "+45,50", cents: 4550},
		{in: "1.5", cents: 150},
		// The refusal that matters: 1.500 is 1500 in one export and 1.50 in
		// another, and guessing is wrong by a thousand in silence.
		{in: "1.500", bad: true},
		{in: "10.001", bad: true},
		{in: "abc", bad: true},
		{in: "", bad: true},
		{in: "1,2,3", bad: true},
	}
	for _, c := range cases {
		got, neg, err := parseDecimalToCents(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q: want refusal, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.cents || neg != c.neg {
			t.Errorf("%q = %d neg=%v, want %d neg=%v", c.in, got, neg, c.cents, c.neg)
		}
	}
}

// A rounded cent is a wrong cent nobody notices, so three decimals are
// refused rather than truncated.
func TestThreeDecimalsAreRefusedNotRounded(t *testing.T) {
	if _, _, err := parseDecimalToCents("10,005"); err == nil {
		t.Fatal("10,005 must be refused; rounding it would invent a cent")
	}
}

func TestParseStatementDateIsStrict(t *testing.T) {
	if _, ok := parseStatementDate("2026-02-30"); ok {
		t.Error("2026-02-30 is not a date and must not roll into March")
	}
	if _, ok := parseStatementDate("31/08/2026"); !ok {
		t.Error("DD/MM/YYYY must parse")
	}
	if _, ok := parseStatementDate("08/31/2026"); ok {
		t.Error("MM/DD/YYYY must not be silently accepted as DD/MM")
	}
}

// Two identical purchases on the same day are two purchases. If the
// fingerprint could not tell them apart, one would be discarded as a
// duplicate and the operator would never learn a real purchase is missing.
func TestIdenticalLinesGetDistinctFingerprints(t *testing.T) {
	csv := "date,description,amount\n" +
		"2026-08-10,CAFE DA ESQUINA,-8.50\n" +
		"2026-08-10,CAFE DA ESQUINA,-8.50\n"
	lines, err := parseCSV(csv)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines", len(lines))
	}
	a, b := lines[0].fingerprint("acct"), lines[1].fingerprint("acct")
	if a == b {
		t.Fatal("two identical purchases collapsed into one fingerprint")
	}
	if lines[0].ordinal != 1 || lines[1].ordinal != 2 {
		t.Fatalf("ordinals = %d,%d", lines[0].ordinal, lines[1].ordinal)
	}
}

// Re-exporting the same statement must reproduce the same fingerprints, or
// nothing about idempotence holds.
func TestFingerprintIsStableAcrossReparse(t *testing.T) {
	csv := "date,description,amount\n" +
		"2026-08-10,MERCADO,-50.00\n" +
		"2026-08-11,PADARIA,-12.30\n" +
		"2026-08-10,MERCADO,-50.00\n"
	first, _ := parseCSV(csv)
	second, _ := parseCSV(csv)
	for i := range first {
		if first[i].fingerprint("acct") != second[i].fingerprint("acct") {
			t.Fatalf("line %d fingerprint is not deterministic", i)
		}
	}
}

// A wider re-export must not renumber the groups already imported: the
// ordinal is scoped to its group, not to a position in the file.
func TestOrdinalsSurviveAWiderReexport(t *testing.T) {
	narrow, _ := parseCSV("date,description,amount\n2026-08-10,MERCADO,-50.00\n")
	wide, _ := parseCSV("date,description,amount\n" +
		"2026-08-01,POSTO,-200.00\n" +
		"2026-08-05,FARMACIA,-31.00\n" +
		"2026-08-10,MERCADO,-50.00\n")
	if narrow[0].fingerprint("acct") != wide[2].fingerprint("acct") {
		t.Fatal("the same purchase changed fingerprint because the file grew")
	}
}

func TestHeaderIsMatchedByNameNotPosition(t *testing.T) {
	a, err := parseCSV("date,description,amount\n2026-08-10,MERCADO,-50.00\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseCSV("valor,data,historico\n-50.00,2026-08-10,MERCADO\n")
	if err != nil {
		t.Fatal(err)
	}
	if a[0].AmountCents != b[0].AmountCents || a[0].Description != b[0].Description {
		t.Fatalf("column order changed the meaning: %+v vs %+v", a[0], b[0])
	}
}

func TestUnreadableLineIsInvalidNotAGuess(t *testing.T) {
	lines, err := parseCSV("date,description,amount\n" +
		"2026-08-10,MERCADO,-50.00\n" +
		"nao-e-data,LIXO,abc\n")
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].Invalid {
		t.Error("the good line must survive a bad neighbour")
	}
	if !lines[1].Invalid || lines[1].Reason == "" {
		t.Error("the bad line must be invalid and say why")
	}
}

func TestMissingHeaderIsRefused(t *testing.T) {
	if _, err := parseCSV("2026-08-10,MERCADO,-50.00\n"); err == nil {
		t.Fatal("a statement with no header must be refused, not positionally guessed")
	}
}

func TestLooksInternalIsConservativeAboutPix(t *testing.T) {
	if looksInternal(normaliseDescription("PIX ENVIADO MARIA")) {
		t.Error("a bare PIX is an ordinary payment; flagging every one makes the question useless")
	}
	if !looksInternal(normaliseDescription("TRANSFERENCIA ENTRE CONTAS")) {
		t.Error("an explicit account-to-account movement must raise the question")
	}
}

func TestDirectionComesFromTheSign(t *testing.T) {
	lines, _ := parseCSV("date,description,amount\n" +
		"2026-08-05,SALARIO,8000.00\n" +
		"2026-08-06,MERCADO,-50.00\n")
	if string(*&lines[0].Direction) != "income" {
		t.Errorf("positive amount = %s, want income", lines[0].Direction)
	}
	if string(lines[1].Direction) != "expense" {
		t.Errorf("negative amount = %s, want expense", lines[1].Direction)
	}
}

func TestStrongIdentityIsTakenFromTheFileWhenPresent(t *testing.T) {
	lines, _ := parseCSV("date,description,amount,fitid\n" +
		"2026-08-10,MERCADO,-50.00,ABC123\n")
	if lines[0].ExternalID != "ABC123" {
		t.Fatalf("external id = %q", lines[0].ExternalID)
	}
	if lines[0].ordinal != 0 {
		t.Error("a strong row needs no ordinal: it is not deduped heuristically")
	}
}

func TestDescriptionOnlyIsNeverTheIdentity(t *testing.T) {
	// Same description, different amounts, must not share a fingerprint.
	lines, _ := parseCSV("date,description,amount\n" +
		"2026-08-10,MERCADO,-50.00\n" +
		"2026-08-10,MERCADO,-51.00\n")
	if lines[0].fingerprint("a") == lines[1].fingerprint("a") {
		t.Fatal("fingerprint ignored the amount")
	}
	if !strings.Contains(lines[0].GroupKey, "MERCADO") {
		t.Fatalf("group key = %q", lines[0].GroupKey)
	}
}
