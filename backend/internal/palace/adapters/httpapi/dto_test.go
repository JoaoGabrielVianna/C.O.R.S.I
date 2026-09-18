package httpapi

// The structural half of the DTO boundary.
//
// The other half lives in the integration suite, which drives real
// requests and asserts that no response body ever contains `"redacted"`.
// This file needs no database: it asks a question about TYPES, and the
// answer cannot depend on data.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
)

// domainPkg is the import path no wire type may reach, at any depth.
const domainPkg = "internal/palace/domain"

// responseTypes is every type this package hands to an encoder.
//
// Adding a response shape without adding it here would leave it
// unchecked, so the list is the one place a reviewer looks. It is short on
// purpose: a surface with many shapes is a surface nobody can audit.
func responseTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(page[roomRow]{}),
		reflect.TypeOf(page[artifactRow]{}),
		reflect.TypeOf(page[memoryRow]{}),
		reflect.TypeOf(roomRow{}),
		reflect.TypeOf(roomDetail{}),
		reflect.TypeOf(artifactRow{}),
		reflect.TypeOf(artifactItem{}),
		reflect.TypeOf(artifactRoomRef{}),
		reflect.TypeOf(artifactDetail{}),
		reflect.TypeOf(memoryRow{}),
		reflect.TypeOf(memoryDetail{}),
		reflect.TypeOf(overviewResponse{}),
		reflect.TypeOf(overviewRoom{}),
		reflect.TypeOf(overviewUnfiled{}),
		reflect.TypeOf(neighborsResponse{}),
		reflect.TypeOf(neighborArtifact{}),
		reflect.TypeOf(neighborMemory{}),
		reflect.TypeOf(neighborEntity{}),
	}
}

// TestTheNeighboursResponseCarriesNoCount is a structural guard on the
// one field this endpoint must never grow.
//
// ── Why by name and not by behaviour ───────────────────────────────────
// Because the behavioural tests check the VALUES in a fixture, and a
// `total` added later would pass every one of them while being exactly the
// leak: it would report the number of relations, including those whose
// endpoint was withheld. The field must not exist, so the test is about
// the field existing.
func TestTheNeighboursResponseCarriesNoCount(t *testing.T) {
	banned := map[string]bool{
		"total": true, "count": true, "hidden": true, "omitted": true,
		"withheld": true, "truncated": true, "relation_count": true,
		"hidden_count": true, "omitted_count": true,
	}
	rt := reflect.TypeOf(neighborsResponse{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if banned[tag] {
			t.Errorf("neighborsResponse carries %q; a neighbour count is the whole "+
				"leak in one integer, whatever it is called", tag)
		}
		if rt.Field(i).Type.Kind() != reflect.Slice {
			t.Errorf("neighborsResponse.%s is %s, not a slice; every field here is a "+
				"group of neighbours and nothing else", rt.Field(i).Name, rt.Field(i).Type)
		}
	}
}

// TestEmptyNeighboursEncodeAsArrays keeps "nothing is connected" and "this
// server does not report that" from being the same response.
func TestEmptyNeighboursEncodeAsArrays(t *testing.T) {
	out := neighborsOf(&app.ArtifactNeighbors{})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("an empty neighbour group encoded as null: %s", raw)
	}
	for _, key := range []string{
		"supersedes", "superseded_by", "mentions", "mentioned_by", "related", "decisions",
	} {
		if !strings.Contains(string(raw), `"`+key+`":[]`) {
			t.Errorf("key %q is missing or not an empty array: %s", key, raw)
		}
	}
}

