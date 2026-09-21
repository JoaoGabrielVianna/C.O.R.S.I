package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestAPairingCodeIsUnguessable.
//
// Not a statistical proof — that is what the alphabet and the length are
// for — but the cheap check that catches the two ways this actually
// breaks: a generator seeded from the clock, and a generator that returns
// the same value because somebody replaced crypto/rand with math/rand.
func TestAPairingCodeIsUnguessable(t *testing.T) {
	const n = 2000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		code, _, err := NewPairingCode(1, 1, time.Now())
		if err != nil {
			t.Fatalf("NewPairingCode: %v", err)
		}
		if len(code) != codeLength {
			t.Fatalf("code %q has length %d, want %d", code, len(code), codeLength)
		}
		for _, r := range code {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q contains %q, which is not in the alphabet", code, r)
			}
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("collision after %d draws: %q", i, code)
		}
		seen[code] = struct{}{}
	}
}

// TestTheCodeItselfIsNeverInTheRecord.
//
// ══════════════════════════════════════════════════════════════════════
//
// The record goes to Postgres, to backups, and to whatever reads them. A
// plaintext code in any field of it would be a live admission ticket
// sitting in a dump.
func TestTheCodeItselfIsNeverInTheRecord(t *testing.T) {
	code, rec, err := NewPairingCode(42, 42, time.Now())
	if err != nil {
		t.Fatalf("NewPairingCode: %v", err)
	}
	if bytes.Contains(rec.CodeHash, []byte(code)) {
		t.Fatal("the plaintext code appears inside the stored hash")
	}
	if len(rec.CodeHash) != 32 {
		t.Fatalf("hash is %d bytes, want 32 (the column CHECKs this)", len(rec.CodeHash))
	}
	// The hash must be the one a redemption will compute from what the
	// operator types, including the separator the bot displays.
	if !EqualCode(rec.CodeHash, HashCode(Format(code))) {
		t.Fatal("the displayed form does not hash to the stored hash; the operator could never redeem it")
	}
}

// TestRetypingIsForgivingInExactlyTheWaysItShould Be.
//
// The operator reads this off a phone and types it into a terminal. Case
// and separators are transcription noise, not secret material; anything
// else is a different code and must stay one.
func TestRetypingIsForgiving(t *testing.T) {
	same := []string{"ABCDE-FGHJK", "abcde-fghjk", "ABCDEFGHJK", " abcde fghjk ", "ABCDE_FGHJK"}
	want := HashCode(same[0])
	for _, v := range same[1:] {
		if !EqualCode(HashCode(v), want) {
			t.Fatalf("%q should normalise to the same code as %q", v, same[0])
		}
	}
	for _, v := range []string{"ABCDE-FGHJM", "ABCDE-FGHJ", "ABCDE-FGHJKL"} {
		if EqualCode(HashCode(v), want) {
			t.Fatalf("%q must NOT be accepted as %q", v, same[0])
		}
	}
}

// TestAmbiguousCharactersAreNotInTheAlphabet.
//
// `0` against `O` and `1` against `I`/`L` is the difference between a
// pairing that works and an operator who retypes a correct code three
// times and concludes the feature is broken.
func TestAmbiguousCharactersAreNotInTheAlphabet(t *testing.T) {
	for _, r := range "01ILOU" {
		if strings.ContainsRune(codeAlphabet, r) {
			t.Fatalf("the alphabet contains the ambiguous character %q", r)
		}
	}
}

// TestACodeExpiresAndIsSingleUse — the two properties the schema enforces
// in SQL, asserted here on the value so the rule is also readable from the
// domain.
func TestACodeExpiresAndIsSingleUse(t *testing.T) {
	now := time.Now()
	_, rec, err := NewPairingCode(7, 7, now)
	if err != nil {
		t.Fatalf("NewPairingCode: %v", err)
	}
	if !rec.Redeemable(now.Add(time.Minute)) {
		t.Fatal("a fresh code should be redeemable inside its ttl")
	}
	if rec.Redeemable(now.Add(PairingTTL + time.Second)) {
		t.Fatal("an expired code must not be redeemable")
	}
	consumed := now
	rec.ConsumedAt = &consumed
	if rec.Redeemable(now.Add(time.Minute)) {
		t.Fatal("a consumed code must not be redeemable a second time")
	}
}

// TestAuthorizesNeedsBothIds.
//
// The user id is the half that matters: it is what turns "a message
// arrived in a chat we trust" into "the person we trust sent it".
func TestAuthorizesNeedsBothIds(t *testing.T) {
	b := &Binding{TelegramUserID: 100, TelegramChatID: 100}
	if !b.Authorizes(100, 100) {
		t.Fatal("the bound identity must be authorized")
	}
	if b.Authorizes(999, 100) {
		t.Fatal("a different telegram USER in the bound chat must be refused")
	}
	if b.Authorizes(100, 999) {
		t.Fatal("the bound user in a different CHAT must be refused")
	}
	revoked := time.Now()
	b.RevokedAt = &revoked
	if b.Authorizes(100, 100) {
		t.Fatal("a revoked binding must authorize nobody")
	}
	var nilBinding *Binding
	if nilBinding.Authorizes(100, 100) {
		t.Fatal("no binding must authorize nobody, without panicking")
	}
}
