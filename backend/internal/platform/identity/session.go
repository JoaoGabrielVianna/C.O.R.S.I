package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

// sessionTokenBytes is the entropy in a session token. 32 bytes is 256
// bits, which is not a number anybody guesses.
const sessionTokenBytes = 32

// Session is a live login.
//
// It carries no profile and no roles, because there is exactly one subject
// and no authorization decision downstream reads anything but "is there a
// session". A field nobody reads is a field that grows a consumer later.
type Session struct {
	Subject   string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Store persists sessions.
//
// ── Why the port takes a DIGEST and never a token ──────────────────────
// Every method here is spelled in terms of `tokenHash`, so the raw token
// physically cannot reach a query, a query log or a database dump. A store
// that took the token and hashed it internally would work identically and
// would put the bearer credential one refactor away from `INSERT`.
type Store interface {
	Create(ctx context.Context, tokenHash []byte, s Session) error
	// Lookup returns the session for a digest, or ok=false when there is
	// none or it has expired. An expired row is not an error: it is the
	// same answer as no row, and the caller must not be able to tell them
	// apart.
	Lookup(ctx context.Context, tokenHash []byte, now time.Time) (Session, bool, error)
	Delete(ctx context.Context, tokenHash []byte) error
	// DeleteExpired reclaims rows whose expiry has passed. Correctness does
	// not depend on it — Lookup already refuses them — so it is called
	// opportunistically and its failure is never fatal to a request.
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// newSessionToken returns a fresh token and the digest to store for it.
//
// The token is base64url so it survives a Set-Cookie header untouched; the
// digest is a bare SHA-256 with no salt and no stretching, which is correct
// here and would be wrong for a password: the input already has 256 bits of
// uniform entropy, so there is no dictionary to precompute and nothing for
// a work factor to buy.
func newSessionToken() (token string, digest []byte, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("identity: session token: %w", err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// digestOf is newSessionToken's other half, for a token arriving in a
// cookie.
func digestOf(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != sessionTokenBytes {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}
