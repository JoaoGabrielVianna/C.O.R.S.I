// Package domain holds the release-history entities and the rules that
// keep a recorded release honest.
//
// Two of those rules carry the whole context:
//
//  1. A published release is immutable. Not "should not be edited" —
//     cannot be. The database enforces it too (see the freeze trigger in
//     migrations/releases/0001), because an invariant that lives only in
//     application code is a promise, and this one has to be a guarantee.
//
//  2. A snapshot is never recomputed. The capabilities, evidence,
//     limitations and decisions of a release are copied in when it is
//     recorded and read back verbatim forever. Nothing in this package
//     reads the current state of any module.
package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ── module ─────────────────────────────────────────────────────────────

// ModuleStatus is the lifecycle of a module, which is a different fact
// from the status of its newest release. A module can be active with
// nothing published yet.
type ModuleStatus string

const (
	// ModuleActive receives work and is expected to keep shipping.
	ModuleActive ModuleStatus = "active"
	// ModulePartial works, but has known gaps that keep it from being
	// production-ready.
	ModulePartial ModuleStatus = "partial"
	// ModuleFrozen is preserved as-is. It keeps whatever history it has
	// and gains no more.
	ModuleFrozen ModuleStatus = "frozen"
)

func (s ModuleStatus) Valid() bool {
	switch s {
	case ModuleActive, ModulePartial, ModuleFrozen:
		return true
	}
	return false
}

