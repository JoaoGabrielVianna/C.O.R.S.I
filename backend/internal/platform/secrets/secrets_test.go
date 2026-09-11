package secrets

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(t *testing.T) Config {
	t.Helper()
	return Config{Key: base64.StdEncoding.EncodeToString(make([]byte, keyBytes))}
}

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := New(testKey(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	const token = "sk-litellm-abcdef123456"
	blob, err := s.Seal(token)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(string(blob), token) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := s.Open(blob)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != token {
		t.Fatalf("round trip = %q, want %q", got, token)
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	s, _ := New(testKey(t))
	a, _ := s.Seal("same")
	b, _ := s.Seal("same")
	if string(a) == string(b) {
		t.Fatal("two seals of the same plaintext produced identical blobs; nonce is not random")
	}
}

func TestOpenRejectsTamperedBlob(t *testing.T) {
	s, _ := New(testKey(t))
	blob, _ := s.Seal("sk-tamper")
	blob[len(blob)-1] ^= 0xff
	if _, err := s.Open(blob); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open tampered = %v, want ErrCorrupt", err)
	}
}

func TestOpenRejectsShortBlob(t *testing.T) {
	s, _ := New(testKey(t))
	if _, err := s.Open([]byte{1, 2, 3}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open short = %v, want ErrCorrupt", err)
	}
}

func TestDisabledSealer(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatalf("new with blank key should not error, got %v", err)
	}
	if s.Enabled() {
		t.Fatal("blank key should yield a disabled sealer")
	}
	if _, err := s.Seal("x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("seal = %v, want ErrNotConfigured", err)
	}
	if _, err := s.Open([]byte("x")); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("open = %v, want ErrNotConfigured", err)
	}
}

func TestNewRejectsMalformedKey(t *testing.T) {
	if _, err := New(Config{Key: "not base64!!"}); err == nil {
		t.Fatal("want error for non-base64 key")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := New(Config{Key: short}); err == nil {
		t.Fatal("want error for 16-byte key")
	}
}

func TestHint(t *testing.T) {
	if got := Hint("sk-abcdef123456"); got != "...3456" {
		t.Fatalf("hint = %q", got)
	}
	if got := Hint("ab"); got != "****" {
		t.Fatalf("short hint = %q", got)
	}
}
