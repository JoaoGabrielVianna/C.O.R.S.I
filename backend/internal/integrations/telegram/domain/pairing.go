package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"time"

	"github.com/google/uuid"
)

/* ── the pairing code ────────────────────────────────────────────────── */

// PairingTTL is how long a code lives.
//
// Ten minutes because the flow it serves is one person walking from their
// phone to their terminal. Long enough to not be a race against yourself,
// short enough that a code read over a shoulder or left in a screenshot
// is worthless by the time anybody acts on it.
const PairingTTL = 10 * time.Minute

// codeAlphabet is Crockford-style: no I, L, O, U, and no digits that look
// like them. The code is TYPED BY A HUMAN, from a phone screen into a
// terminal, and a character set that admits `0`/`O` turns an unguessable
// secret into a guessing game about the font.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// codeLength is 10 characters over a 30-character alphabet.
//
// ── The entropy, stated rather than assumed ────────────────────────────
// 30^10 ≈ 5.9e14, a little over 49 bits. Against a ten-minute window and
// a redemption path that is a local CLI — not a network endpoint anybody
// can hammer — that is far beyond what an attacker could enumerate. The
// number is chosen to be typed, not to be maximal: doubling it would buy
// entropy nobody can attack and cost an operator a transcription error.
const codeLength = 10

// PairingCode is an issued, not-yet-redeemed admission.
//
// The plaintext code is NOT a field. It exists in the return value of
// NewPairingCode, travels to exactly one Telegram message, and is never
// seen by this struct, by the repository, or by the database. See the
// schema header.
type PairingCode struct {
	ID             uuid.UUID
	CodeHash       []byte
	TelegramUserID int64
	TelegramChatID int64
	ExpiresAt      time.Time
	ConsumedAt     *time.Time
	CreatedAt      time.Time
}

// NewPairingCode mints a code for one Telegram identity.
//
// It returns the plaintext separately from the record on purpose: the
// caller has to decide, explicitly, to send it somewhere. A struct with a
// `Code` field would eventually be logged.
func NewPairingCode(telegramUserID, telegramChatID int64, now time.Time) (plaintext string, rec *PairingCode, err error) {
	plaintext, err = randomCode()
	if err != nil {
		return "", nil, err
	}
	return plaintext, &PairingCode{
		ID:             uuid.New(),
		CodeHash:       HashCode(plaintext),
		TelegramUserID: telegramUserID,
		TelegramChatID: telegramChatID,
		ExpiresAt:      now.Add(PairingTTL),
		CreatedAt:      now,
	}, nil
}

// HashCode is the one-way function storage holds.
//
// Normalisation is part of it, and has to be: the operator retypes this
// code, and "the code is right but you typed it in lower case" is not a
// failure mode worth shipping. Case folding and separator stripping happen
// HERE, once, so the issued code and the redeemed code are folded by
// identical rules — the classic way this breaks is two call sites
// normalising almost the same way.
func HashCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormalizeCode(code)))
	return sum[:]
}

// NormalizeCode folds a typed code to its canonical form.
func NormalizeCode(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		// Spaces and dashes are what a person adds to make a long code
		// readable. They are not part of the secret.
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// EqualCode compares in constant time.
//
// The timing channel here is thin to the point of theoretical — the
// comparison is against a hash, the lookup is by that hash, and the caller
// is a local CLI. It is constant time anyway because a secret comparison
// written the obvious way is a thing somebody copies into a context where
// it does matter.
func EqualCode(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// Redeemable reports whether this code may still be turned into a binding.
func (p *PairingCode) Redeemable(now time.Time) bool {
	return p != nil && p.ConsumedAt == nil && now.Before(p.ExpiresAt)
}

// Format inserts a separator so a ten-character code can be read off a
// phone without losing your place. NormalizeCode removes it again, so the
// displayed form and the stored form agree by construction.
func Format(code string) string {
	if len(code) != codeLength {
		return code
	}
	return code[:5] + "-" + code[5:]
}

// randomCode draws from crypto/rand with no modulo bias: the alphabet
// length divides 240 evenly (30 × 8), so bytes at or above 240 are
// redrawn rather than folded.
func randomCode() (string, error) {
	const limit = 240
	out := make([]byte, 0, codeLength)
	buf := make([]byte, codeLength*2)
	for len(out) < codeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= limit {
				continue
			}
			out = append(out, codeAlphabet[int(b)%len(codeAlphabet)])
			if len(out) == codeLength {
				break
			}
		}
	}
	return string(out), nil
}
