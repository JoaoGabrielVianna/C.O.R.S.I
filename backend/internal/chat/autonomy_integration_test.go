//go:build integration

// Autonomous investigation, and the line it must not cross.
//
// The policy now tells the model to take a lookup rather than offer one.
// That is a statement about READS. The registry contains a capability that
// writes, so "act without asking" has an edge, and these tests are about
// where it is: what the provider is actually told about each capability,
// and what a scoped turn is allowed to reach.
package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── a capability that changes something ─────────────────────────────── */

// mutateTool is the smallest possible write. It exists because the boundary
// between "read freely" and "never without being asked" cannot be tested
// against a catalogue that only reads — and because pointing this suite at
// a real write tool would make the Agents module name another module.
type mutateTool struct{}

const mutateToolName = "system.mutate"

func (mutateTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        mutateToolName,
		Title:       "Mutate",
		Description: "Changes a thing.",
		Effect:      domain.EffectWrite,
		Internal:    true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"value": {Type: domain.TypeString, MaxLength: 100},
			},
		},
	}
}

func (mutateTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	return domain.ToolOutput{"changed": true}, nil
}

// declaredDescriptionOf reads back what the provider was told one tool does.
func declaredDescriptionOf(req ports.CompletionRequest, name string) (string, bool) {
	for _, d := range req.Tools {
		if d.Name.String() == name {
			return d.DeclaredDescription(), true
		}
	}
	return "", false
}

/* ── H · a write declares itself, a read does not change ─────────────── */

func TestAWriteCapabilityTellsTheModelItChangesThings(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(answer("ok", 40, 10))
	e.send(e.wsA, s.conversationID, "olá")

	req := e.lastRequest()

	write, ok := declaredDescriptionOf(req, mutateToolName)
	if !ok {
		t.Fatalf("the write capability was never declared: %+v", req.Tools)
	}
	if !strings.Contains(write, "CHANGES data") {
		t.Fatalf("the model was not told this capability changes things: %q", write)
	}
	if !strings.Contains(write, "unless the user asked") {
		t.Fatalf("the model was not told who authorises the change: %q", write)
	}

	// And the read is exactly what its author wrote. Every capability that
	// existed before this change goes on the wire unchanged.
	read, ok := declaredDescriptionOf(req, echoTool)
	if !ok {
		t.Fatal("the read capability was never declared")
	}
	if strings.Contains(read, "CHANGES data") {
		t.Fatalf("a read capability was labelled as a write: %q", read)
	}
}

// The policy's own carve-out, on the wire, in the same turn. Together with
// the declaration above it is what makes the rule applicable: the model is
// told the exception exists AND which capabilities it covers.
func TestTheTurnCarriesBothHalvesOfTheWriteBoundary(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(answer("ok", 40, 10))
	e.send(e.wsA, s.conversationID, "olá")

	req := e.lastRequest()
	policy := ""
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "capabilities that look things up") {
			policy = m.Content
		}
	}
	if policy == "" {
		t.Fatal("the grounding policy did not reach a turn that has capabilities")
	}
	if !strings.Contains(policy, "CHANGES something is the exception") {
		t.Fatalf("the policy carries no exception for writes:\n%s", policy)
	}
	if !strings.Contains(policy, "do it and then answer") {
		t.Fatalf("the policy no longer tells the model to take the lookup:\n%s", policy)
	}
}

/* ── E and F · `@` is still the user's authority over scope ──────────── */

// Scoping a turn to a read must leave the write unreachable — not merely
// undeclared, but unable to run. Autonomy applies to what is in scope, and
// `@` is what sets the scope.
func TestASelectionKeepsAWriteCapabilityOutOfTheTurnEntirely(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	// The user attaches the read only, then the model asks for the write.
	e.scriptNext(
		askTool("call_w", mutateToolName, `{"value":"x"}`),
		answer("não pude", 40, 10),
	)
	rec := e.sendWith(e.wsA, s.conversationID, "faça algo",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	// Declared: only the selection.
	if names := toolNamesOf(e.nthRequest(1)); len(names) != 1 || names[0] != echoTool {
		t.Fatalf("declared %v, want only the selected read", names)
	}
	// Executed: nothing. The per-call gate is the same set.
	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want the refused one", calls)
	}
	if calls[0].Status == "ok" || calls[0].ErrorCode != string(domain.ToolErrNotAuthorized) {
		t.Fatalf("a write outside the selection was %s/%s, want an authorization refusal",
			calls[0].Status, calls[0].ErrorCode)
	}
}

/* ── C and G · nothing to investigate with ───────────────────────────── */

// An agent with no capabilities is told nothing about investigating. The
// policy is an instruction to do something it cannot do, and charging for
// it every turn would be paying for a sentence that can only mislead.
func TestAnAgentWithNoCapabilitiesIsNotToldToInvestigate(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"system_prompt": "seja objetivo"})

	e.scriptNext(answer("ok", 40, 10))
	e.send(e.wsA, s.conversationID, "olá")

	req := e.lastRequest()
	if len(req.Tools) != 0 {
		t.Fatalf("an agent with no grants declared %+v", req.Tools)
	}
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "do it and then answer") {
			t.Fatalf("a turn with no capabilities was told to investigate: %q", m.Content)
		}
	}
	// And the prompt is what it always was.
	if len(req.Messages) != 2 || req.Messages[0].Content != "seja objetivo" {
		t.Fatalf("messages = %+v", req.Messages)
	}
}

/* ── the report prices what was sent ─────────────────────────────────── */

// The write notice is real characters on the wire, billed as input on every
// call of the turn. A report that omitted it would understate exactly the
// turns that carry a capability able to change something.
func TestTheReportCountsTheWriteNotice(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(answer("ok", 40, 10))
	e.send(e.wsA, s.conversationID, "olá")

	turns := e.assistantTurns(e.wsA, s.conversationID)
	block := turns[len(turns)-1].ContextReport.block(t, "tools")

	def := mutateTool{}.Definition()
	if block.Characters <= len(def.Description) {
		t.Fatalf("tools block is %d characters; the declaration that was sent is longer",
			block.Characters)
	}
	if app.EstimateTokens(block.Characters) != block.EstimatedTokens {
		t.Fatal("the tools block's token estimate disagrees with its own character count")
	}
}
