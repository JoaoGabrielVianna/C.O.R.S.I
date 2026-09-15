package palace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/palace/tools"
)

// The catalogue, checked without a database.
//
// Everything here is a property of the DEFINITIONS: how many there are,
// what they are called, what they declare about themselves. None of it
// needs Postgres, so none of it is behind the integration tag: a
// capability that declared itself External, or forgot Confidential,
// should fail the fast gate rather than waiting for docker.

// expectedEffects is the whole catalogue, written out.
//
// ── Why the table is spelled here and not derived ──────────────────────
// Deriving the effects from the definitions would prove only that the
// definitions agree with themselves. Written out, this is a second
// statement of what each capability DOES, and the two have to match: a
// read that quietly became a write, or a write mislabelled as a read,
// fails here. The effect is what the interface shows an operator before
// they authorize something, and what the write receipt counts.
var expectedEffects = map[chatdomain.ToolName]chatdomain.ToolEffect{
	tools.RoomListTool:   chatdomain.EffectRead,
	tools.RoomCreateTool: chatdomain.EffectWrite,
	tools.RoomUpdateTool: chatdomain.EffectWrite,

	tools.ArtifactListTool:   chatdomain.EffectRead,
	tools.ArtifactGetTool:    chatdomain.EffectRead,
	tools.ArtifactCreateTool: chatdomain.EffectWrite,
	tools.ArtifactUpdateTool: chatdomain.EffectWrite,

	tools.ItemAddTool:    chatdomain.EffectWrite,
	tools.ItemUpdateTool: chatdomain.EffectWrite,
	tools.ItemRemoveTool: chatdomain.EffectWrite,

	tools.MemoryListTool:   chatdomain.EffectRead,
	tools.MemoryGetTool:    chatdomain.EffectRead,
	tools.MemoryCreateTool: chatdomain.EffectWrite,
	tools.MemoryUpdateTool: chatdomain.EffectWrite,

	tools.SourceCreateTool: chatdomain.EffectWrite,
	tools.SourceGetTool:    chatdomain.EffectRead,

	// Provenance: a write, because it changes what a record rests on. It
	// creates neither end and edits neither.
	tools.MemoryLinkSourceTool: chatdomain.EffectWrite,

	tools.RelationCreateTool: chatdomain.EffectWrite,
	tools.RelationListTool:   chatdomain.EffectRead,
	tools.RelationRemoveTool: chatdomain.EffectWrite,

	tools.SessionGetTool:   chatdomain.EffectRead,
	tools.SessionStartTool: chatdomain.EffectWrite,
	tools.SessionFocusTool: chatdomain.EffectWrite,
	tools.SessionCloseTool: chatdomain.EffectWrite,
}

// paletteTools builds the catalogue without a database.
//
// The tools hold an application service they never call here: every
// assertion below reads a Definition, which touches nothing.
func paletteTools() []chatdomain.ToolDefinition {
	built := tools.New(nil)
	defs := make([]chatdomain.ToolDefinition, 0, len(built))
	for _, t := range built {
		defs = append(defs, t.Definition())
	}
	return defs
}

func TestThereAreExactlyTwentyFourPalaceCapabilities(t *testing.T) {
	// Twenty-four is the normative count of Palace Core v1. It went from
	// twenty-three when provenance gained the only operation that could
	// reach it: `palace.source.create` produced evidence that nothing
	// could cite, which made two capabilities structurally useless.
	const want = 24
	defs := paletteTools()
	if len(defs) != want {
		t.Fatalf("the catalogue has %d capabilities, want %d", len(defs), want)
	}
	if len(expectedEffects) != want {
		t.Fatalf("this test's table has %d entries, want %d", len(expectedEffects), want)
	}
	if got := len(tools.Capabilities()); got != want {
		t.Fatalf("the agent blueprint names %d capabilities, want %d", got, want)
	}
}

