package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Meta's `signed_request`: the envelope its platform callbacks arrive in.
//
// ── What this is for ───────────────────────────────────────────────────
// Two callbacks Meta pings without any session of ours — deauthorization
// and data deletion — carry no header we issued and no credential we can
// check. The ONLY thing that makes them trustworthy is that Meta signed
// the body with the app secret, which only Meta and this deployment hold.
//
// So this file is the entire authentication of those two endpoints. If it
// is wrong, anybody who learns the public URL can delete a connection by
// posting a user id.
//
// ── The format, as Meta documents it ───────────────────────────────────
//
//	signed_request = base64url(signature) "." base64url(payload)
//	signature      = HMAC-SHA256(payload_as_received, app_secret)
//	payload        = JSON { "algorithm": "HMAC-SHA256", "user_id": …, … }
//
// Two details in there are easy to get wrong and both are load-bearing:
//
//   - The signature covers the payload STRING EXACTLY AS RECEIVED, not the
//     decoded JSON. Re-encoding it before checking would change the bytes
//     and every valid request would fail.
//   - The encoding is base64URL (`-` and `_`), and Meta sends it unpadded.

// SignedRequest is the verified content of one platform callback.
//
// It carries only what this integration acts on. `algorithm` is checked and
// discarded; the other documented fields (`issued_at`, `expires`,
// `oauth_token`) are not read, because acting on them would mean this
// integration doing something a callback never asked for.
type SignedRequest struct {
	// UserID is the APP-SCOPED user id — the same id space `GET /me`
	// returns and this integration stores as Connection.AccountID. It is
	// what makes a callback resolvable to a stored row without the caller
	// naming a workspace.
	UserID string `json:"user_id"`
	// Algorithm is Meta's declaration of how it signed. Verified rather
	// than trusted: a payload claiming a weaker algorithm must not be able
	// to talk this code into accepting one.
	Algorithm string `json:"algorithm"`
	IssuedAt  int64  `json:"issued_at"`
}

// expectedAlgorithm is the only one this build accepts.
//
// Pinned, not read from the payload. The classic failure of signature
// envelopes is honouring an attacker-chosen `alg`; here the field is
// compared against this constant and anything else is refused, so a
// `"algorithm": "none"` payload is rejected before its signature matters.
const expectedAlgorithm = "HMAC-SHA256"

// maxSignedRequestBytes bounds what will be parsed.
//
// A platform callback carries a handful of short fields. Anything larger is
// not a callback, and decoding it would spend memory on the strength of a
// promise the caller never made.
const maxSignedRequestBytes = 8 << 10

// ParseSignedRequest verifies and decodes one `signed_request`.
//
// ── Why the secret is a parameter and not a package variable ───────────
// So that a deployment with no app secret configured CANNOT reach a code
// path that skips verification. There is no "unverified" mode and no
// boolean to pass: the only way to obtain a SignedRequest is to supply the
// secret that proves the request came from Meta.
//
// An empty secret is refused outright rather than treated as "no checking",
// because an HMAC with an empty key is a real HMAC that an attacker can
// also compute.
func ParseSignedRequest(raw, appSecret string) (SignedRequest, error) {
	if strings.TrimSpace(appSecret) == "" {
		return SignedRequest{}, Invalid(
			"this deployment has no Meta app secret, so a platform callback cannot be verified")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SignedRequest{}, Invalid("signed_request is required")
	}
	if len(raw) > maxSignedRequestBytes {
		return SignedRequest{}, Invalid("signed_request is larger than a platform callback may be")
	}

	encodedSig, payload, found := strings.Cut(raw, ".")
	if !found || encodedSig == "" || payload == "" {
		return SignedRequest{}, Invalid("signed_request is not in the form signature.payload")
	}

	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encodedSig, "="))
	if err != nil {
		return SignedRequest{}, Invalid("signed_request signature is not base64url")
	}

	// Signed over the payload EXACTLY as it arrived. Decoding first and
	// re-encoding would change the bytes and break every genuine request.
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(payload))
	// Constant time: a byte-by-byte comparison leaks how much of a forged
	// signature was correct, which is enough to build the rest of it.
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return SignedRequest{}, Invalid("signed_request signature does not match this app secret")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return SignedRequest{}, Invalid("signed_request payload is not base64url")
	}

	var out SignedRequest
	if err := json.Unmarshal(decoded, &out); err != nil {
		return SignedRequest{}, Invalid("signed_request payload is not JSON")
	}
	if out.Algorithm != expectedAlgorithm {
		return SignedRequest{}, Invalid("signed_request declares algorithm %q, which this build does not accept", out.Algorithm)
	}
	if strings.TrimSpace(out.UserID) == "" {
		// Without it the callback names nobody, and acting on it would mean
		// choosing a row to delete on no evidence at all.
		return SignedRequest{}, Invalid("signed_request carries no user_id, so it identifies no account")
	}
	return out, nil
}
