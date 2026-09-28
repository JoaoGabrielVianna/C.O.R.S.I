// Package internal_test holds the architecture test for this repository.
//
// # What this enforces
//
// The modular monolith in ./internal is split into bounded contexts, one per
// directory, with internal/integrations/<name> counting as its own context
// rather than as a shared "integrations" one. The dependency rule is:
//
//	Module → Platform          allowed
//	Integration → Platform     allowed
//	Module → Integration       forbidden (wired in cmd/corsi instead)
//	Integration → Module       forbidden
//	Platform → anything        forbidden
//	Module → Module            forbidden
//
// Until this file existed the rule was documented and followed by hand. It
// was never enforced: Go's import cycle check does not care about layering,
// and nothing else looked. The claim "bounded contexts do not import each
// other" was true about intent and unverified about the build. This test is
// what turns it into a property the compiler cannot silently lose.
//
// The check parses every .go file under ./internal with go/parser in
// ImportsOnly mode, maps each file and each internal import to its context,
// and fails on any cross-context edge not declared in allowances below.
//
// Test files are scanned too, under their own scope. That is not incidental:
// most of this repository's integration tests carry a //go:build integration
// tag, so a tool that resolves packages under the default build constraints
// never loads them and reports a tidier graph than the one that exists.
// Parsing the source directly sees them. An import in a _test.go file is
// still code in this repository reaching into another context's internals.
//
// # What this deliberately does not enforce
//
// cmd/corsi is not scanned. It is the composition root: it imports every
// module, every integration and platform in order to wire them together, and
// that is the single place where doing so is correct. A test that scanned it
// would need an allowance so broad it would prove nothing. Keeping the scan
// scoped to ./internal is what lets the allowance list stay readable.
//
// This also says nothing about layering *inside* a context — domain not
// importing adapters, and so on. That is a separate property.
//
// # Debt 1: the tool contract lives in the wrong module (production)
//
// Six contexts import chat/domain and chat/ports. Those edges are real
// Module → Module dependencies and they are allowed here, explicitly, rather
// than hidden by loosening the rule. They are the only cross-context imports
// in production code in the entire tree.
//
// The reason they exist is that internal/chat is two things at once: the
// Agents module, and the owner of the tool contract (ports.Tool,
// domain.ToolDefinition, domain.ToolSchema, the receipt types) that every
// other context must implement in order to expose capabilities to an agent.
// Finance does not depend on Agents because it wants to talk to Agents; it
// depends on Agents because that is where the interface it implements
// happens to be declared.
//
// Decision, taken and deferred on purpose: the tool contract belongs in
// platform, as platform/toolkit. Platform is already a legal dependency for
// every module and every integration, so moving it closes these edges
// without a single new allowance, and the production rule becomes
// exception-free.
//
// It is not being done in this change, and the reason is not that nobody got
// to it. chat/domain currently mixes the contract (ToolDefinition, Tool,
// Confidential, External, WriteReceipt, ReadReceipt) with conversation state
// (Message, ContextBlock, FinishReason, TurnReference), and the consuming
// contexts reach for symbols on both sides of that line. Splitting it is a
// domain decision about which of those types are contract and which are
// Agents' own state, not a mechanical file move. Done badly it leaves a
// platform package holding conversation concepts, which is a worse shape
// than the one it replaces.
//
// # Debt 2: integration tests compose another context's runtime
//
// Every cross-context import that is not the two above is in a _test.go
// file, and nearly all of them are one pattern: a module's integration suite
// builds a real chat runtime — app service, repo, reference adapters, the
// LLM adapter, sometimes the HTTP layer — and drives a scripted model
// through an actual turn to prove its own capabilities are reachable by an
// agent end to end. The metathreads suite goes further and composes the
// threads module too, because the property under test is precisely that an
// agent holding both does not confuse our Threads with Meta's.
//
// These are the most valuable tests in the repository and the coupling is
// the point of them, so they are allowed. But they are misfiled, and that is
// the actual finding: a test that composes three contexts is not a test of
// any one of them. It belongs to the composition root, in a suite that sits
// outside every context — the same reason cmd/corsi is allowed to import
// everything. Moving them is a larger change than this one and is not on the
// critical path; naming the edges here bounds the debt in the meantime.
//
// Both debts are listed by exact package, never by context or prefix. An
// allowance for chat/domain does not permit chat/app. A new dependency on a
// part of another context that nobody has already justified fails here, even
// when that context is one this test already lets through.
//
// # Why stdlib only
//
// go/parser and os are enough. golang.org/x/tools/go/packages would do the
// same job, add a dependency tree to a module whose point is controlling its
// dependencies, and — as noted above — would have to be told about build
// tags to see what this sees.
package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// importPrefix is the module path of everything this test reasons about.
// Imports that do not start with it are stdlib or third party and out of
// scope.
const importPrefix = "github.com/corsi/backend/internal/"

