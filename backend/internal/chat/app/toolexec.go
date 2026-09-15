package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Tool execution: the four gates a call passes before anything runs.
//
//	resolve   → is this a tool at all?          tool_not_found
//	authorize → may THIS agent use it?          tool_not_authorized
//	validate  → do the arguments fit?           tool_invalid_arguments
//	execute   → bounded, cancellable            tool_execution_failed / tool_timeout
//
// The order is not decorative. Authorization is checked before the
// arguments are even parsed, so an unauthorized call never reaches a
// validator that might be persuaded to do work; and resolution comes first
// because "may I use X" is not answerable when X is not a thing.
//
// ── The result of a failed call is still a result ──────────────────────
// Every one of these outcomes goes back to the model as the content of a
// `tool` message rather than ending the turn. That is what lets an agent
// say "I tried to look that up and the arguments were wrong" instead of the
// conversation dying with a 500. The one exception is the round limit,
// which is not a tool error at all — see send.go.

// toolExecutionTimeout bounds one executor.
//
// Ten seconds. It is not a network budget — nothing in this batch touches
// the network — it is the ceiling that must exist before one does: a turn
// holds an open SSE connection and the user's attention while a tool runs,
// and the first integration-backed tool will inherit whatever number is
// here. Ten seconds is long enough for an HTTP round trip to a slow API and
// short enough that a hung executor is a delay rather than an outage.
//
// It is derived from the request context, so it is a ceiling and never a
// floor: pressing stop cancels a running tool immediately.
const toolExecutionTimeout = 10 * time.Second

// toolOutcome is one call, executed or refused, in the form the loop needs:
// something to send back to the model, and something to write down.
type toolOutcome struct {
	Call domain.ToolCall
	// Content is what the model receives as the `tool` message body. Always
	// JSON, success or failure, so the model reads one shape.
	Content string
	Status  domain.ToolCallStatus
	Failure *domain.ToolFailure
	// Result is the serialised success payload, or nil on failure. Kept
	// apart from Content because the audit trail records what the tool
	// returned, not what we phrased for the model.
	Result *string
	// EffectRef is the entity this call touched, as the CAPABILITY reported
	// it — never as this file inferred it. Nil whenever the capability does
	// not implement ports.EffectReporter, whenever it declines to name one,
	// and whenever what it named does not satisfy the contract.
	//
	// It survives redaction on purpose: it is the one piece of a
	// Confidential call that may outlive the payload, because it is a type
	// and a UUID and cannot carry content.
	EffectRef  *domain.EffectRef
	DurationMS int
}

// executeToolCall runs one call through the four gates.
//
// It never returns an error. Every failure is an outcome, because a failed
// tool call is a fact about the conversation rather than a fault of the
// request — and a turn that dies because the model guessed a bad argument
// is a turn the user paid for and did not get.
func (s *Service) executeToolCall(
	ctx context.Context,
	authorized []domain.ToolDefinition,
	call domain.ToolCall,
) toolOutcome {
	started := time.Now()
	outcome := func(status domain.ToolCallStatus, code domain.ToolErrorCode, msg string) toolOutcome {
		f := domain.ToolError(code, msg)
		return toolOutcome{
			Call:       call,
			Content:    encodeToolFailure(f),
			Status:     status,
			Failure:    f,
			DurationMS: int(time.Since(started).Milliseconds()),
		}
	}
	// ── Refused before running, versus ran and failed ───────────────
	//
	// Gates 1 to 3 turn a call away before it does anything: the tool does
	// not exist, this agent may not use it, the arguments do not fit. Gate
	// 4 is the tool doing its job and failing at it.
	//
	// The two used to share `error`, which made "was this attempted?"
	// unanswerable — and that is the exact question a write receipt is. A
	// refusal is NOT_EXECUTED, and a person reading a receipt can tell the
	// difference between an agent that was stopped and one that broke.
	//
	// What the MODEL is told is unchanged: both still come back as the same
	// shape of tool failure, so no prompt behaviour moves with this.
	refuse := func(code domain.ToolErrorCode, msg string) toolOutcome {
		return outcome(domain.ToolCallNotExecuted, code, msg)
	}
	fail := func(code domain.ToolErrorCode, msg string) toolOutcome {
		return outcome(domain.ToolCallError, code, msg)
	}

	// ── 1. resolve ──────────────────────────────────────────────────
	tool, registered := s.tools.Lookup(call.Name)
	if !registered {
		return refuse(domain.ToolErrNotFound,
			"there is no tool named "+call.Name.String())
	}

	// ── 2. authorize ────────────────────────────────────────────────
	//
	// Against what the turn read from the store, not against the registry.
	// Being in the registry is availability; being in this slice is
	// permission, and conflating the two is the single mistake this whole
	// design exists to make impossible.
	def, allowed := isAuthorized(authorized, call.Name)
	if !allowed {
		// Told apart from "not found" on purpose, and told to the model as
		// such: a model that hears "no such tool" will try spelling
		// variations of the name, while one that hears "not authorized"
		// explains the situation to the user.
		return refuse(domain.ToolErrNotAuthorized,
			"this agent is not authorized to use "+call.Name.String())
	}

	// ── 3. validate ─────────────────────────────────────────────────
	args, err := def.Schema.ValidateArguments(call.Arguments)
	if err != nil {
		var f *domain.ToolFailure
		if errors.As(err, &f) {
			return refuse(f.Code, f.Message)
		}
		return refuse(domain.ToolErrInvalidArguments, err.Error())
	}

	// ── 4. execute ──────────────────────────────────────────────────
	//
	// The deadline hangs off the request context rather than replacing it,
	// so both bounds apply: the tool stops at its own ceiling, and it stops
	// immediately if the user hangs up.
	execCtx, cancel := context.WithTimeout(ctx, toolExecutionTimeout)
	defer cancel()

	out, err := tool.Execute(execCtx, args)
	duration := int(time.Since(started).Milliseconds())
	if err != nil {
		// A deadline is reported as a timeout even when the executor wrapped
		// it in something else, because what the model and the audit trail
		// need to know is that it ran out of time, not how it phrased that.
		code := domain.ToolErrExecutionFailed
		message := err.Error()
		if execCtx.Err() != nil {
			code = domain.ToolErrTimeout
			message = "the tool did not finish within " + toolExecutionTimeout.String()
		} else {
			var f *domain.ToolFailure
			if errors.As(err, &f) {
				code, message = f.Code, f.Message
			}
		}
		o := fail(code, message)
		o.DurationMS = duration
		return o
	}

	encoded, err := encodeToolOutput(out)
	if err != nil {
		o := fail(domain.ToolErrExecutionFailed, err.Error())
		o.DurationMS = duration
		return o
	}

	result := encoded
	return toolOutcome{
		Call:    call,
		Content: encoded,
		Status:  domain.ToolCallOK,
		Result:  &result,
		// Asked of the tool, with the output it just produced, before
		// anything is redacted — and only on success, because a call that
		// failed touched nothing to identify.
		EffectRef:  s.effectRefOf(tool, call.Name, out),
		DurationMS: duration,
	}
}

