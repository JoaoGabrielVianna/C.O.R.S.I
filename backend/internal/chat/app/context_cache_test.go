package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

// Where the cache breakpoint goes, and what it is not allowed to change.
//
// ── The claim under test ───────────────────────────────────────────────
// Turning prompt caching on changes ONE thing about a turn: one message
// carries a marker asking the provider to cache the prefix ending at it.
// It does not change which messages are sent, their order, their roles,
// their text, the tool declaration, or a single number in the report.
//
// That is the whole basis for calling this sprint lossless, so it is
// asserted structurally here and byte for byte in the adapter's own
// cache_control_test.go.

func cacheTestAgent(prompt string) *domain.Agent {
	return &domain.Agent{
		ID:           uuid.New(),
		SystemPrompt: prompt,
		Model:        "claude-opus-4-7",
		MaxTokens:    4096,
		HistoryLimit: 40,
	}
}

func cacheTestTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{{
		Name:        "finance.summary.get",
		Description: "Reads a period summary.",
		Effect:      domain.EffectRead,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"period": {Type: domain.TypeString, Description: "the period"},
			},
			Required: []string{"period"},
		},
	}}
}

func cacheTestInput(promptCache bool) ContextInput {
	current := &domain.Message{ID: uuid.New(), Role: domain.RoleUser, Content: "quanto gastei?"}
	return ContextInput{
		Agent:    cacheTestAgent("Você é o Ledger."),
		Tools:    cacheTestTools(),
		Memories: []domain.Memory{{Content: "prefere respostas curtas"}},
		History: []domain.Message{
			{ID: uuid.New(), Role: domain.RoleUser, Content: "oi"},
			{ID: uuid.New(), Role: domain.RoleAssistant, Content: "olá"},
			*current,
		},
		Current:     current,
		PromptCache: promptCache,
	}
}

// breakpoints returns the index of every marked message.
func breakpoints(built BuiltContext) []int {
	var out []int
	for i, m := range built.Messages {
		if m.CacheBreakpoint {
			out = append(out, i)
		}
	}
	return out
}

// TestCacheBreakpointLandsOnTheLastInstruction pins the position.
//
// The instructions are the grounding policy and the agent's own prompt, and
// they are the only two things in a request that are the same bytes on every
// turn of an agent's life. Everything after them — subjects, reference
// state, evidence, history — is rewritten or appended per turn, so a marker
// below any of them would be an entry written once and never read.
func TestCacheBreakpointLandsOnTheLastInstruction(t *testing.T) {
	built := BuildContext(cacheTestInput(true))

	marks := breakpoints(built)
	if len(marks) != 1 {
		t.Fatalf("%d breakpoints, want exactly one: %v", len(marks), marks)
	}

	block, ok := built.Report.Block(domain.BlockInstructions)
	if !ok {
		t.Fatalf("no instructions block")
	}
	// The instructions are appended first and consecutively, so the last one
	// is at Items-1. A marker anywhere else means the assembler's ordering
	// changed underneath this rule.
	if want := block.Items - 1; marks[0] != want {
		t.Fatalf("breakpoint at message %d, want %d (the last of %d instruction messages)",
			marks[0], want, block.Items)
	}
	if built.Messages[marks[0]].Role != "system" {
		t.Fatalf("the breakpoint landed on a %q message", built.Messages[marks[0]].Role)
	}
	// It must be the AGENT'S prompt, not the grounding policy that precedes
	// it: marking the policy would leave the prompt outside the prefix.
	if built.Messages[marks[0]].Content != "Você é o Ledger." {
		t.Fatalf("the breakpoint is not on the agent's own instructions: %q",
			built.Messages[marks[0]].Content)
	}
	if built.Report.CacheBreakpointAfter != domain.BlockInstructions {
		t.Fatalf("the report says the prefix ends after %q",
			built.Report.CacheBreakpointAfter)
	}
}

