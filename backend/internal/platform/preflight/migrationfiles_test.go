package preflight_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The runner's own rule, copied from golang-migrate source/parse.go:
//
//	^([0-9]+)_(.*)\.(down|up)\.(.*)$
//
// The extension group is `(.*)`, which swallows ANY suffix. That is the
// whole trap: a file parked in a migrations directory with a reassuring
// name is still a migration.
var migrationName = regexp.MustCompile(`^([0-9]+)_(.*)\.(down|up)\.(.*)$`)

// TestNothingInMigrationsIsSecretlyAMigration.
//
// ── The incident ───────────────────────────────────────────────────────
// A prepared but UNAUTHORIZED release was parked at
// `migrations/releases/0012_agents_v14.up.sql.candidate`, on the
// assumption that the suffix made it inert. It did not: the runner read
// direction `up` and extension `sql.candidate`, and the gate's disposable
// database published an Agents release nobody had approved. The dev
// database hid it, because migrations had already run there.
//
// The rule this pins is narrow and total: inside a migrations directory,
// a file that parses as a migration must end in `.sql`. Anything else is
// either a mistake or a file that belongs somewhere the runner never
// looks.
func TestNothingInMigrationsIsSecretlyAMigration(t *testing.T) {
	root := filepath.Join("..", "..", "..", "migrations")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("migrations directory not reachable from here: %v", err)
	}

	var offenders []string
	var seen int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		seen++
		m := migrationName.FindStringSubmatch(d.Name())
		if m == nil {
			// Does not parse as a migration at all. Harmless: the runner
			// skips it. A README may live here.
			return nil
		}
		if ext := m[4]; ext != "sql" {
			offenders = append(offenders, path+" (runner would apply it as a "+m[3]+" migration with extension "+ext+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if seen == 0 {
		t.Fatal("walked the migrations tree and found no files; the path is wrong and this test proves nothing")
	}
	if len(offenders) > 0 {
		t.Fatalf("files the runner would execute but that are not plain .sql:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
