//go:build integration

// The shape of a stored snapshot.
//
// ── Why this test exists ───────────────────────────────────────────────
// A release snapshot is written by a migration, as a JSONB literal, and
// read back by a Go decoder with typed fields. Nothing in between checks
// that the two agree. When they do not, the failure is silent and total:
// `json.Unmarshal` fills a struct with zero values, the API returns rows of
// empty strings, and the page renders blank sections for a release that
// looks fine in the database.
//
// That already happened. Six of the eight releases published before this
// test existed carry `{name, note}` where the reader expects `{label,
// value}` or `{text, ref}`, and 60 items across them render empty. Those
// rows are immutable by trigger, so they cannot be corrected — which makes
// a ratchet the only remaining response.
//
// ── What it enforces ───────────────────────────────────────────────────
// Every release outside the documented legacy set must match the decoder's
// vocabulary exactly: required keys present and non-blank, no unexpected
// keys, every value a non-empty string.
package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// snapshotField is one array column and the vocabulary its decoder speaks.
type snapshotField struct {
	column   string
	required []string
	optional []string
}

// The six snapshot columns, and the exact keys domain.Release decodes.
//
//	Capability     {name, note?}
//	Metric         {label, value}
//	Note           {text, ref?}
//	DocRef         {label, path}
var snapshotFields = []snapshotField{
	{"capabilities", []string{"name"}, []string{"note"}},
	{"evidence", []string{"label", "value"}, nil},
	{"limitations", []string{"text"}, []string{"ref"}},
	{"decisions", []string{"text"}, []string{"ref"}},
	{"technical_notes", []string{"text"}, []string{"ref"}},
	{"doc_refs", []string{"label", "path"}, nil},
}

// legacyMalformed is the closed set of rows that predate this rule.
//
// ── Why an allowlist and not a fix ─────────────────────────────────────
// These rows are published, and a published release is immutable by
// database trigger. Correcting them would mean disabling the freeze, which
// is the one thing the release history exists to make impossible: a record
// that can be tidied up is not a record. So the debt is named, bounded, and
// prevented from growing.
//
// Adding an entry here is not a way to pass this test. It is a statement
// that a release shipped with a snapshot that renders blank, and it needs
// the same justification any other permanent defect does.
var legacyMalformed = map[string]bool{
	"agents/1.1.0":    true,
	"agents/1.1.1":    true,
	"agents/1.2.0":    true,
	"agents/1.3.0":    true,
	"job-radar/1.0.0": true,
	"threads/1.0.0":   true,
}

// TestEverySnapshotMatchesTheDecodersVocabulary walks every stored release
// and checks each snapshot array against the keys its Go type declares.
func TestEverySnapshotMatchesTheDecodersVocabulary(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rows, err := e.pool.Query(ctx, `SELECT module_key, version FROM releases.releases
	                                ORDER BY module_key, major, minor, patch`)
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	type ref struct{ module, version string }
	var all []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.module, &r.version); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all = append(all, r)
	}
	rows.Close()
	if len(all) == 0 {
		t.Fatal("no releases in the database; this test would pass vacuously")
	}

	checked, skipped := 0, 0
	for _, r := range all {
		key := r.module + "/" + r.version
		if legacyMalformed[key] {
			skipped++
			continue
		}
		checked++
		for _, f := range snapshotFields {
			var raw []byte
			err := e.pool.QueryRow(ctx,
				`SELECT `+f.column+`::text FROM releases.releases
				 WHERE module_key = $1 AND version = $2`, r.module, r.version).Scan(&raw)
			if err != nil {
				t.Fatalf("%s %s: %v", key, f.column, err)
			}
			for _, problem := range validateSnapshotArray(raw, f) {
				t.Errorf("%s %s: %s", key, f.column, problem)
			}
		}
	}
	t.Logf("checked %d releases against the decoder's vocabulary; %d legacy rows skipped",
		checked, skipped)

	// The allowlist must not outlive the rows it describes. An entry naming
	// a release that no longer exists is a rule nobody can evaluate.
	present := map[string]bool{}
	for _, r := range all {
		present[r.module+"/"+r.version] = true
	}
	for key := range legacyMalformed {
		if !present[key] {
			t.Errorf("legacyMalformed names %q, which is not in the database; "+
				"remove the entry rather than leaving a rule about nothing", key)
		}
	}
}

// validateSnapshotArray returns every problem found, rather than the first.
// A snapshot with four malformed entries should report four.
func validateSnapshotArray(raw []byte, f snapshotField) []string {
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return []string{"not a JSON array of objects: " + err.Error()}
	}

	allowed := map[string]bool{}
	for _, k := range f.required {
		allowed[k] = true
	}
	for _, k := range f.optional {
		allowed[k] = true
	}

	var problems []string
	for i, it := range items {
		for _, k := range f.required {
			v, ok := it[k]
			if !ok {
				problems = append(problems, itemf(i, "missing required key %q", k))
				continue
			}
			s, isString := v.(string)
			if !isString {
				problems = append(problems, itemf(i, "key %q is not a string", k))
				continue
			}
			if trimmed(s) == "" {
				problems = append(problems, itemf(i, "required key %q is blank", k))
			}
		}
		var unexpected []string
		for k, v := range it {
			if !allowed[k] {
				unexpected = append(unexpected, k)
				continue
			}
			// An optional key that is present must still be usable.
			if s, ok := v.(string); ok && trimmed(s) == "" {
				problems = append(problems, itemf(i, "key %q is present and blank", k))
			}
		}
		if len(unexpected) > 0 {
			sort.Strings(unexpected)
			problems = append(problems, itemf(i,
				"unexpected keys %v — the decoder drops them and the item renders empty",
				unexpected))
		}
	}
	return problems
}

func itemf(i int, format string, args ...any) string {
	return "item " + strconv.Itoa(i) + ": " + fmt.Sprintf(format, args...)
}

func trimmed(s string) string { return strings.TrimSpace(s) }