// TestPromptCacheChangesNothingButTheMarker is the equivalence proof at the
// builder level.
//
// Same input twice, caching off and on. Every message, every role, every
// character, the tool list and the entire report must be identical; the only
// permitted difference is the boolean.
func TestPromptCacheChangesNothingButTheMarker(t *testing.T) {
	off := BuildContext(cacheTestInput(false))
	on := BuildContext(cacheTestInput(true))

	if len(off.Messages) != len(on.Messages) {
		t.Fatalf("message count changed: %d off, %d on", len(off.Messages), len(on.Messages))
	}
	for i := range off.Messages {
		a, b := off.Messages[i], on.Messages[i]
		if a.Role != b.Role {
			t.Fatalf("message %d role changed: %q → %q", i, a.Role, b.Role)
		}
		if a.Content != b.Content {
			t.Fatalf("message %d content changed:\n off: %q\n  on: %q", i, a.Content, b.Content)
		}
		if a.ToolCallID != b.ToolCallID || len(a.ToolCalls) != len(b.ToolCalls) {
			t.Fatalf("message %d tool scaffolding changed", i)
		}
	}
	if len(off.Tools) != len(on.Tools) {
		t.Fatalf("the tool declaration changed: %d → %d", len(off.Tools), len(on.Tools))
	}

	// The accounting must not move either. A marker costs no characters and
	// must not appear as one, or the Inspector would report a turn as having
	// grown when nothing was added to it.
	if off.Report.TotalCharacters != on.Report.TotalCharacters {
		t.Fatalf("total characters changed: %d → %d",
			off.Report.TotalCharacters, on.Report.TotalCharacters)
	}
	if off.Report.TotalEstimatedTokens != on.Report.TotalEstimatedTokens {
		t.Fatalf("estimated tokens changed: %d → %d",
			off.Report.TotalEstimatedTokens, on.Report.TotalEstimatedTokens)
	}
	if len(off.Report.Blocks) != len(on.Report.Blocks) {
		t.Fatalf("block count changed")
	}
	for i := range off.Report.Blocks {
		// ContextBlock holds a slice, so the countable parts are compared
		// field by field and the exclusions are compared alongside them.
		a, b := off.Report.Blocks[i], on.Report.Blocks[i]
		if a.Kind != b.Kind || a.Items != b.Items || a.Characters != b.Characters ||
			a.EstimatedTokens != b.EstimatedTokens {
			t.Fatalf("block %d changed: %+v → %+v", i, a, b)
		}
		if len(a.Exclusions) != len(b.Exclusions) {
			t.Fatalf("block %d exclusions changed: %v → %v", i, a.Exclusions, b.Exclusions)
		}
		for j := range a.Exclusions {
			if a.Exclusions[j] != b.Exclusions[j] {
				t.Fatalf("block %d exclusion %d changed: %+v → %+v",
					i, j, a.Exclusions[j], b.Exclusions[j])
			}
		}
	}
}

// TestPromptCacheOffMarksNothing is the kill switch, asserted rather than
// assumed. Off must produce the context this builder produced before
// caching existed.
func TestPromptCacheOffMarksNothing(t *testing.T) {
	built := BuildContext(cacheTestInput(false))
	if marks := breakpoints(built); len(marks) != 0 {
		t.Fatalf("caching is off and %d messages carry a breakpoint: %v", len(marks), marks)
	}
	if built.Report.CacheBreakpointAfter != "" {
		t.Fatalf("the report claims a breakpoint after %q with caching off",
			built.Report.CacheBreakpointAfter)
	}
}

// TestCacheBreakpointWithoutASystemPrompt covers the agent that never wrote
// one but does hold capabilities.
//
// The grounding policy is still emitted, so there IS a stable instruction to
// mark — and marking it is what puts the tool catalogue, which is the
// expensive part, inside the cached prefix.
func TestCacheBreakpointWithoutASystemPrompt(t *testing.T) {
	in := cacheTestInput(true)
	in.Agent = cacheTestAgent("")
	built := BuildContext(in)

	marks := breakpoints(built)
	if len(marks) != 1 {
		t.Fatalf("%d breakpoints on an agent with tools and no prompt, want 1", len(marks))
	}
	if marks[0] != 0 {
		t.Fatalf("the breakpoint is at %d, want the grounding policy at 0", marks[0])
	}
}