// sharedContext may be imported by every other context. It is the bottom of
// the dependency graph and imports no context but itself;
// TestPlatformDependsOnNoModule is what holds that down.
const sharedContext = "platform"

// scope distinguishes an edge that ships in the binary from one that exists
// only to test something. Both are checked; they are allowed separately, so
// that a test helper's reach cannot quietly become a production dependency.
type scope int

const (
	scopeProduction scope = iota
	scopeTest
)

func (s scope) String() string {
	if s == scopeTest {
		return "test"
	}
	return "production"
}

// allowance is one declared exception to the dependency rule.
//
// from is a context name, or "*" for any context. Each entry in to is an
// exact package path relative to internal/ — never a context name and never
// a prefix. That is the teeth of this test: allowing "chat/domain" does not
// allow "chat/app".
//
// why is required. An allowance without a stated reason is how a rule turns
// back into a habit.
type allowance struct {
	from  string
	to    []string
	scope scope
	why   string
}

var allowances = []allowance{
	// ---------------------------------------------------------------
	// DEBT 1 — the tool contract lives in internal/chat instead of
	// internal/platform. See the package doc for the decision and for
	// why it is deferred rather than merely pending.
	//
	// These are the only cross-context imports in production code.
	// When the contract moves to platform/toolkit this entry is
	// deleted, not edited.
	// ---------------------------------------------------------------
	{
		from:  "*",
		to:    []string{"chat/domain", "chat/ports"},
		scope: scopeProduction,
		why:   "tool contract (ports.Tool, ToolDefinition, ToolSchema, receipts) is declared in the Agents module; target is platform/toolkit",
	},

	// ---------------------------------------------------------------
	// DEBT 2 — integration suites compose a real chat runtime to prove
	// their own capabilities are reachable through an actual agent
	// turn. Valuable tests, wrong location: they belong to the
	// composition root, not to the context they happen to live in.
	// ---------------------------------------------------------------
	{
		from: "*",
		to: []string{
			"chat/app",
			"chat/domain",
			"chat/ports",
			"chat/adapters/tools",
			"chat/adapters/repo",
			"chat/adapters/references",
			"chat/adapters/llm",
			"chat/adapters/httpapi",
		},
		scope: scopeTest,
		why:   "integration suites drive a scripted model through a real turn to prove a capability is reachable end to end; these tests belong in a composition-root suite",
	},
	{
		from: "integrations/metathreads",
		to: []string{
			"threads/tools",
			"threads/ports",
			"threads/app",
			"threads/adapters/repo",
		},
		scope: scopeTest,
		why:   "the grounding suite gives one agent both Meta Threads and our Threads on purpose: the property under test is that it does not confuse external evidence with internal drafts",
	},
}

// TestBoundedContextsDoNotImportEachOther is the enforcement.
func TestBoundedContextsDoNotImportEachOther(t *testing.T) {
	edges := scanInternal(t)

	used := make(map[string]bool, len(allowances))
	var violations []string

	for _, e := range edges {
		if e.fromContext == e.toContext {
			continue
		}
		if e.toContext == sharedContext {
			continue
		}
		if key, ok := matchAllowance(e); ok {
			used[key] = true
			continue
		}
		violations = append(violations, e.String())
	}

	if len(violations) > 0 {
		violations = dedupe(violations)
		t.Errorf(
			"%d disallowed cross-context import(s):\n\n%s\n\n"+
				"A bounded context may import only itself and %q. If an edge is\n"+
				"intentional it needs an entry in allowances with a stated reason, and\n"+
				"that entry is a decision about the architecture rather than a way to\n"+
				"make this test pass. If it is not intentional, invert it: declare the\n"+
				"interface in the consuming context, or move the shared type into\n"+
				"internal/%s.",
			len(violations),
			strings.Join(violations, "\n"),
			sharedContext,
			sharedContext,
		)
	}

	// An allowance that no longer matches any import is an exception
	// nobody is taking. Failing on it is what makes the debt shrink
	// instead of accumulate: the coupling cannot be removed without this
	// test insisting the permission be removed too.
	t.Run("no stale allowances", func(t *testing.T) {
		for i, a := range allowances {
			for _, to := range a.to {
				if !used[allowanceKey(i, to)] {
					t.Errorf(
						"allowance %q -> %q (%s) matches no import in the tree.\n"+
							"The coupling it permitted is gone. Delete the target.",
						a.from, to, a.scope,
					)
				}
			}
		}
	})

	// A rule with an undocumented exception is a rule with a hole in it.
	t.Run("every allowance states a reason", func(t *testing.T) {
		for _, a := range allowances {
			if strings.TrimSpace(a.why) == "" {
				t.Errorf("allowance from %q (%s) has no stated reason", a.from, a.scope)
			}
			if len(a.to) == 0 {
				t.Errorf("allowance from %q (%s) permits nothing; delete it", a.from, a.scope)
			}
		}
	})
}