func TestEveryCapabilityIsNamedOnceAndWellFormed(t *testing.T) {
	seen := map[chatdomain.ToolName]bool{}
	for _, def := range paletteTools() {
		if seen[def.Name] {
			t.Errorf("%q is declared twice", def.Name)
		}
		seen[def.Name] = true

		if !chatdomain.ValidToolName(string(def.Name)) {
			t.Errorf("%q is not a valid tool name", def.Name)
		}
		if !strings.HasPrefix(string(def.Name), "palace.") {
			t.Errorf("%q is not in the palace namespace", def.Name)
		}
		if def.Name.Namespace() != "palace" {
			t.Errorf("%q resolves to namespace %q", def.Name, def.Name.Namespace())
		}
		// The registry refuses a malformed definition at construction; this
		// says so before the registry is even built.
		if err := def.Validate(); err != nil {
			t.Errorf("%q is not a valid definition: %v", def.Name, err)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	EVERY PALACE CAPABILITY IS Confidential. NONE IS External
//
// ══════════════════════════════════════════════════════════════════════
func TestEveryCapabilityIsConfidentialAndInternal(t *testing.T) {
	for _, def := range paletteTools() {
		if !def.Confidential {
			t.Errorf("%q is not Confidential; its arguments and result would be kept "+
				"in the audit trail in the clear", def.Name)
		}
		if def.External {
			t.Errorf("%q declares itself External; it reads our own database, and saying "+
				"otherwise would let a Palace read make a turn VERIFIED_EXTERNAL_READ", def.Name)
		}
		if def.Internal {
			t.Errorf("%q declares itself Internal, which would keep it out of a production "+
				"catalogue", def.Name)
		}
	}
}

func TestEveryCapabilityDeclaresTheEffectItActuallyHas(t *testing.T) {
	for _, def := range paletteTools() {
		want, known := expectedEffects[def.Name]
		if !known {
			t.Errorf("%q is registered and not in this test's table; add it deliberately", def.Name)
			continue
		}
		if def.Effect != want {
			t.Errorf("%q declares %q, want %q", def.Name, def.Effect, want)
		}
	}
	for name := range expectedEffects {
		found := false
		for _, def := range paletteTools() {
			if def.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is in this test's table and not registered", name)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	NO CAPABILITY TAKES A WORKSPACE
//
// ══════════════════════════════════════════════════════════════════════
//
// Not an optional one, not one that defaults. The model has no way to
// express a workspace and therefore no way to be wrong about it: the
// value comes from the context the platform middleware stamped.
func TestNoCapabilityAcceptsAWorkspaceArgument(t *testing.T) {
	for _, def := range paletteTools() {
		for _, prop := range def.Schema.PropertyNames() {
			lowered := strings.ToLower(prop)
			if strings.Contains(lowered, "workspace") || lowered == "tenant" || lowered == "account_id" {
				t.Errorf("%q accepts %q; the workspace comes from the context and nowhere else",
					def.Name, prop)
			}
		}
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	NO CAPABILITY SMUGGLES STRUCTURE THROUGH A STRING
//
// ══════════════════════════════════════════════════════════════════════
//
// The schema this platform validates is a flat object of scalars. A
// capability that wanted to accept a list would have to encode one inside
// a string property, and the schema shown to the model would then
// describe a contract the validator does not enforce. A declared shape we
// do not check is a contract we do not have, so entries are unitary
// instead. See items.go.
func TestNoCapabilityAsksForJSONInsideAString(t *testing.T) {
	for _, def := range paletteTools() {
		for name, prop := range def.Schema.Properties {
			if prop.Type != chatdomain.TypeString {
				continue
			}
			lowered := strings.ToLower(prop.Description)
			for _, smell := range []string{"json", "comma-separated", "comma separated", "array of"} {
				if strings.Contains(lowered, smell) {
					t.Errorf("%q: property %q describes %q, which would be structure "+
						"encoded inside a scalar", def.Name, name, smell)
				}
			}
		}
	}
}

func TestEveryCapabilityTellsTheModelWhatItIsFor(t *testing.T) {
	for _, def := range paletteTools() {
		if len(def.Description) < 80 {
			t.Errorf("%q has a %d character description; it is what the model reads to "+
				"decide whether to call it", def.Name, len(def.Description))
		}
		if strings.TrimSpace(def.Title) == "" {
			t.Errorf("%q has no title", def.Name)
		}
		// Every write earns the notice the platform appends, which is how a
		// model learns which capabilities change something.
		declared := def.DeclaredDescription()
		if def.Effect == chatdomain.EffectWrite && !strings.Contains(declared, "CHANGES data") {
			t.Errorf("%q is a write and its declared description carries no write notice", def.Name)
		}
		if strings.Contains(declared, "OUTSIDE this product") {
			t.Errorf("%q is declared as reading an external system", def.Name)
		}
	}
}

/* ── the agent blueprint ─────────────────────────────────────────────── */

func TestTheAgentBlueprintNamesExactlyTheRegisteredCatalogue(t *testing.T) {
	// In both directions. A capability without a grant would be one the
	// agent can see and never use; a grant without a capability would be a
	// row the registry refuses to resolve.
	registered := map[chatdomain.ToolName]bool{}
	for _, def := range paletteTools() {
		registered[def.Name] = true
	}

	granted := map[chatdomain.ToolName]bool{}
	for _, name := range tools.Capabilities() {
		if granted[name] {
			t.Errorf("the blueprint names %q twice", name)
		}
		granted[name] = true
		if !registered[name] {
			t.Errorf("the blueprint grants %q, which is not registered", name)
		}
	}
	for name := range registered {
		if !granted[name] {
			t.Errorf("%q is registered and the blueprint does not grant it", name)
		}
	}
}

func TestTheAgentBlueprintGrantsNothingOutsidePalace(t *testing.T) {
	for _, name := range tools.Capabilities() {
		if name.Namespace() != "palace" {
			t.Errorf("the blueprint grants %q, which belongs to %q", name, name.Namespace())
		}
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE INSTRUCTIONS CARRY NO STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// A count written into a prompt is the same failure as one invented by
// the model, with a different author: it ages, it reads as
// authoritative, and no capability was called to produce it. The rule is
// the platform's and Palace inherits it.
func TestTheAgentInstructionsCarryNoCountsOrSnapshots(t *testing.T) {
	instructions := tools.AgentInstructions

	if strings.TrimSpace(instructions) == "" {
		t.Fatal("the agent has no instructions")
	}
	for _, digit := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		if strings.Contains(instructions, digit) {
			t.Errorf("the instructions contain the digit %q; a number in a prompt is a fact "+
				"that ages. State the rule, not the count", digit)
		}
	}
	// And it must not teach a heuristic for deciding what to keep: the
	// user decides, and consolidation is a later, asked-for step.
	if !strings.Contains(instructions, "Do NOT write because a conversation touched on") {
		t.Error("the instructions no longer tell the model not to write on its own initiative")
	}
	if !strings.Contains(instructions, "fabricated source") {
		t.Error("the instructions no longer warn against fabricating evidence")
	}
}

/* ── the registry ────────────────────────────────────────────────────── */

func TestThePalaceCatalogueBuildsAProductionRegistry(t *testing.T) {
	// Built exactly as the composition root builds it: Internal off, so
	// this is the catalogue a production deployment gets.
	registry, err := chattools.New(chattools.Options{Extra: tools.New(nil)})
	if err != nil {
		t.Fatalf("the registry refused the Palace catalogue: %v", err)
	}

	palace := 0
	for _, def := range registry.Definitions() {
		if def.Name.Namespace() == "palace" {
			palace++
		}
	}
	if palace != 24 {
		t.Errorf("%d Palace capabilities reached the registry, want 24", palace)
	}
	for _, name := range tools.Capabilities() {
		if _, ok := registry.Lookup(name); !ok {
			t.Errorf("%q is not resolvable in the registry, so a grant for it would 404", name)
		}
	}
}

/* ── the constructor ─────────────────────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	AN INCOMPLETE Deps IS REFUSED AT CONSTRUCTION
//
// ══════════════════════════════════════════════════════════════════════
//
// The failure this closes actually happened: a Deps missing one
// repository constructed cleanly and panicked later, on a nil pointer,
// several layers away from the line that forgot something.
func TestTheServiceRefusesAnIncompleteDeps(t *testing.T) {
	full := func() app.Deps {
		return app.Deps{
			Rooms:      stubRooms{},
			Memories:   stubMemories{},
			Artifacts:  stubArtifacts{},
			Items:      stubItems{},
			Sources:    stubSources{},
			Provenance: stubProvenance{},
			Relations:  stubRelations{},
			Sessions:   stubSessions{},
			Clock:      stubClock{},
		}
	}

	if _, err := app.NewService(full()); err != nil {
		t.Fatalf("a complete Deps was refused: %v", err)
	}

	// One at a time, so the message names the one that is missing rather
	// than the first of many.
	blanks := map[string]func(*app.Deps){
		"Rooms":      func(d *app.Deps) { d.Rooms = nil },
		"Memories":   func(d *app.Deps) { d.Memories = nil },
		"Artifacts":  func(d *app.Deps) { d.Artifacts = nil },
		"Items":      func(d *app.Deps) { d.Items = nil },
		"Sources":    func(d *app.Deps) { d.Sources = nil },
		"Provenance": func(d *app.Deps) { d.Provenance = nil },
		"Relations":  func(d *app.Deps) { d.Relations = nil },
		"Sessions":   func(d *app.Deps) { d.Sessions = nil },
		"Clock":      func(d *app.Deps) { d.Clock = nil },
	}
	for name, blank := range blanks {
		d := full()
		blank(&d)
		svc, err := app.NewService(d)
		if err == nil {
			t.Errorf("a Deps with no %s was accepted", name)
			continue
		}
		if svc != nil {
			t.Errorf("a refused construction returned a service for %s", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal for %s does not name it: %v", name, err)
		}
	}
}

func TestATypedNilIsRefusedLikeAMissingOne(t *testing.T) {
	// An interface holding a nil pointer is not a nil interface. A caller
	// that wired one has the same bug as one that wired nothing, and used
	// to get the same panic later.
	var typedNil *ports.RoomRepo
	_ = typedNil

	d := app.Deps{
		Rooms:      (ports.RoomRepo)(nil),
		Memories:   stubMemories{},
		Artifacts:  stubArtifacts{},
		Items:      stubItems{},
		Sources:    stubSources{},
		Provenance: stubProvenance{},
		Relations:  stubRelations{},
		Sessions:   stubSessions{},
		Clock:      stubClock{},
	}
	if _, err := app.NewService(d); err == nil {
		t.Fatal("a nil Rooms was accepted")
	}
}

func TestAMissingLoggerIsNotAMissingDependency(t *testing.T) {
	// A missing repository makes an operation impossible; a missing logger
	// makes it quiet. Refusing over the second would be treating a
	// preference as a dependency.
	svc, err := app.NewService(app.Deps{
		Rooms:      stubRooms{},
		Memories:   stubMemories{},
		Artifacts:  stubArtifacts{},
		Items:      stubItems{},
		Sources:    stubSources{},
		Provenance: stubProvenance{},
		Relations:  stubRelations{},
		Sessions:   stubSessions{},
		Clock:      stubClock{},
		Logger:     nil,
	})
	if err != nil {
		t.Fatalf("a Deps with no logger was refused: %v", err)
	}
	if svc == nil {
		t.Fatal("no service was returned")
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	A CALL WITH NO WORKSPACE IS REFUSED BEFORE IT REACHES ANYTHING
//
// ══════════════════════════════════════════════════════════════════════
//
// Every capability is built here over a NIL application service, and a
// bare context carrying no workspace. If any of them reached the service,
// the test would panic rather than fail: the refusal has to come first,
// from workspaceOf, before a single field of the request is used.
//
// That ordering is the whole guarantee. A capability that validated its
// arguments first and checked the workspace afterwards would already have
// decided what to do with somebody's data before asking whose it was.
func TestEveryCapabilityRefusesACallWithNoWorkspace(t *testing.T) {
	bare := context.Background()

	for _, tool := range tools.New(nil) {
		def := tool.Definition()

		// Arguments that would be perfectly valid, so nothing else can be
		// what refuses the call.
		args := map[string]any{}
		for name, prop := range def.Schema.Properties {
			switch prop.Type {
			case chatdomain.TypeString:
				args[name] = uuid.New().String()
			case chatdomain.TypeBoolean:
				args[name] = false
			case chatdomain.TypeInteger, chatdomain.TypeNumber:
				args[name] = 1
			}
		}

		out, err := tool.Execute(bare, args)
		if err == nil {
			t.Errorf("%q ran without a workspace and returned %v", def.Name, out)
			continue
		}
		var failure *chatdomain.ToolFailure
		if !errors.As(err, &failure) {
			t.Errorf("%q refused with %T, want a tool failure: %v", def.Name, err, err)
			continue
		}
		if !strings.Contains(failure.Message, "workspace") {
			t.Errorf("%q refused without saying why: %v", def.Name, failure.Message)
		}
	}
}
