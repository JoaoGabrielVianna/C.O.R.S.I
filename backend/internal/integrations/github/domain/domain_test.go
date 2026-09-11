package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func uuidNonZero() uuid.UUID { return uuid.New() }

/* ── the credential ──────────────────────────────────────────────────── */

func TestTokenValidationNeverQuotesTheToken(t *testing.T) {
	// The rule with no exception: a validation error that echoes its input
	// is the most common way a secret reaches a log file. Every rejection
	// path is checked, not just the obvious one.
	secret := "ghp_" + strings.Repeat("s", 60)
	cases := map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"too long":   strings.Repeat(secret, 20),
		// Interior, not trailing. A trailing newline is what a paste from a
		// terminal produces and is trimmed rather than refused; a control
		// character INSIDE the value is the one that could split a header.
		"control": secret[:10] + "\n" + secret[10:],
		"space":   secret[:10] + " " + secret[10:],
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateToken(token)
			if err == nil {
				t.Fatalf("%q was accepted", name)
			}
			if strings.Contains(err.Error(), secret[:20]) {
				t.Fatalf("the error quoted the token: %q", err)
			}
		})
	}
}

func TestAPlausibleTokenIsAccepted(t *testing.T) {
	// Both shapes GitHub issues today. A validator that only knew one would
	// reject the other on the day somebody pasted it.
	for _, token := range []string{
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("b", 82),
		"  ghp_" + strings.Repeat("c", 36) + "  ", // trimmed, not refused
	} {
		if err := ValidateToken(token); err != nil {
			t.Fatalf("a plausible token was refused: %v", err)
		}
	}
}

func TestATokenWithAHeaderInjectionIsRefused(t *testing.T) {
	// It goes into an HTTP header. A CR or LF in one either gets rejected
	// at the socket or, on a lesser stack, splits the header.
	for _, bad := range []string{"abc\r\nX-Evil: 1", "abc\ndef", "abc\rdef"} {
		if err := ValidateToken(bad); err == nil {
			t.Fatalf("%q was accepted as a token", bad)
		}
	}
}

/* ── the base URL ────────────────────────────────────────────────────── */

func TestAPIBaseURLMustBeAnAbsoluteHTTPURL(t *testing.T) {
	// This value becomes the prefix of every request that carries the
	// token. A stored `file://` or a bare host is a credential pointed
	// somewhere nobody chose.
	for _, bad := range []string{"", "api.github.com", "file:///etc", "ftp://x", "https://"} {
		if err := ValidateAPIBaseURL(bad); err == nil {
			t.Fatalf("%q was accepted as an API base URL", bad)
		}
	}
	for _, good := range []string{"https://api.github.com", "http://127.0.0.1:8080"} {
		if err := ValidateAPIBaseURL(good); err != nil {
			t.Fatalf("%q was refused: %v", good, err)
		}
	}
}

func TestNormalizeAPIBaseURLDefaultsAndTrims(t *testing.T) {
	if got := NormalizeAPIBaseURL(""); got != DefaultAPIBaseURL {
		t.Fatalf("empty normalized to %q, want the github.com default", got)
	}
	if got := NormalizeAPIBaseURL("https://ghe.acme.com/api/v3/"); got != "https://ghe.acme.com/api/v3" {
		t.Fatalf("got %q, want the trailing slash gone", got)
	}
}

/* ── repository identity ─────────────────────────────────────────────── */

// These two functions are what make path construction safe by construction
// rather than by review. A stored owner or name that could contain a slash
// or a dot segment would defeat every escape downstream.
func TestOwnerAndNameCannotContainPathSeparatorsOrTraversal(t *testing.T) {
	for _, bad := range []string{
		"", "a/b", "..", ".", "a b", "a?b", "a#b", "a%2fb", "-leading",
		strings.Repeat("a", 40),
	} {
		if ValidOwner(bad) {
			t.Fatalf("owner %q was accepted", bad)
		}
	}
	for _, bad := range []string{"", "a/b", "..", ".", "a b", "a?b", "a:b", strings.Repeat("a", 101)} {
		if ValidRepoName(bad) {
			t.Fatalf("repo name %q was accepted", bad)
		}
	}
	for _, good := range []string{"acme", "Acme-Corp", "a1", "joaocorsi"} {
		if !ValidOwner(good) {
			t.Fatalf("owner %q was refused", good)
		}
	}
	for _, good := range []string{"website", "c.o.r.s.i", "my_repo", "repo-1.0"} {
		if !ValidRepoName(good) {
			t.Fatalf("repo name %q was refused", good)
		}
	}
}

