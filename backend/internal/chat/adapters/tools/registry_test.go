package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── a stand-in for a tool the host supplies ─────────────────────────── */

// fakeTool stands in for the kind of tool that will arrive from outside
// this package — a GitHub-backed one from Integrations, a Finance-backed one
// from a module capability. It exists here to prove the seam is real:
// nothing in this package knows what it is, and the registry takes it
// anyway.
type fakeTool struct {
	name domain.ToolName
	run  func(ctx context.Context, args map[string]any) (domain.ToolOutput, error)
}

func (f fakeTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        f.name,
		Title:       "Fake",
		Description: "A tool supplied by the host binary.",
		Effect:      domain.EffectRead,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{"q": {Type: domain.TypeString}},
		},
	}
}

func (f fakeTool) Execute(ctx context.Context, args map[string]any) (domain.ToolOutput, error) {
	if f.run != nil {
		return f.run(ctx, args)
	}
	return domain.ToolOutput{"ok": true}, nil
}

/* ── the registry ────────────────────────────────────────────────────── */

func TestRegistryHoldsTheBuiltinTool(t *testing.T) {
	r, err := New(Options{Internal: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := r.Lookup("system.echo"); !ok {
		t.Fatal("system.echo is not registered")
	}
	defs := r.Definitions()
	if len(defs) != 1 {
		t.Fatalf("Definitions() = %d entries, want exactly the one built-in", len(defs))
	}
	if !defs[0].Internal {
		t.Error("system.echo must be marked internal: it is a test instrument, not a capability")
	}
}

// The lookup answers "does this exist", never "may this be used". A
// registered tool is available to the system and authorized for nobody.
func TestLookupOfAnUnknownNameFails(t *testing.T) {
	r, _ := New(Options{Internal: true})
	if _, ok := r.Lookup("github.repository.read"); ok {
		t.Fatal("a tool that is not in this build resolved")
	}
	if _, ok := r.Lookup(""); ok {
		t.Fatal("the empty name resolved")
	}
}

// A host-supplied tool is indistinguishable from a built-in one once
// registered, which is the property that lets the first real tool ship
// without Agents importing whoever implements it.
func TestHostSuppliedToolsAreRegistered(t *testing.T) {
	r, err := New(Options{Internal: true, Extra: []ports.Tool{fakeTool{name: "github.repository.read"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := r.Lookup("github.repository.read"); !ok {
		t.Fatal("the host-supplied tool did not register")
	}
	if len(r.Definitions()) != 2 {
		t.Fatalf("Definitions() = %d, want the built-in plus the supplied one", len(r.Definitions()))
	}
}

// A duplicate name refuses the whole registry rather than picking a winner.
// Silently keeping one of two tools with the same name means the running
// system offers a capability that is not the one anybody wired.
func TestDuplicateNamesRefuseTheRegistry(t *testing.T) {
	_, err := New(Options{Internal: true, Extra: []ports.Tool{fakeTool{name: "system.echo"}}})
	if err == nil {
		t.Fatal("a duplicate of the built-in name was accepted")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %q, want it to say what went wrong", err)
	}

	_, err = New(Options{Extra: []ports.Tool{fakeTool{name: "a.b"}, fakeTool{name: "a.b"}}})
	if err == nil {
		t.Fatal("two supplied tools with the same name were accepted")
	}
}

// A malformed definition refuses the registry for the same reason: half a
// registry is a deployment quietly serving fewer capabilities than it was
// configured with.
func TestAnInvalidDefinitionRefusesTheRegistry(t *testing.T) {
	if _, err := New(Options{Extra: []ports.Tool{fakeTool{name: "NotAValidName"}}}); err == nil {
		t.Fatal("a tool with an invalid name was registered")
	}
}

// Definitions() returns a copy, so a caller that sorts or truncates the
// result cannot reorder the catalogue every later turn reads.
func TestDefinitionsAreACopy(t *testing.T) {
	r, _ := New(Options{Extra: []ports.Tool{fakeTool{name: "a.b"}}})
	got := r.Definitions()
	got[0] = domain.ToolDefinition{Name: "vandalised"}
	if r.Definitions()[0].Name == "vandalised" {
		t.Fatal("mutating the returned slice changed the registry")
	}
}

// Name order, always. It reaches the model: the declaration array is built
// from this slice, and two identical turns must produce an identical body.
func TestDefinitionsAreInNameOrder(t *testing.T) {
	r, _ := New(Options{Extra: []ports.Tool{fakeTool{name: "z.last"}, fakeTool{name: "a.first"}}})
	defs := r.Definitions()
	for i := 1; i < len(defs); i++ {
		if defs[i-1].Name > defs[i].Name {
			t.Fatalf("definitions are not in name order: %q before %q", defs[i-1].Name, defs[i].Name)
		}
	}
}

/* ── the proof tool ──────────────────────────────────────────────────── */

// The whole point of system.echo is that it cannot fail for a reason of its
// own: no clock, no network, no randomness. Two identical calls are two
// identical results, which is what makes it usable as a fixture.
func TestEchoIsDeterministic(t *testing.T) {
	tool, _ := New(Options{Internal: true})
	echo, _ := tool.Lookup("system.echo")

	first, err := echo.Execute(context.Background(), map[string]any{"text": "olá"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	second, _ := echo.Execute(context.Background(), map[string]any{"text": "olá"})

	if first["text"] != "olá" {
		t.Fatalf("echo returned %v, want the input back", first["text"])
	}
	if first["text"] != second["text"] {
		t.Fatal("two identical calls produced different results")
	}
}

// A cancelled context does not make echo fail, and that is correct: it
// finishes instantly, and the cancellation that matters is enforced by the
// caller around every executor. The test exists so nobody "fixes" this into
// a ctx check the tool has no use for.
func TestEchoDoesNotInventItsOwnCancellation(t *testing.T) {
	r, _ := New(Options{Internal: true})
	echo, _ := r.Lookup("system.echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := echo.Execute(ctx, map[string]any{"text": "a"}); err != nil {
		t.Fatalf("Execute on a cancelled context = %v, want nil", err)
	}
}

// The declaration has to survive its own validator: a built-in that the
// registry would reject is a build that cannot start.
func TestEchoDefinitionIsValid(t *testing.T) {
	if err := (echoTool{}).Definition().Validate(); err != nil {
		t.Fatalf("the built-in definition is invalid: %v", err)
	}
}

// An executor that receives something the schema would never have produced
// reports a failure rather than panicking on a type assertion.
func TestEchoRefusesAnUnvalidatedArgument(t *testing.T) {
	r, _ := New(Options{Internal: true})
	echo, _ := r.Lookup("system.echo")
	_, err := echo.Execute(context.Background(), map[string]any{"text": 42})
	var f *domain.ToolFailure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *domain.ToolFailure", err, err)
	}
}

// Compile-time proof that the registry satisfies the port the application
// layer depends on.
var _ ports.ToolRegistry = (*Registry)(nil)
var _ ports.Tool = echoTool{}

/* ── internal tools are a registry decision ──────────────────────────── */

// The production shape: nothing configured, and the catalogue is EMPTY.
//
// This is the release gate for `system.echo`. It is not a display rule — a
// tool that is merely hidden is still lookupable, still authorizable, and
// still runnable by anyone who knows its name. Absent from the registry, it
// is none of those things, and every DENY rule already written covers it
// without being told.
func TestInternalToolsAreAbsentByDefault(t *testing.T) {
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if defs := r.Definitions(); len(defs) != 0 {
		t.Fatalf("the default catalogue has %d tools (%+v), want none: the only\n"+
			"built-in is a diagnostic and must not ship as a capability", len(defs), defs)
	}
	if _, ok := r.Lookup("system.echo"); ok {
		t.Fatal("system.echo resolves in a registry built without internal tools")
	}
}

// The flag admits exactly the internal ones, and changes nothing else.
func TestInternalFlagAdmitsOnlyTheDiagnostics(t *testing.T) {
	real := fakeTool{name: "github.repository.read"}

	off, _ := New(Options{Extra: []ports.Tool{real}})
	if len(off.Definitions()) != 1 {
		t.Fatalf("with internal off the catalogue is %+v, want only the real tool", off.Definitions())
	}
	if _, ok := off.Lookup("github.repository.read"); !ok {
		t.Fatal("a host-supplied capability was filtered as though it were a diagnostic")
	}

	on, _ := New(Options{Internal: true, Extra: []ports.Tool{real}})
	if len(on.Definitions()) != 2 {
		t.Fatalf("with internal on the catalogue is %+v, want both", on.Definitions())
	}
}

// The rule is read off the definition, never off a name. A hardcoded
// `system.echo` anywhere would be a rule enforced in one place and forgotten
// in the next.
func TestTheGateReadsTheDefinitionAndNotTheName(t *testing.T) {
	// A host-supplied tool that declares itself internal is filtered too,
	// even though this package has never heard of its name.
	internalExtra := internalFakeTool{fakeTool{name: "vendor.diagnostic"}}

	off, _ := New(Options{Extra: []ports.Tool{internalExtra}})
	if _, ok := off.Lookup("vendor.diagnostic"); ok {
		t.Fatal("a tool declaring itself internal survived a registry built without internal tools")
	}
	on, _ := New(Options{Internal: true, Extra: []ports.Tool{internalExtra}})
	if _, ok := on.Lookup("vendor.diagnostic"); !ok {
		t.Fatal("the flag did not admit a host-supplied internal tool")
	}
}

// internalFakeTool is a host-supplied tool that marks itself internal.
type internalFakeTool struct{ fakeTool }

func (f internalFakeTool) Definition() domain.ToolDefinition {
	d := f.fakeTool.Definition()
	d.Internal = true
	return d
}
