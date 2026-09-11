package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

// What can be asserted about the tools without a database.
//
// Their behaviour needs a service, a Postgres and a fake GitHub, and it is
// tested end to end in github_integration_test.go. What is here is
// everything that is a property of the DECLARATIONS — which is the part a
// deploy can break silently, because a bad definition is refused by the
// registry at start-up and a wrong one is not refused by anything.

// definitions builds the catalogue. Execute is never called, so a nil
// service is exactly the right dependency: it makes it impossible for a
// test in this file to accidentally become an integration test.
func definitions(t *testing.T) []chatdomain.ToolDefinition {
	t.Helper()
	tools := New(nil)
	out := make([]chatdomain.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Definition())
	}
	return out
}

func TestEveryToolIsAReadAndNoWriteCanEverBeRegistered(t *testing.T) {
	// The central claim of this version, asserted rather than asserted-in-a-
	// comment. If a write tool is ever added, this fails.
	defs := definitions(t)
	if len(defs) == 0 {
		t.Fatal("the integration registered no tools at all")
	}
	for _, d := range defs {
		if d.Effect != chatdomain.EffectRead {
			t.Fatalf("%s declares effect %q; this version is read-only", d.Name, d.Effect)
		}
	}
}

func TestNoToolNameSuggestsAWrite(t *testing.T) {
	// A second, independent check on the same claim. The effect field is
	// what the system enforces; the name is what a person reads in the
	// authorization screen, and the two must not be able to disagree.
	forbidden := []string{
		"create", "update", "delete", "write", "push", "merge", "close",
		"comment", "open", "edit", "remove", "set", "add", "dispatch",
	}
	for _, d := range definitions(t) {
		name := d.Name.String()
		for _, verb := range forbidden {
			if strings.Contains(name, verb) {
				t.Fatalf("%s contains the verb %q; this version performs no writes", name, verb)
			}
		}
	}
}

func TestEveryDefinitionIsValidAndFollowsTheNamingConvention(t *testing.T) {
	// The registry validates these at start-up and refuses the whole
	// catalogue on the first bad one, which would take the API down. Catch
	// it here instead of in production.
	for _, d := range definitions(t) {
		if err := d.Validate(); err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		if !strings.HasPrefix(d.Name.String(), "github.") {
			t.Fatalf("%s is not namespaced to the system it exposes", d.Name)
		}
		if d.Internal {
			t.Fatalf("%s is marked internal; these are product capabilities", d.Name)
		}
	}
}

func TestTitlesReadAsGitHubCapabilitiesInTheMenu(t *testing.T) {
	// The `@` menu renders the backend's title verbatim and invents
	// nothing. So "@ GitHub · Buscar código" is a property of THIS string,
	// and a frontend that grouped rows by parsing names would be the
	// parallel catalogue the batch forbids.
	for _, d := range definitions(t) {
		if !strings.HasPrefix(d.Title, "GitHub · ") {
			t.Fatalf("%s has title %q; the menu groups by this prefix", d.Name, d.Title)
		}
	}
}

func TestEveryRepositoryScopedToolTakesTheRepositoryAsARequiredArgument(t *testing.T) {
	// A repository-scoped tool with an optional repository would be a tool
	// that has to invent a default, and the only available default is
	// "whichever row comes first" — an answer about a repository nobody
	// named.
	scoped := map[string]bool{
		"github.commit.list":       true,
		"github.commit.get":        true,
		"github.file.get":          true,
		"github.pull_request.list": true,
		"github.pull_request.get":  true,
	}
	for _, d := range definitions(t) {
		if !scoped[d.Name.String()] {
			continue
		}
		if _, ok := d.Schema.Properties["repository"]; !ok {
			t.Fatalf("%s does not declare a repository", d.Name)
		}
		required := false
		for _, r := range d.Schema.Required {
			if r == "repository" {
				required = true
			}
		}
		if !required {
			t.Fatalf("%s does not require a repository", d.Name)
		}
	}
}

func TestEveryStringArgumentIsBounded(t *testing.T) {
	// An unbounded string property is a channel the model controls. The
	// whole-payload ceiling still applies, but a 16 KiB path is a 16 KiB
	// path in a URL and in an audit row.
	for _, d := range definitions(t) {
		for name, p := range d.Schema.Properties {
			if p.Type == chatdomain.TypeString && p.MaxLength == 0 {
				t.Fatalf("%s.%s is an unbounded string", d.Name, name)
			}
		}
	}
}