// TestASupersedesNeighbourIsNeverBuiltFromRawEnds is a source-level guard.
//
// ── Why a test reads the source ────────────────────────────────────────
// Because the direction of SUPERSEDES cannot be caught any other way once
// it is wrong. Both ends are artifacts, so an inverted reading produces a
// coherent history with no error anywhere, and a fixture test only catches
// it if somebody wrote the fixture the right way round. The domain froze
// the answer behind `Superseding()`, and the rule is that the classifier
// calls it rather than comparing `FromID` itself.
func TestASupersedesNeighbourIsNeverBuiltFromRawEnds(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "app", "neighbors.go"))
	if err != nil {
		t.Fatalf("read classifier: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "rel.Superseding()") {
		t.Fatal("the classifier does not call Relation.Superseding(); the frozen " +
			"direction is being re-derived, and an inversion would be silent")
	}
	if strings.Contains(body, "RelationSupersedes") {
		t.Error("the classifier names the supersedes kind directly, which suggests it " +
			"is branching on the kind instead of asking the domain for the direction")
	}
}

// TestNoWireTypeCarriesADomainType is the regression detector for the
// boundary the package comment describes.
//
// ── What breaks if this is deleted ─────────────────────────────────────
// A handler that returned `domain.Room` instead of `roomRow` would not
// crash and would not leak: the entity redacts itself, so the response
// would be `{"type":"palace.room","id":…,"redacted":true}` and the screen
// would render an empty card. That is a SAFE failure and a silent one,
// which is the kind most likely to reach production. This test makes it
// loud, and it does so at the type level, so it catches the nested case
// too: a wire struct that embeds an entity three fields down.
func TestNoWireTypeCarriesADomainType(t *testing.T) {
	for _, rt := range responseTypes() {
		walkFields(t, rt, rt.Name(), map[reflect.Type]bool{})
	}
}

func walkFields(t *testing.T, rt reflect.Type, path string, seen map[reflect.Type]bool) {
	t.Helper()
	if seen[rt] {
		return
	}
	seen[rt] = true

	for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
		rt = rt.Elem()
	}
	if strings.Contains(rt.PkgPath(), domainPkg) {
		t.Fatalf("%s is %s, which comes from the domain package. "+
			"Wire types are built field by field; see the package comment", path, rt.String())
	}
	if rt.Kind() != reflect.Struct {
		return
	}
	// time.Time is a struct from the standard library and has no fields
	// worth walking into. Everything else gets inspected.
	if rt == reflect.TypeOf(time.Time{}) {
		return
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		walkFields(t, f.Type, path+"."+f.Name, seen)
	}
}

// TestEveryWireFieldIsNamedOnTheWire keeps the surface explicit: a field
// without a json tag is a field somebody exposed by accident, under
// whatever name Go happened to give it.
func TestEveryWireFieldIsNamedOnTheWire(t *testing.T) {
	for _, rt := range responseTypes() {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Tag.Get("json") == "" {
				t.Errorf("%s.%s has no json tag; every exposed field is named on purpose",
					rt.Name(), f.Name)
			}
		}
	}
}

/* ── the visibility this surface imposes ─────────────────────────────── */

// TestTheSurfaceNeverAdmitsHighlySensitive pins the two constants that the
// whole privacy posture rests on. It is deliberately a test of the VALUES
// rather than of a behaviour: flipping either one would still compile, and
// every behavioural test in the integration suite would keep passing right
// up until the one that checks the withheld row.
func TestTheSurfaceNeverAdmitsHighlySensitive(t *testing.T) {
	v := surfaceVisibility()
	if v.IncludeHighlySensitive {
		t.Fatal("the read surface admits highly sensitive content; it must never")
	}
	if !v.InheritRoomVisibility {
		t.Fatal("the read surface does not inherit room visibility; " +
			"an artifact in a withheld room would be rendered")
	}
}

