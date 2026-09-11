package domain

import (
	"testing"
	"time"
)

func TestParseVersionAcceptsOnlyThreePartSemVer(t *testing.T) {
	ok := map[string]Version{
		"1.0.0":   {1, 0, 0},
		"v1.0.0":  {1, 0, 0},
		"0.0.1":   {0, 0, 1},
		"1.10.0":  {1, 10, 0},
		"12.4.99": {12, 4, 99},
	}
	for in, want := range ok {
		got, err := ParseVersion(in)
		if err != nil {
			t.Errorf("ParseVersion(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", in, got, want)
		}
	}

	bad := []string{
		"", "1", "1.0", "1.0.0.0", "latest", "v", "1.0.x",
		// Leading zeroes are rejected so "01.0.0" and "1.0.0" cannot both
		// name the same version.
		"01.0.0", "1.01.0", "1.0.00",
		// Pre-release and build metadata are out of scope: accepting them
		// without deciding how they order would put ambiguity into the one
		// timeline that exists to remove it.
		"1.0.0-rc.1", "1.0.0+build.5",
		"-1.0.0", " 1.0.0.",
	}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) was accepted; want rejected", in)
		} else if !IsKind(err, KindInvalid) {
			t.Errorf("ParseVersion(%q) error kind = %v, want invalid", in, err)
		}
	}
}

// TestCompareIsNumericNotLexical is the rule the schema's integer columns
// exist to preserve. As text, "1.9.0" sorts above "1.10.0".
func TestCompareIsNumericNotLexical(t *testing.T) {
	nine := Version{1, 9, 0}
	ten := Version{1, 10, 0}
	if nine.Compare(ten) >= 0 {
		t.Errorf("1.9.0 compared >= 1.10.0; the ordering is lexical")
	}
	if ten.Compare(nine) <= 0 {
		t.Errorf("1.10.0 compared <= 1.9.0")
	}
	if nine.Compare(nine) != 0 {
		t.Errorf("a version does not equal itself")
	}
	if (Version{2, 0, 0}).Compare(Version{1, 99, 99}) <= 0 {
		t.Errorf("major must dominate minor and patch")
	}
}

func TestNewReleaseStartsAsADraftWithEmptyLists(t *testing.T) {
	r, err := NewRelease("agents", "v1.0.0", "  a summary  ")
	if err != nil {
		t.Fatalf("NewRelease: %v", err)
	}
	if r.Status != StatusDraft {
		t.Errorf("status = %q, want draft: recording is never declaring", r.Status)
	}
	if r.Version != "1.0.0" {
		t.Errorf("version = %q; the leading v must be normalised away", r.Version)
	}
	if r.Summary != "a summary" {
		t.Errorf("summary = %q, want it trimmed", r.Summary)
	}
	if r.ReleasedAt != nil || r.PublishedAt != nil {
		t.Error("a draft carries no dates")
	}
	// Empty, not nil: the wire form must always be a list.
	if r.Capabilities == nil || r.Evidence == nil || r.Limitations == nil ||
		r.Decisions == nil || r.TechnicalNotes == nil || r.DocRefs == nil {
		t.Error("snapshot slices must be initialised empty, never nil")
	}
}

func TestNewReleaseRefusesWhatItCannotRecord(t *testing.T) {
	if _, err := NewRelease("agents", "1.0.0", "   "); !IsKind(err, KindInvalid) {
		t.Errorf("an empty summary was accepted; a release with no summary records nothing")
	}
	if _, err := NewRelease("Not A Key", "1.0.0", "x"); !IsKind(err, KindInvalid) {
		t.Errorf("an invalid module key was accepted")
	}
	if _, err := NewRelease("agents", "nope", "x"); !IsKind(err, KindInvalid) {
		t.Errorf("an invalid version was accepted")
	}
}

func TestValidModuleKey(t *testing.T) {
	for _, k := range []string{"agents", "job-radar", "a1", "a-b-c"} {
		if !ValidModuleKey(k) {
			t.Errorf("%q should be a valid key", k)
		}
	}
	for _, k := range []string{"", "Agents", "job_radar", "-a", "a-", "a--b", "a b", "a/b"} {
		if ValidModuleKey(k) {
			t.Errorf("%q should be rejected", k)
		}
	}
}

func TestPublishIsNotIdempotent(t *testing.T) {
	r, _ := NewRelease("agents", "1.0.0", "x")
	at := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	if err := r.Publish(at); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if !r.IsPublished() || r.ReleasedAt == nil || !r.ReleasedAt.Equal(at) {
		t.Fatalf("publish did not take: %+v", r)
	}
	// A second publish must be refused rather than silently re-stamping:
	// "already published, on a date someone else chose" is the fact an
	// audit trail exists to keep.
	if err := r.Publish(at.Add(time.Hour)); !IsKind(err, KindConflict) {
		t.Errorf("second publish error = %v, want a conflict", err)
	}
	if !r.ReleasedAt.Equal(at) {
		t.Errorf("the original release date was overwritten")
	}
}

// TestCurrentOfSkipsDraftsAndRanksByVersion holds the two rules that decide
// what the product claims to be running.
func TestCurrentOfSkipsDraftsAndRanksByVersion(t *testing.T) {
	published := func(v string, at time.Time) *Release {
		r, err := NewRelease("agents", v, "x")
		if err != nil {
			t.Fatalf("NewRelease(%q): %v", v, err)
		}
		if err := r.Publish(at); err != nil {
			t.Fatalf("publish %q: %v", v, err)
		}
		return r
	}
	draft := func(v string) *Release {
		r, _ := NewRelease("agents", v, "x")
		return r
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if CurrentOf(nil) != nil {
		t.Error("CurrentOf(nil) must be nil, not a fabricated version")
	}
	if got := CurrentOf([]*Release{draft("9.9.9")}); got != nil {
		t.Errorf("a draft became current: %v", got.Version)
	}

	// Ranked by version, not by insertion order and not by date: a patch
	// to an old line published later does not become current.
	got := CurrentOf([]*Release{
		published("1.0.0", base),
		published("1.10.0", base.AddDate(0, 1, 0)),
		published("1.2.0", base.AddDate(0, 2, 0)),
		draft("2.0.0"),
	})
	if got == nil || got.Version != "1.10.0" {
		t.Fatalf("current = %v, want 1.10.0", got)
	}
}
