package tools

import "testing"

// The money boundary has exactly one job: an amount must arrive at the
// domain as the integer it was sent as, and must be rendered back from THAT
// integer and no other.
//
// These are unit tests because the property is arithmetic. The other half —
// that the integer the tool received is the integer Postgres stored — is
// asserted against a real database in the integration suite, by reading the
// column back in SQL.

func TestFormatCentsRendersTheStoredInteger(t *testing.T) {
	cases := []struct {
		cents int64
		want  string
	}{
		{0, "R$ 0,00"},
		{1, "R$ 0,01"},
		{9, "R$ 0,09"},
		{99, "R$ 0,99"},
		{100, "R$ 1,00"},
		// The case the whole file exists for: eighty-nine reais and ninety
		// centavos is 8990, is not 89, and is not 899.
		{8990, "R$ 89,90"},
		{9000, "R$ 90,00"},
		{89, "R$ 0,89"},
		{899, "R$ 8,99"},
		{125000, "R$ 1.250,00"},
		{1100000, "R$ 11.000,00"},
		{123456789, "R$ 1.234.567,89"},
		{100000000000, "R$ 1.000.000.000,00"},
		// Negative appears only in a computed net, never in a stored row.
		{-8990, "R$ -89,90"},
		{-1, "R$ -0,01"},
	}
	for _, c := range cases {
		if got := formatCents(c.cents); got != c.want {
			t.Errorf("formatCents(%d) = %q, want %q", c.cents, got, c.want)
		}
	}
}

// amountFields is the only way a result states an amount, so the two fields
// it writes must always describe the same number.
func TestAmountFieldsAgreeWithEachOther(t *testing.T) {
	for _, cents := range []int64{0, 1, 8990, 9000, 125000, -7} {
		out := map[string]any{}
		amountFields(out, "amount", cents)
		if out["amount_cents"] != cents {
			t.Fatalf("amount_cents = %v, want %d", out["amount_cents"], cents)
		}
		if out["amount"] != formatCents(cents) {
			t.Fatalf("amount = %v, want %q", out["amount"], formatCents(cents))
		}
	}
}
