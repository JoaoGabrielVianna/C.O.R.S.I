package tools

import (
	"context"

	"github.com/corsi/backend/internal/chat/domain"
)

// system.echo — the foundation's proof tool.
//
// ── What it is for ─────────────────────────────────────────────────────
// Proving the mechanism, end to end and in production code: registry →
// authorization → schema validation → declaration to the model → parsing a
// streamed tool call → execution → result back to the model → continuation
// → persistence → accounting. Every link in that chain is real. Only the
// capability at the end is trivial, and it is trivial on purpose.
//
// ── Why echo and not "current time" or "calculate" ─────────────────────
// Because a proof instrument must not be able to fail for a reason of its
// own. It has no network, no clock, no randomness and no side effect, so
// two identical calls produce two identical results, forever. `current_time`
// would make every fixture depend on when the test ran; `calculate` would
// need a parser, and a parser is a second thing that can be wrong. When a
// test using this tool fails, the failure is in the machinery — which is
// the only reason the tool exists.
//
// ── Why it is marked internal ──────────────────────────────────────────
// It is not a product capability and must never be described as one. The
// interface shows it, labelled, rather than hiding it: a capability the
// user cannot see is a capability they cannot revoke, and the honest way to
// say "this exists to test the system" is to say it on the row.
type echoTool struct{}

// maxEchoText bounds the input well below the whole-payload ceiling. The
// tool returns what it was given, so its input bound is also its output
// bound — and both are prompt tokens on the next call of the same turn.
const maxEchoText = 2000

func (echoTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:   "system.echo",
		Title:  "Echo",
		Effect: domain.EffectRead,
		// Written for the model, not for the user. It has to say plainly that
		// the tool is useless for answering questions, or a model given it
		// alongside real tools will reach for it when it has nothing better.
		Description: "Returns the text it was given, unchanged. A diagnostic " +
			"tool for verifying that tool calling works end to end. It has no " +
			"access to any data and cannot answer questions.",
		Internal: true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"text": {
					Type:        domain.TypeString,
					Description: "The text to return unchanged.",
					MaxLength:   maxEchoText,
				},
			},
			Required: []string{"text"},
		},
	}
}

// Execute returns its input. It takes ctx and ignores it, which is correct
// exactly because there is nothing to cancel: the deadline and the
// cancellation are enforced by the caller around every executor (see
// app/toolexec.go), so a tool that finishes instantly needs no ceremony to
// be cancellable in the way that matters.
func (echoTool) Execute(_ context.Context, args map[string]any) (domain.ToolOutput, error) {
	// The cast is safe because the schema was validated before the call: a
	// declared string property either decoded into a string or the call never
	// reached here. The comma-ok form is kept anyway, because "safe because
	// somewhere else checked" is exactly the assumption that stops being true.
	text, ok := args["text"].(string)
	if !ok {
		return nil, domain.ToolError(domain.ToolErrExecutionFailed,
			"text was not a string after validation")
	}
	return domain.ToolOutput{"text": text}, nil
}
