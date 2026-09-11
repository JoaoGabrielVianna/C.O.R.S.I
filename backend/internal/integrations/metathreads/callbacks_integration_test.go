//go:build integration

package metathreads

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	threadstools "github.com/corsi/backend/internal/threads/tools"
)

/*
Meta's two platform callbacks, over real HTTP against real Postgres.

── Why these are the most dangerous endpoints in the product ───────────
They are the only routes in this integration that carry no workspace
header and no credential of ours, and one of them DELETES. Everything
that keeps them safe is in the signature check, so most of what follows
is an attempt to make them act without a valid one.
*/

// signedRequest builds the envelope exactly as Meta does.
func signedRequest(t *testing.T, payloadJSON, secret string) string {
	t.Helper()
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) + "." + payload
}

func payloadFor(accountID string) string {
	return `{"algorithm":"HMAC-SHA256","user_id":"` + accountID + `","issued_at":1756000000}`
}

// postCallback sends a form-encoded POST, the way Meta does — and with NO
// workspace header, because Meta has never heard of a workspace.
func (e *env) postCallback(path, signed string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path,
		strings.NewReader("signed_request="+signed))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// liveConnections counts what the workspace can still see.
func (e *env) liveConnections() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT count(*) FROM meta_threads.connections WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		e.t.Fatalf("count connections: %v", err)
	}
	return n
}

/* ── deauthorization ─────────────────────────────────────────────────── */