// TestNoInstructionsMeansNoBreakpoint is the honest end of the rule: an
// agent with no capabilities and no prompt has nothing stable to cache, so
// nothing is marked. Marking the first history message instead would place a
// breakpoint on the most volatile block in the request.
func TestNoInstructionsMeansNoBreakpoint(t *testing.T) {
	in := cacheTestInput(true)
	in.Agent = cacheTestAgent("")
	in.Tools = nil

	built := BuildContext(in)
	if marks := breakpoints(built); len(marks) != 0 {
		t.Fatalf("%d breakpoints with no instructions at all: %v", len(marks), marks)
	}
	if built.Report.CacheBreakpointAfter != "" {
		t.Fatalf("the report claims a breakpoint with nothing to mark")
	}
}

// TestBreakpointNeverLandsOnAVolatileBlock is the rule stated as a property
// rather than as a position.
//
// Reference state is re-read every turn by design and tool evidence is
// recomputed from the audit trail every turn. A breakpoint at or below
// either would produce an entry that is written on every turn and read on
// none — the exact failure mode that turns caching from a saving into a 25%
// surcharge, since a cache write costs more than an ordinary input token.
func TestBreakpointNeverLandsOnAVolatileBlock(t *testing.T) {
	in := cacheTestInput(true)
	in.ContextReferences = []domain.ContextReference{{
		Type: "job_radar.opportunity", ID: uuid.NewString(), Label: "Vaga X",
	}}
	in.HydratedReferences = []HydratedReference{{
		Reference: in.ContextReferences[0],
		Status:    domain.HydrationCurrent,
	}}
	in.Evidence = EvidenceSelection{Items: []EvidenceItem{{
		Tool: "github.repository.list", Arguments: "{}", Result: `{"repos":[]}`,
	}}}

	built := BuildContext(in)
	marks := breakpoints(built)
	if len(marks) != 1 {
		t.Fatalf("%d breakpoints, want 1", len(marks))
	}

	// Everything volatile must sit strictly AFTER the marker.
	instructions, _ := built.Report.Block(domain.BlockInstructions)
	if marks[0] >= instructions.Items {
		t.Fatalf("the breakpoint at %d is past the %d instruction messages, so it is "+
			"inside a block that is rewritten every turn", marks[0], instructions.Items)
	}
	for _, kind := range []domain.BlockKind{
		domain.BlockContextReferences, domain.BlockReferenceState, domain.BlockToolEvidence,
	} {
		if _, present := built.Report.Block(kind); !present {
			t.Fatalf("%s was expected in this fixture and is missing; the test is not "+
				"exercising what it claims to", kind)
		}
	}
}

/* ── the unbounded history block, as a measurement case ──────────────── */

// syntheticPaste builds a message of a given size out of deterministic,
// meaningless text.
//
// It stands in for a real one. The conversation that exposed this behaviour
// began with a 78.105-character paste of the operator's own financial task
// description, and that content is private and is not going into a test
// fixture. Its SIZE is the only property that matters here, so the size is
// what the fixture reproduces.
func syntheticPaste(characters int) string {
	// Counted in RUNES, not bytes. The unit carries accents, so a byte-length
	// loop would build a shorter string than it thinks and the rune slice
	// below would run off the end — which is also the reason the whole
	// builder counts characters with utf8.RuneCountInString rather than len.
	const unit = "linha de conteúdo colado sem valor semântico para este teste. "
	unitRunes := utf8.RuneCountInString(unit)

	var b strings.Builder
	for n := 0; n < characters; n += unitRunes {
		b.WriteString(unit)
	}
	return string([]rune(b.String())[:characters])
}