// TestPlatformDependsOnNoModule states the half of the rule that the main
// case would pass vacuously today. Platform is the bottom of the graph, so
// it currently has no outgoing cross-context edge at all and the loop above
// never reaches a decision about it. If platform ever imports a module that
// is a layering inversion serious enough to name on its own rather than read
// out of a generic violation list.
func TestPlatformDependsOnNoModule(t *testing.T) {
	for _, e := range scanInternal(t) {
		if e.fromContext != sharedContext || e.toContext == sharedContext {
			continue
		}
		t.Errorf(
			"platform imports a bounded context: %s\n"+
				"Platform is shared infrastructure; depending on a module inverts the\n"+
				"layering and makes the module impossible to extract. Whatever platform\n"+
				"needs here belongs behind an interface platform declares and the module\n"+
				"implements.",
			e,
		)
	}
}

// importEdge is one import statement, resolved to contexts.
type importEdge struct {
	file        string // path relative to internal/
	fromContext string
	toPackage   string // path relative to internal/
	toContext   string
	scope       scope
}

func (e importEdge) String() string {
	return "  " + e.fromContext + " -> " + e.toPackage + "  (" + e.scope.String() + ": " + e.file + ")"
}

func allowanceKey(i int, to string) string { return strconv.Itoa(i) + "\x00" + to }

// matchAllowance reports whether e is declared, and which allowance target
// covered it so that staleness can be tracked per target rather than per
// entry.
func matchAllowance(e importEdge) (string, bool) {
	for i, a := range allowances {
		if a.scope != e.scope {
			continue
		}
		if a.from != "*" && a.from != e.fromContext {
			continue
		}
		for _, to := range a.to {
			if to == e.toPackage {
				return allowanceKey(i, to), true
			}
		}
	}
	return "", false
}

// scanInternal parses every .go file under the directory this test lives in
// and returns each import that points back into internal/.
//
// The test binary runs with its own package directory as the working
// directory, which is internal/ itself, so "." is the tree to walk. Parsing
// rather than building is what makes build-tagged files visible.
func scanInternal(t *testing.T) []importEdge {
	t.Helper()

	fset := token.NewFileSet()
	var edges []importEdge

	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}

		rel := filepath.ToSlash(p)
		from := contextOf(path.Dir(rel))
		if from == "" {
			// A .go file sitting directly in internal/ belongs to no
			// context. This file is the only one.
			return nil
		}

		sc := scopeProduction
		if strings.HasSuffix(rel, "_test.go") {
			sc = scopeTest
		}

		f, parseErr := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}

		for _, spec := range f.Imports {
			ip, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				continue
			}
			if !strings.HasPrefix(ip, importPrefix) {
				continue
			}
			target := strings.TrimPrefix(ip, importPrefix)
			edges = append(edges, importEdge{
				file:        rel,
				fromContext: from,
				toPackage:   target,
				toContext:   contextOf(target),
				scope:       sc,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	// A scan that silently found nothing would report a clean
	// architecture forever, including after someone moves this file
	// somewhere it cannot see the tree.
	if len(edges) == 0 {
		wd, _ := os.Getwd()
		t.Fatalf("found no internal imports at all under %s; the scan is not looking at the source tree", wd)
	}

	return edges
}

// contextOf maps a package path relative to internal/ to the bounded context
// that owns it. It takes a package path, never a file path: callers holding
// a file pass path.Dir of it.
//
// internal/integrations is a namespace, not a context: github and
// metathreads are separate contexts that must not import each other, so the
// second path element is part of the name.
//
// It returns "" for a path that names no context — internal/ itself, where
// this file lives, and the bare "integrations" namespace, which holds no
// package of its own.
func contextOf(pkgRel string) string {
	pkgRel = path.Clean(filepath.ToSlash(pkgRel))
	if pkgRel == "." || pkgRel == "/" || pkgRel == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(pkgRel, "/"), "/")
	if parts[0] == "integrations" {
		if len(parts) < 2 {
			return ""
		}
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