// Module is the stable identity of a versioned part of the platform.
//
// It deliberately carries no `current_version` column. The current release
// is derived from the releases themselves — the highest published version
// — so there is no second copy of that fact to drift out of agreement
// with the timeline it summarises.
type Module struct {
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Status      ModuleStatus `json:"status"`
	Position    int          `json:"-"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// moduleKeyPattern keeps keys URL-safe, because the key is the routing
// identity: it appears in /releases/modules/{key} and in links people
// paste into documents.
var moduleKeyPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func ValidModuleKey(k string) bool { return k != "" && moduleKeyPattern.MatchString(k) }

// ── version ────────────────────────────────────────────────────────────

// Version is a parsed SemVer MAJOR.MINOR.PATCH.
//
// Pre-release and build metadata are not accepted. The product versions
// whole modules on a three-number scheme, and accepting `1.0.0-rc.1`
// without deciding how it orders against `1.0.0` would put an ambiguity
// straight into the timeline the page exists to make unambiguous.
type Version struct {
	Major int
	Minor int
	Patch int
}

var versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// ParseVersion accepts "1.0.0" and, as a convenience for callers echoing a
// UI label, "v1.0.0". It rejects everything else — including leading
// zeroes, which would let '1.01.0' and '1.1.0' name the same version.
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(s), "v")
	m := versionPattern.FindStringSubmatch(raw)
	if m == nil {
		return Version{}, Invalid(fmt.Sprintf("version %q is not a valid MAJOR.MINOR.PATCH semantic version", s))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// Compare returns -1, 0 or +1. Ordering is numeric per component, which is
// the entire reason the parts are stored as integers.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return sign(v.Major - o.Major)
	case v.Minor != o.Minor:
		return sign(v.Minor - o.Minor)
	case v.Patch != o.Patch:
		return sign(v.Patch - o.Patch)
	}
	return 0
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

// ── release ────────────────────────────────────────────────────────────

type ReleaseStatus string

const (
	// StatusDraft is recorded but not declared. It is editable, and it is
	// never anyone's current release.
	StatusDraft ReleaseStatus = "draft"
	// StatusPublished is a declared release. From this point the row is
	// frozen.
	StatusPublished ReleaseStatus = "published"
)

func (s ReleaseStatus) Valid() bool {
	return s == StatusDraft || s == StatusPublished
}

// Stability is how much a release asks to be trusted, which is a separate
// question from whether it has been declared.
//
// The two are independent on purpose. A release candidate can be published
// — that is what an RC is — and a stable version can sit unpublished while
// someone decides. One column answering both would have to lie in one
// direction the first time a beta shipped.
type Stability string

const (
	// StabilityStable is an ordinary release. It is the default because a
	// release recorded without qualification is a normal one; pre-release
	// maturity is what a person opts into and says out loud.
	StabilityStable Stability = "stable"
	StabilityBeta   Stability = "beta"
	StabilityRC     Stability = "rc"
)

func (s Stability) Valid() bool {
	switch s {
	case StabilityStable, StabilityBeta, StabilityRC:
		return true
	}
	return false
}

// Capability is something the version lets a person do. `Note` bounds the
// claim — what the capability does *not* cover is as useful to an auditor
// as what it does.
type Capability struct {
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
}

// Metric is a measured fact about the release, as a label and a rendered
// value. The value is a string rather than a number because "8/8" and
// "123" are both legitimate answers, and inventing a numeric type here
// would force one of them into a shape it does not have.
type Metric struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Note is a free line of prose with an optional reference to whatever
// makes it verifiable.
type Note struct {
	Text string `json:"text"`
	Ref  string `json:"ref,omitempty"`
}

// DocRef points at the document that justifies part of this snapshot. It
// is a pointer, never a source: the page renders the stored row and does
// not read Markdown to reconstruct what shipped.
type DocRef struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Release is one version of one module, frozen at the moment it was
// recorded.
type Release struct {
	ID        string        `json:"id"`
	ModuleKey string        `json:"module_key"`
	Version   string        `json:"version"`
	Status    ReleaseStatus `json:"status"`
	// Stability is the maturity claim. Independent of Status: see the type.
	Stability  Stability  `json:"stability"`
	ReleasedAt *time.Time `json:"released_at"`

	Summary string `json:"summary"`

	Capabilities   []Capability `json:"capabilities"`
	Evidence       []Metric     `json:"evidence"`
	Limitations    []Note       `json:"limitations"`
	Decisions      []Note       `json:"decisions"`
	TechnicalNotes []Note       `json:"technical_notes"`
	DocRefs        []DocRef     `json:"doc_refs"`

	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
}

// Parsed re-reads the version string. The parts are stored alongside it in
// the database for ordering; this is for callers holding a Release value.
func (r Release) Parsed() (Version, error) { return ParseVersion(r.Version) }

func (r Release) IsPublished() bool { return r.Status == StatusPublished }

// NewRelease is the only constructor, and it validates everything a
// release must satisfy before it can exist. Recording is always the same
// shape regardless of who calls it.
//
// Snapshot slices are normalised to empty rather than nil so the JSON wire
// form is a list in every case: a client rendering `capabilities.map` must
// not have to distinguish "none" from "absent".
func NewRelease(moduleKey, version, summary string) (*Release, error) {
	if !ValidModuleKey(moduleKey) {
		return nil, Invalid(fmt.Sprintf("module key %q is not a valid identifier", moduleKey))
	}
	v, err := ParseVersion(version)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return nil, Invalid("summary is required: a release with no summary records nothing")
	}
	return &Release{
		ModuleKey:      moduleKey,
		Version:        v.String(),
		Status:         StatusDraft,
		Stability:      StabilityStable,
		Summary:        strings.TrimSpace(summary),
		Capabilities:   []Capability{},
		Evidence:       []Metric{},
		Limitations:    []Note{},
		Decisions:      []Note{},
		TechnicalNotes: []Note{},
		DocRefs:        []DocRef{},
	}, nil
}

// Publish moves a draft to published at the given instant.
//
// It refuses a second publish rather than treating it as a no-op. Making
// it idempotent would mean a caller that published the wrong version could
// re-run and see success, and the difference between "this is now
// published" and "this was already published, by someone else, on another
// date" is exactly what an audit trail exists to preserve.
func (r *Release) Publish(at time.Time) error {
	if r.IsPublished() {
		return Conflict(fmt.Sprintf("release %s/%s is already published and cannot be published again", r.ModuleKey, r.Version))
	}
	at = at.UTC()
	r.Status = StatusPublished
	r.ReleasedAt = &at
	r.PublishedAt = &at
	return nil
}

// CurrentOf picks the release a module is currently on: the highest
// published version.
//
// Drafts are skipped, and that is the rule the whole draft state exists
// for — recording a version must never change what the product claims to
// be running. Ordering is by version, not by date: a patch published to an
// old line after a newer minor shipped does not become current.
func CurrentOf(rs []*Release) *Release {
	var best *Release
	var bestV Version
	for _, r := range rs {
		if !r.IsPublished() {
			continue
		}
		v, err := r.Parsed()
		if err != nil {
			continue
		}
		if best == nil || v.Compare(bestV) > 0 {
			best, bestV = r, v
		}
	}
	return best
}
