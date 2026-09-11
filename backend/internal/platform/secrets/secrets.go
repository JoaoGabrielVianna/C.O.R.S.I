// Package secrets seals and opens small secret values — API tokens and the
// like — so they can sit in Postgres without being readable from a database
// dump, a backup file, or a stray `SELECT *`.
//
// Construction is AES-256-GCM with a random 96-bit nonce prepended to the
// ciphertext. GCM is authenticated: a tampered blob fails to open rather
// than decrypting to garbage. The nonce is generated per Seal call, so
// sealing the same token twice produces different ciphertext.
//
// The key comes from `SECRETS_KEY` as 32 base64 bytes:
//
//	openssl rand -base64 32
//
// When the variable is unset the package still constructs — it returns a
// Sealer whose every operation fails with ErrNotConfigured. That keeps a
// backend that has never used secrets booting exactly as before, and turns
// a missing key into one clear error at the moment someone tries to save a
// credential, instead of a crash at startup.
//
// Rotating the key invalidates every stored blob. There is no key-id header
// and no envelope re-wrap: rotation means re-entering the credentials.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrNotConfigured is returned by every Sealer operation when no key was
// supplied. Callers should surface it as a configuration problem, not as a
// generic internal error — the fix is an env var, not a retry.
var ErrNotConfigured = errors.New("secrets: SECRETS_KEY not configured")

// ErrCorrupt means the blob could not be authenticated: wrong key, or the
// stored bytes were altered.
var ErrCorrupt = errors.New("secrets: ciphertext could not be opened")

const keyBytes = 32 // AES-256

type Config struct {
	// Key is base64-encoded 32 bytes. Empty disables sealing.
	Key string `env:"SECRETS_KEY"`
}

// Sealer seals and opens secret values. The zero value is not usable —
// build one with New.
type Sealer struct {
	aead cipher.AEAD // nil when unconfigured
}

// New builds a Sealer from config. A blank key yields a disabled Sealer
// (every call returns ErrNotConfigured) rather than an error, so the host
// binary boots without the variable set. A malformed key is a real error:
// it means someone tried to configure this and got it wrong.
func New(cfg Config) (*Sealer, error) {
	if cfg.Key == "" {
		return &Sealer{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("secrets: SECRETS_KEY is not valid base64: %w", err)
	}
	if len(raw) != keyBytes {
		return nil, fmt.Errorf("secrets: SECRETS_KEY must decode to %d bytes, got %d", keyBytes, len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("secrets: build cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: build gcm: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Enabled reports whether a key was configured. Use it to fail a request
// early with a helpful message instead of attempting a doomed Seal.
func (s *Sealer) Enabled() bool { return s != nil && s.aead != nil }

// Seal encrypts plaintext, returning nonce||ciphertext||tag.
func (s *Sealer) Seal(plaintext string) ([]byte, error) {
	if !s.Enabled() {
		return nil, ErrNotConfigured
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: read nonce: %w", err)
	}
	// Passing nonce as the dst prefix makes the nonce the blob's own header,
	// so Open needs nothing but the stored bytes.
	return s.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open reverses Seal.
func (s *Sealer) Open(blob []byte) (string, error) {
	if !s.Enabled() {
		return "", ErrNotConfigured
	}
	ns := s.aead.NonceSize()
	if len(blob) < ns {
		return "", ErrCorrupt
	}
	plaintext, err := s.aead.Open(nil, blob[:ns], blob[ns:], nil)
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plaintext), nil
}

// Hint returns the trailing characters of a secret, for display only. It is
// deliberately short — enough to tell two keys apart, useless on its own.
func Hint(secret string) string {
	const n = 4
	if len(secret) <= n {
		return "****"
	}
	return "..." + secret[len(secret)-n:]
}