// effectRefOf asks a capability which entity it just touched.
//
// ── Why this is a type assertion and not a field ───────────────────────
// Because reporting an effect is opt-in. Most capabilities touch nothing
// nameable — a listing, a read, a link between two things that already
// exist — and the 47 that do not implement the interface answer nothing at
// all, which is the correct answer and not a gap.
//
// ── Why the result is validated here ───────────────────────────────────
// A capability under pressure to be helpful could return a title where an
// id belongs. domain.EffectRef refuses that structurally, and this is where
// the refusal takes effect: what fails the contract is DROPPED and logged,
// never trimmed, corrected or passed along. A wrong identifier is worse
// than none, because a resume would follow it confidently.
func (s *Service) effectRefOf(tool ports.Tool, name domain.ToolName, out domain.ToolOutput) *domain.EffectRef {
	reporter, ok := tool.(ports.EffectReporter)
	if !ok {
		return nil
	}
	ref, reported := reporter.EffectRefOf(out)
	if !reported {
		return nil
	}
	if !ref.Valid() {
		// Loud, because this is a capability breaking its own contract and
		// the symptom downstream would be a silently unsafe resume.
		s.log.Warn("capability reported a malformed effect ref; dropping it",
			"tool", name.String(), "type", ref.Type)
		return nil
	}
	return &ref
}

// encodeToolOutput serialises a success, refusing one that is too large.
//
// The ceiling is enforced here rather than left to each executor because
// every byte of it becomes a prompt token on the next provider call of the
// same turn. A tool that wants to return more than the ceiling is a tool
// that should paginate, and telling it so through the normal failure path
// lets the model ask again with a narrower request.
func encodeToolOutput(out domain.ToolOutput) (string, error) {
	if out == nil {
		out = domain.ToolOutput{}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", domain.ToolError(domain.ToolErrExecutionFailed,
			"the tool returned something that could not be encoded")
	}
	if len(raw) > domain.MaxToolResultBytes {
		return "", domain.ToolError(domain.ToolErrExecutionFailed,
			"the tool returned more data than one turn may carry; ask for less")
	}
	return string(raw), nil
}

// encodeToolFailure is what the model reads when a call did not work.
//
// One shape for every failure, carrying the code and the sentence. The code
// is there so a model can distinguish "fix your arguments" from "you are
// not allowed", and the sentence is there because that is what it will
// paraphrase to the user.
func encodeToolFailure(f *domain.ToolFailure) string {
	raw, err := json.Marshal(map[string]any{
		"error":   string(f.Code),
		"message": f.Message,
	})
	if err != nil {
		// Two constant strings cannot fail to marshal. If they somehow did,
		// the model still has to receive valid JSON.
		return `{"error":"tool_execution_failed","message":"the failure could not be encoded"}`
	}
	return string(raw)
}