func TestValidateRepositoryRefusesAFullNameThatDisagreesWithItsParts(t *testing.T) {
	// full_name is only ever a lookup key, and owner/name is what builds a
	// path. A row where they disagree would make the key find a row that
	// then reads a different repository.
	r := Repository{
		WorkspaceID:  uuidNonZero(),
		ConnectionID: uuidNonZero(),
		GitHubID:     1,
		Owner:        "acme",
		Name:         "website",
		FullName:     "someoneelse/private",
	}
	if err := r.Validate(); err == nil {
		t.Fatal("a repository whose full_name names a different repository was accepted")
	}
}

func TestFullNameShapeRejectsWhatAModelMightInvent(t *testing.T) {
	// Not a security boundary — the boundary is that a key matching no row
	// is denied — but it makes the common refusal cheap and specific.
	for _, bad := range []string{
		"", "acme", "acme/", "/website", "../../etc/passwd",
		"https://github.com/acme/website", "acme/website/tree/main",
		"acme/../other", "acme website",
	} {
		if ValidFullNameShape(bad) {
			t.Fatalf("%q passed the shape check", bad)
		}
	}
	for _, good := range []string{"acme/website", "Acme/Web-Site", " acme/website "} {
		if !ValidFullNameShape(good) {
			t.Fatalf("%q failed the shape check", good)
		}
	}
}

func TestNormalizeFullNameFoldsCaseSoAModelDoesNotHaveToGuessIt(t *testing.T) {
	for _, in := range []string{"Acme/Website", "ACME/WEBSITE", " acme/website ", "/acme/website/"} {
		if got := NormalizeFullName(in); got != "acme/website" {
			t.Fatalf("NormalizeFullName(%q) = %q", in, got)
		}
	}
}

/* ── the error vocabulary ────────────────────────────────────────────── */

func TestTheRefusalsThatMatterCarryDistinctMachineReadableCodes(t *testing.T) {
	// A client and a model both branch on these. Two refusals sharing a
	// code is two situations nobody downstream can tell apart.
	seen := map[string]string{}
	for name, err := range map[string]*Error{
		"not connected":    NotConnected(),
		"not authorized":   RepositoryNotAuthorized("acme/website"),
		"github rejected":  Unauthorized("x"),
		"github throttled": RateLimited("x"),
	} {
		if err.Code == "" {
			t.Fatalf("%s carries no code", name)
		}
		if other, dup := seen[err.Code]; dup {
			t.Fatalf("%s and %s share the code %q", name, other, err.Code)
		}
		seen[err.Code] = name
	}
}

func TestRepositoryRefusalNamesWhatWasRefusedWithoutClaimingItExists(t *testing.T) {
	err := RepositoryNotAuthorized("acme/website")
	if !strings.Contains(err.Message, "acme/website") {
		t.Fatalf("the refusal does not say what was refused: %q", err.Message)
	}
	// It must not say "does not exist": a caller who may not read a
	// repository must not learn whether it is there.
	if strings.Contains(strings.ToLower(err.Message), "does not exist") {
		t.Fatalf("the refusal leaks existence: %q", err.Message)
	}
}

func TestIsKindSeesThroughTheErrorInterface(t *testing.T) {
	var err error = RateLimited("slow down")
	if !IsKind(err, KindRateLimited) {
		t.Fatal("IsKind did not recognise a rate limit through the error interface")
	}
	if IsKind(err, KindUnauthorized) {
		t.Fatal("IsKind confused a rate limit with an auth failure")
	}
}