// TestHistoryHasNoCharacterCeiling is a REGRESSION FIXTURE, not a wish.
//
// ── What it asserts, and why that is the honest thing to assert ────────
// It asserts the runtime as it actually behaves: a single pasted message is
// replayed in full, on every turn, for as long as it stays inside the
// message-count window. There is no character ceiling on history.
//
// The documentation claimed for three months that there was one and that
// it "closed the unpredictable cost hole". It did not, and the
// documentation has been corrected rather than the behaviour,
// because cutting history is the one economy on the table that reduces what
// the model knows — and the caching sprint deliberately bought its saving
// without touching a word of the context.
//
// So this test exists to MEASURE the exposure and to fail loudly the day
// somebody adds truncation without a design. If a ceiling is ever
// introduced, this test should fail, and the person who broke it should
// replace it with one that asserts the ceiling AND its reporting — a cut
// that does not appear in the ContextReport as an exclusion is the silent
// truncation this module refuses everywhere else.
func TestHistoryHasNoCharacterCeiling(t *testing.T) {
	// The size of the real paste that exposed this.
	const pasteChars = 78105
	paste := syntheticPaste(pasteChars)

	pasted := &domain.Message{ID: uuid.New(), Role: domain.RoleUser, Content: paste}
	follow := &domain.Message{ID: uuid.New(), Role: domain.RoleUser, Content: "e agora?"}

	in := cacheTestInput(false)
	in.History = []domain.Message{
		*pasted,
		{ID: uuid.New(), Role: domain.RoleAssistant, Content: "ok"},
		*follow,
	}
	in.Current = follow

	built := BuildContext(in)

	history, ok := built.Report.Block(domain.BlockHistory)
	if !ok {
		t.Fatalf("no history block")
	}
	// Every character of the paste is still there, on a turn several
	// messages later. This is the behaviour, stated.
	if history.Characters < pasteChars {
		t.Fatalf("history carried %d characters, fewer than the %d-character paste "+
			"alone. A ceiling appears to have been introduced.\n\n"+
			"If that was deliberate: this fixture must be replaced by one that "+
			"asserts the ceiling AND that the cut is reported as a ContextReport "+
			"exclusion. A truncation nobody can see is the failure mode every "+
			"other block in this builder is arranged to prevent.",
			history.Characters, pasteChars)
	}
	// And nothing was reported as excluded, because nothing was.
	for _, ex := range history.Exclusions {
		if ex.Reason == domain.ReasonBudget || ex.Reason == domain.ReasonTruncated {
			t.Fatalf("history reported a %q exclusion; a size limit exists that this "+
				"fixture does not know about", ex.Reason)
		}
	}

	// The cost, stated in the units the Inspector uses. This is the number
	// the correction in context-engine.md cites, and it is recomputed here
	// so the document cannot drift away from the runtime again.
	est := EstimatorFor(in.Agent.Model)
	t.Logf("a %d-character paste contributes ~%d estimated tokens to EVERY turn it "+
		"stays in the window (history_limit=%d messages, no size limit)",
		pasteChars, est.Tokens(pasteChars), in.Agent.HistoryLimit)
}

// TestOnlyHistoryLacksASizeBudget states the asymmetry as a property, so the
// correction to the documentation has something enforcing it.
//
// Memory, Sources and Evidence each refuse to grow past a character budget.
// History does not. That is the whole finding, and it is one line of test.
func TestOnlyHistoryLacksASizeBudget(t *testing.T) {
	budgets := map[string]int{
		"memory":   MemoryBudgetChars,
		"sources":  SourcesBudgetChars,
		"evidence": EvidenceBudgetChars,
	}
	for name, budget := range budgets {
		if budget <= 0 {
			t.Errorf("%s reports no character budget; the asymmetry this test "+
				"describes has changed", name)
		}
	}
	// There is deliberately no HistoryBudgetChars to assert against. If one
	// is ever added, this test should be rewritten rather than deleted: the
	// question "what bounds history?" must keep having an answer somewhere a
	// reader can find.
	t.Logf("memory=%d sources=%d evidence=%d chars; history is bounded by MESSAGE "+
		"COUNT only (agent.history_limit, 1..200, default %d)",
		MemoryBudgetChars, SourcesBudgetChars, EvidenceBudgetChars, domain.DefaultHistoryLimit)
}