// A genuine ping removes exactly the account it names.
func TestDeauthorizeRemovesTheConnectionItNames(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)
	if e.liveConnections() != 1 {
		t.Fatal("fixture did not connect")
	}

	rec := e.postCallback("/integrations/meta-threads/deauthorize",
		signedRequest(t, payloadFor(conn.AccountID), testAppSecret))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if e.liveConnections() != 0 {
		t.Fatal("the deauthorized connection is still live")
	}

	// And the credential is destroyed, not merely orphaned.
	var cipher []byte
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT token_cipher FROM meta_threads.connections WHERE id = $1`, conn.ID).Scan(&cipher); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if len(cipher) != 0 {
		t.Fatal("a sealed credential survived deauthorization")
	}
}

// THE test for this surface. A body signed with anything but the app secret
// must change nothing — otherwise the public URL is "delete whoever this
// names".
func TestAForgedCallbackCannotDeleteAnything(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	for _, path := range []string{
		"/integrations/meta-threads/deauthorize",
		"/integrations/meta-threads/data-deletion",
	} {
		forged := signedRequest(t, payloadFor(conn.AccountID), "attacker-guessed-this")
		rec := e.postCallback(path, forged)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s accepted a forged signature: %d", path, rec.Code)
		}
		if e.liveConnections() != 1 {
			t.Fatalf("%s deleted a connection on a forged signature", path)
		}
	}

	// Unsigned, and with no parameter at all.
	for _, body := range []string{"", "not-an-envelope", "sig.payload"} {
		rec := e.postCallback("/integrations/meta-threads/deauthorize", body)
		if rec.Code == http.StatusOK {
			t.Errorf("an unsigned body was accepted: %q", body)
		}
		if e.liveConnections() != 1 {
			t.Fatalf("an unsigned body deleted a connection: %q", body)
		}
	}
}

// A genuine callback for an account nobody linked is a success with nothing
// to do — not an error, and not somebody else's row.
func TestACallbackForAnUnknownAccountRemovesNothing(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	rec := e.postCallback("/integrations/meta-threads/deauthorize",
		signedRequest(t, payloadFor("99999999999999999"), testAppSecret))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if e.liveConnections() != 1 {
		t.Fatal("a callback for another account removed a connection")
	}
}

/* ── data deletion ───────────────────────────────────────────────────── */

// The response shape Meta's own check requires: url + confirmation_code.
func TestDataDeletionReturnsTheContractMetaRequires(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	rec := e.postCallback("/integrations/meta-threads/data-deletion",
		signedRequest(t, payloadFor(conn.AccountID), testAppSecret))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	url, _ := body["url"].(string)
	code, _ := body["confirmation_code"].(string)
	if url == "" || code == "" {
		t.Fatalf("the contract requires both fields: %+v", body)
	}
	// The url has to point at something a person can read, carrying the code.
	if !strings.Contains(url, "/integrations/meta-threads/data-deletion") {
		t.Errorf("url = %q", url)
	}
	if !strings.Contains(url, code) {
		t.Errorf("the status url does not carry the confirmation code: %q", url)
	}
	// The deletion actually happened; the code is not a promise of later work.
	if e.liveConnections() != 0 {
		t.Fatal("data deletion returned a code and deleted nothing")
	}
}

// The confirmation code must not be the account id wearing a hat: it is
// shown, logged and pasted into support messages.
func TestTheConfirmationCodeDoesNotCarryTheAccountID(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	rec := e.postCallback("/integrations/meta-threads/data-deletion",
		signedRequest(t, payloadFor(conn.AccountID), testAppSecret))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	code, _ := body["confirmation_code"].(string)

	if strings.Contains(code, conn.AccountID) {
		t.Fatalf("the confirmation code embeds the app-scoped user id: %q", code)
	}
	if code == conn.AccountID {
		t.Fatal("the confirmation code IS the user id")
	}
}

// The status page renders with no session, no workspace and no bundle —
// Meta sends a person straight to it.
func TestTheStatusPageIsReadableWithNoSession(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest(http.MethodGet,
		"/integrations/meta-threads/data-deletion?code=abc123def456", nil)
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"COMPLETE", "abc123def456", "never stored"} {
		if !strings.Contains(body, want) {
			t.Errorf("the status page does not mention %q:\n%s", want, body)
		}
	}
	// Plain text with nosniff, so an echoed code cannot become markup.
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the echoed code is served without nosniff")
	}
}

/* ── the boundary the callbacks must not cross ───────────────────────── */

// A Meta callback deletes the CREDENTIAL and nothing else. The operator's
// own content pipeline is their work, authored in our product; it is not
// Meta's data and a Meta callback has no standing over it.
func TestDataDeletionDoesNotTouchTheInternalThreadsPipeline(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	// The operator's own drafts, in the INTERNAL module.
	e.execute(t, e.wsA, threadstools.ThreadCreateTool, map[string]any{
		"title": "Minha ideia", "content": "texto que é meu, não da Meta",
	})
	before := len(e.liveThreads(e.wsA))
	if before != 1 {
		t.Fatalf("fixture: %d internal threads", before)
	}

	rec := e.postCallback("/integrations/meta-threads/data-deletion",
		signedRequest(t, payloadFor(conn.AccountID), testAppSecret))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	if e.liveConnections() != 0 {
		t.Fatal("the credential was not deleted")
	}
	if got := len(e.liveThreads(e.wsA)); got != before {
		t.Fatalf("a Meta callback deleted %d of the operator's own threads", before-got)
	}
}

// The callbacks carry no workspace header and must not need one — that is
// the whole reason they sit outside the workspace middleware.
func TestTheCallbacksWorkWithNoWorkspaceHeader(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	// postCallback deliberately sets no X-Test-Workspace-Id.
	rec := e.postCallback("/integrations/meta-threads/deauthorize",
		signedRequest(t, payloadFor(conn.AccountID), testAppSecret))
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("the callback was refused for having no workspace: %d", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

// A user linked in two workspaces asking to be deleted must be deleted in
// both: a platform callback names a PERSON, and honouring it in one place
// would leave a live credential the user believes is gone.
func TestDeletionCoversEveryWorkspaceThatLinkedTheAccount(t *testing.T) {
	e := newEnv(t)
	a := e.connect(e.wsA)
	b := e.connect(e.wsB)
	if a.AccountID != b.AccountID {
		t.Fatalf("fixture: the two workspaces linked different accounts")
	}
	if e.liveConnections() != 2 {
		t.Fatalf("fixture: %d connections", e.liveConnections())
	}

	rec := e.postCallback("/integrations/meta-threads/data-deletion",
		signedRequest(t, payloadFor(a.AccountID), testAppSecret))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if e.liveConnections() != 0 {
		t.Fatal("a deletion request left a live credential in another workspace")
	}
}

// No secret ever reaches a callback response, however it fails.
func TestNoResponseCarriesTheAppSecret(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)

	bodies := []string{
		signedRequest(t, payloadFor(conn.AccountID), testAppSecret),
		signedRequest(t, payloadFor(conn.AccountID), "wrong-secret"),
		"garbage",
		"",
	}
	for _, path := range []string{
		"/integrations/meta-threads/deauthorize",
		"/integrations/meta-threads/data-deletion",
	} {
		for _, b := range bodies {
			rec := e.postCallback(path, b)
			if strings.Contains(rec.Body.String(), testAppSecret) {
				t.Fatalf("%s leaked the app secret", path)
			}
		}
	}
}
