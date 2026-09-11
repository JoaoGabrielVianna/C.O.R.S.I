package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

/*
The authentication of the only unauthenticated surface in this integration.

Meta's platform callbacks arrive with no session, no workspace header and
no credential of ours. The HMAC signature is the ENTIRE reason they can be
trusted, so every case below is a way somebody could try to delete a
connection by posting a user id at a public URL.
*/

const testSecret = "meta-app-secret-not-real"

// sign builds a genuine signed_request the way Meta does.
func sign(t *testing.T, payloadJSON, secret string) string {
	t.Helper()
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sig + "." + payload
}

const goodPayload = `{"algorithm":"HMAC-SHA256","user_id":"17841400000000000","issued_at":1756000000}`

func TestAGenuineSignedRequestIsAccepted(t *testing.T) {
	got, err := ParseSignedRequest(sign(t, goodPayload, testSecret), testSecret)
	if err != nil {
		t.Fatalf("a correctly signed request was refused: %v", err)
	}
	if got.UserID != "17841400000000000" {
		t.Errorf("user_id = %q", got.UserID)
	}
	if got.IssuedAt != 1756000000 {
		t.Errorf("issued_at = %d", got.IssuedAt)
	}
}

// THE test. A payload signed with any other key must not be honoured, or
// the public callback becomes "delete whoever this body names".
func TestARequestSignedWithAnotherSecretIsRefused(t *testing.T) {
	forged := sign(t, goodPayload, "attacker-guessed-this")
	if _, err := ParseSignedRequest(forged, testSecret); err == nil {
		t.Fatal("a request signed with the wrong secret was accepted")
	}
}

// Flipping a byte of the payload must invalidate the signature: otherwise
// a genuine callback for one account could be edited into one for another.
func TestATamperedPayloadIsRefused(t *testing.T) {
	genuine := sign(t, goodPayload, testSecret)
	sig, payload, _ := strings.Cut(genuine, ".")

	other := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"algorithm":"HMAC-SHA256","user_id":"99999999999999999","issued_at":1756000000}`))
	if _, err := ParseSignedRequest(sig+"."+other, testSecret); err == nil {
		t.Fatal("a payload swapped under a genuine signature was accepted")
	}
	// And the untouched pair still works, so the check above is not just
	// "everything fails".
	if _, err := ParseSignedRequest(sig+"."+payload, testSecret); err != nil {
		t.Fatalf("the genuine pair stopped working: %v", err)
	}
}

// The classic signature-envelope hole: honouring an attacker-chosen
// algorithm. The field is compared against a pinned constant.
func TestADowngradedAlgorithmIsRefused(t *testing.T) {
	for _, alg := range []string{"none", "HMAC-SHA1", "", "hmac-sha256"} {
		payload := `{"algorithm":"` + alg + `","user_id":"17841400000000000"}`
		// Correctly signed — only the declared algorithm is wrong.
		if _, err := ParseSignedRequest(sign(t, payload, testSecret), testSecret); err == nil {
			t.Errorf("algorithm %q was accepted", alg)
		}
	}
}

// A callback that names nobody must not be able to reach a delete: with no
// user id there is no evidence for choosing any row.
func TestAPayloadWithNoUserIDIsRefused(t *testing.T) {
	for _, payload := range []string{
		`{"algorithm":"HMAC-SHA256"}`,
		`{"algorithm":"HMAC-SHA256","user_id":""}`,
		`{"algorithm":"HMAC-SHA256","user_id":"   "}`,
	} {
		if _, err := ParseSignedRequest(sign(t, payload, testSecret), testSecret); err == nil {
			t.Errorf("a payload with no user_id was accepted: %s", payload)
		}
	}
}

// An unconfigured deployment cannot verify anything, so it must refuse
// rather than fall back to an empty key — which is a real HMAC an attacker
// can also compute.
func TestAnEmptyAppSecretRefusesEverything(t *testing.T) {
	withEmptyKey := sign(t, goodPayload, "")
	if _, err := ParseSignedRequest(withEmptyKey, ""); err == nil {
		t.Fatal("an empty app secret verified a request")
	}
	// Even a request that IS correctly signed for the real secret.
	if _, err := ParseSignedRequest(sign(t, goodPayload, testSecret), ""); err == nil {
		t.Fatal("an empty app secret accepted a genuine request")
	}
}

func TestMalformedEnvelopesAreRefused(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"no separator":      "justonelongstring",
		"empty signature":   ".eyJhbGciOiJIUzI1NiJ9",
		"empty payload":     "c2ln.",
		"signature not b64": "!!!not-base64!!!.eyJhIjoxfQ",
		"payload not b64":   sign(t, goodPayload, testSecret)[:strings.Index(sign(t, goodPayload, testSecret), ".")] + ".!!!",
		"payload not json":  sign(t, "this is not json", testSecret),
		"whitespace only":   "   ",
	}
	for name, raw := range cases {
		if _, err := ParseSignedRequest(raw, testSecret); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A body far larger than a callback is refused before it is decoded.
func TestAnOversizedSignedRequestIsRefused(t *testing.T) {
	huge := strings.Repeat("a", maxSignedRequestBytes+1)
	if _, err := ParseSignedRequest("sig."+huge, testSecret); err == nil {
		t.Fatal("an oversized signed_request was parsed")
	}
}

// Meta sends base64url WITHOUT padding. Accepting the padded form too costs
// nothing and avoids a whole class of "works in the docs, fails in
// production" reports.
func TestPaddedBase64IsAlsoAccepted(t *testing.T) {
	payload := base64.URLEncoding.EncodeToString([]byte(goodPayload)) // padded
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(payload))
	sig := base64.URLEncoding.EncodeToString(mac.Sum(nil)) // padded

	if _, err := ParseSignedRequest(sig+"."+payload, testSecret); err != nil {
		t.Fatalf("a padded base64url request was refused: %v", err)
	}
}
