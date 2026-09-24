package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ── Why a verifier in the environment, and never a password ────────────
//
// The operator supplies `AUTH_PASSWORD_HASH`, an argon2id PHC string, and
// never supplies the password itself. The distinction is the whole point:
// an env var is readable by anything that can inspect the process or the
// orchestrator's stored spec, and a password read there is a password that
// works everywhere its owner reused it. A verifier read there costs an
// attacker an offline argon2id campaign against a single target.
//
// Nothing in this process ever holds the plaintext for longer than the
// login request that carried it, and nothing writes it anywhere.

// ErrMalformedHash reports a verifier this package cannot read.
//
// It fails the BOOT rather than the login. A deployment whose verifier is
// unparseable authenticates nobody, and discovering that at the first login
// attempt — as a generic "invalid credentials" — would be indistinguishable
// from a typo in the password.
var ErrMalformedHash = errors.New("identity: AUTH_PASSWORD_HASH is not a valid argon2id PHC string")

// argon2idParams are the cost parameters this package writes when it
// generates a verifier. Verification reads whatever the stored string
// declares, so raising these later does not invalidate an existing hash.
//
// 64 MiB / 3 passes / 2 lanes is the OWASP argon2id baseline. On the 2-vCPU
// box this runs on it costs ~90ms per attempt, which is the point: it is
// the floor under every guess, and the login endpoint is rate-limited on
// top of it.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonLanes     = 2
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// HashPassword produces a PHC-formatted argon2id verifier.
//
// Used by cmd/corsi-passwd, which runs on the operator's own machine. The
// server never calls it: it has no reason to see a plaintext password
// except to check one.
func HashPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("identity: refusing to hash an empty password")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity: salt: %w", err)
	}
	sum := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemoryKiB, argonLanes, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonLanes,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// verifier is a parsed AUTH_PASSWORD_HASH, kept in memory for the process's
// lifetime so every login pays the argon2 cost and not the parse cost.
type verifier struct {
	memory, time uint32
	lanes        uint8
	salt, digest []byte
}

func parseVerifier(phc string) (verifier, error) {
	parts := strings.Split(phc, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, digest
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return verifier{}, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return verifier{}, ErrMalformedHash
	}
	var v verifier
	var lanes int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &v.memory, &v.time, &lanes); err != nil {
		return verifier{}, ErrMalformedHash
	}
	if lanes < 1 || lanes > 255 {
		return verifier{}, ErrMalformedHash
	}
	v.lanes = uint8(lanes)

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return verifier{}, ErrMalformedHash
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(digest) == 0 {
		return verifier{}, ErrMalformedHash
	}
	v.salt, v.digest = salt, digest
	return v, nil
}

// matches reports whether plaintext produces this verifier's digest.
//
// The comparison is constant-time. The argon2 call above it is not
// data-dependent either: cost comes from the STORED parameters, so a wrong
// password costs exactly what a right one costs.
func (v verifier) matches(plaintext string) bool {
	sum := argon2.IDKey([]byte(plaintext), v.salt, v.time, v.memory, v.lanes, uint32(len(v.digest)))
	return subtle.ConstantTimeCompare(sum, v.digest) == 1
}