func TestTheCatalogueSurvivesTheRealRegistry(t *testing.T) {
	// The registry is what production builds, and it refuses a duplicate
	// name or a malformed definition by panicking at start-up. Building it
	// here means a wiring mistake fails a test rather than a deploy.
	//
	// Internal is false, which is the production answer: the seven tools
	// must be present in a catalogue that contains no diagnostics.
	reg, err := chattools.New(chattools.Options{Extra: New(nil)})
	if err != nil {
		t.Fatalf("the production registry refused the GitHub catalogue: %v", err)
	}
	got := reg.Definitions()
	if len(got) != len(definitions(t)) {
		t.Fatalf("registry has %d tools, the integration offers %d", len(got), len(definitions(t)))
	}
	for _, d := range got {
		if _, ok := reg.Lookup(d.Name); !ok {
			t.Fatalf("%s is in the catalogue and does not resolve to an executor", d.Name)
		}
	}
}

func TestAToolCallWithoutAWorkspaceFailsClosed(t *testing.T) {
	// There is no sensible default. Guessing would mean spending one
	// workspace's credential on another's request.
	for _, tool := range New(nil) {
		out, err := tool.Execute(context.Background(), map[string]any{
			"repository": "acme/website", "query": "x", "sha": "abc", "path": "a.go", "number": int64(1),
		})
		if err == nil {
			t.Fatalf("%s ran without a workspace and returned %v", tool.Definition().Name, out)
		}
		var f *chatdomain.ToolFailure
		if !asToolFailure(err, &f) {
			t.Fatalf("%s returned %v, want a tool failure", tool.Definition().Name, err)
		}
	}
}

func TestAWorkspaceInTheContextIsWhatTheToolReads(t *testing.T) {
	// The mechanism the tools depend on, asserted here so the dependency is
	// visible. That the CHAT module actually propagates this context is a
	// separate claim, asserted in chat's own suite.
	ws := uuid.New()
	got, err := workspaceOf(workspace.WithWorkspaceID(context.Background(), ws))
	if err != nil {
		t.Fatalf("workspaceOf: %v", err)
	}
	if got != ws {
		t.Fatalf("got %s, want %s", got, ws)
	}
	if _, err := workspaceOf(workspace.WithWorkspaceID(context.Background(), uuid.Nil)); err == nil {
		t.Fatal("the nil workspace was accepted")
	}
}

func asToolFailure(err error, target **chatdomain.ToolFailure) bool {
	f, ok := err.(*chatdomain.ToolFailure)
	if ok {
		*target = f
	}
	return ok
}

/* ── result shaping ──────────────────────────────────────────────────── */

func TestAResultThatFitsIsReturnedUnchanged(t *testing.T) {
	in := map[string]any{"commits": []ports.Commit{{SHA: "abc", Message: "hello"}}}
	out, err := fit(in, "commits")
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if _, cut := out["truncated"]; cut {
		t.Fatal("a small result was reported as truncated")
	}
}

func TestAnOversizeResultIsCutAndSaysSo(t *testing.T) {
	// The property that matters: a list that was cut and does not say so is
	// indistinguishable from a complete one, and the model would answer
	// "there are 4 commits" when there are four hundred.
	commits := make([]ports.Commit, 0, 400)
	for i := 0; i < 400; i++ {
		commits = append(commits, ports.Commit{
			SHA:     strings.Repeat("a", 40),
			Message: strings.Repeat("m", domain.MaxCommitMessage),
		})
	}
	out, err := fit(map[string]any{"commits": commits}, "commits")
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	raw, _ := json.Marshal(out)
	if len(raw) > domain.MaxToolResultBytes {
		t.Fatalf("result is %d bytes, above the %d budget", len(raw), domain.MaxToolResultBytes)
	}
	if out["truncated"] != true {
		t.Fatal("the result was cut and does not declare it")
	}
	if _, ok := out["truncated_note"]; !ok {
		t.Fatal("the truncation carries no instruction the model can act on")
	}
	kept, ok := out["commits"].([]any)
	if !ok || len(kept) == 0 || len(kept) >= len(commits) {
		t.Fatalf("commits were not actually reduced: %T", out["commits"])
	}
}

func TestTheGitHubBudgetSitsBelowTheOneAgentsEnforces(t *testing.T) {
	// If these were equal, a payload that measured exactly at the budget
	// would be refused by Agents as an execution failure — which the model
	// reads as "the tool is broken" rather than "ask for less". The gap is
	// what keeps the honest, smaller answer reachable.
	if domain.MaxToolResultBytes >= chatdomain.MaxToolResultBytes {
		t.Fatalf("the GitHub budget (%d) is not below the Agents ceiling (%d)",
			domain.MaxToolResultBytes, chatdomain.MaxToolResultBytes)
	}
}

func TestAPayloadWithNothingToShrinkIsRefusedRatherThanOversized(t *testing.T) {
	// Handing Agents an oversize payload would produce its generic
	// "returned more data than one turn may carry" failure. Refusing here
	// produces one that says what to do instead.
	huge := map[string]any{"file": strings.Repeat("x", domain.MaxToolResultBytes+1000)}
	if _, err := fit(huge, ""); err == nil {
		t.Fatal("an oversize payload with no list was returned")
	}
}