// TestEveryFilterConstructorCarriesBothRules is the companion check, and
// it is the one that catches the likelier mistake.
//
// Forgetting `IncludeHighlySensitive` is loud: the withheld row shows up
// and the privacy suite goes red. Forgetting `InheritRoomVisibility` is
// quiet: every level is still respected, and only the CONTAINED rows leak.
// So the constructors are checked for both, by name, rather than trusted
// to have been written correctly once.
func TestEveryFilterConstructorCarriesBothRules(t *testing.T) {
	if newRoomFilter().IncludeHighlySensitive {
		t.Error("the room filter admits highly sensitive rooms")
	}

	artifacts := newArtifactFilter()
	if artifacts.IncludeHighlySensitive {
		t.Error("the artifact filter admits highly sensitive artifacts")
	}
	if !artifacts.InheritRoomVisibility {
		t.Error("the artifact filter does not inherit room visibility; " +
			"a listing would return the contents of a withheld room")
	}

	memories := newMemoryFilter()
	if memories.IncludeHighlySensitive {
		t.Error("the memory filter admits highly sensitive memories")
	}
	if !memories.InheritRoomVisibility {
		t.Error("the memory filter does not inherit room visibility; " +
			"a listing would return memories about withheld artifacts")
	}
}

// TestFilterConstructorsStartUnscoped guards the other direction: a
// constructor that pre-set a room, a status or a page would make every
// handler silently narrower than it reads.
func TestFilterConstructorsStartUnscoped(t *testing.T) {
	a := newArtifactFilter()
	if a.RoomID != nil || a.Unfiled || a.Status != nil || a.Kind != nil ||
		a.Search != "" || a.Limit != 0 || a.Offset != 0 {
		t.Errorf("the artifact filter starts scoped: %+v", a)
	}
	m := newMemoryFilter()
	if m.RoomID != nil || m.Unfiled || m.ArtifactID != nil || m.Status != nil ||
		m.Kind != nil || m.MinImportance != 0 || m.Search != "" ||
		m.Limit != 0 || m.Offset != 0 {
		t.Errorf("the memory filter starts scoped: %+v", m)
	}
}

/* ── shaping ─────────────────────────────────────────────────────────── */

func TestAnExcerptDeclaresWhenItCut(t *testing.T) {
	short, truncated := excerpt("uma nota curta")
	if short != "uma nota curta" || truncated {
		t.Fatalf("short text: got %q truncated=%v", short, truncated)
	}

	long := strings.Repeat("a", excerptRunes+50)
	cut, truncated := excerpt(long)
	if !truncated {
		t.Fatal("a cut excerpt did not say it had been cut; a reader cannot tell " +
			"it apart from a very short record")
	}
	if len([]rune(cut)) > excerptRunes+1 { // +1 for the ellipsis
		t.Fatalf("excerpt is %d runes, want at most %d", len([]rune(cut)), excerptRunes+1)
	}
}

func TestAnExcerptCollapsesToOneLine(t *testing.T) {
	got, _ := excerpt("primeira linha\n\n   segunda   linha")
	if got != "primeira linha segunda linha" {
		t.Fatalf("got %q", got)
	}
}

func TestAnEmptyListingIsAnArrayAndNotNull(t *testing.T) {
	p := newPage[roomRow](nil, 0, 25, 0)
	if p.Items == nil {
		t.Fatal("items is nil; it would encode as null and every caller would need a guard")
	}
	if len(p.Items) != 0 {
		t.Fatalf("items has %d entries, want 0", len(p.Items))
	}
}

func TestANilReferenceEncodesAsAbsentAndNotAsAZeroID(t *testing.T) {
	if got := idString(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	id := uuid.New()
	got := idString(&id)
	if got == nil || *got != id.String() {
		t.Fatalf("got %v, want %s", got, id)
	}
}

/* ── the level rule ──────────────────────────────────────────────────── */

// TestTheLevelRuleMatchesTheDomainVocabulary walks the closed vocabulary
// rather than naming levels one by one, so a fourth level added to the
// domain fails here instead of being silently admitted.
func TestTheLevelRuleMatchesTheDomainVocabulary(t *testing.T) {
	for _, level := range domain.Sensitivities {
		row := roomRow{Sensitivity: string(level)}
		if row.Sensitivity == "" {
			t.Fatalf("level %q rendered empty", level)
		}
	}
	if len(domain.Sensitivities) != 3 {
		t.Fatalf("the sensitivity vocabulary has %d levels; this surface was designed "+
			"around three, and a new one needs a decision about whether it may be rendered",
			len(domain.Sensitivities))
	}
}
