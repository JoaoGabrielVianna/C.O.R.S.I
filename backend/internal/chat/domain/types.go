// Package domain holds entities, value objects, and domain errors for the
// chat bounded context. It depends only on the stdlib and uuid; nothing here
// imports HTTP, SQL, or any platform/adapter package.
//
// This context owns three things: the credentials for an OpenAI-compatible
// LLM endpoint (LiteLLM in practice), a registry of configured agents, and
// the conversation log. It knows nothing about any other bounded context.
package domain

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

func (r Role) Valid() bool {
	return r == RoleUser || r == RoleAssistant
}

// FinishReason records why a streamed completion stopped. `stop` and
// `length` come from the provider; the rest are ours, for a stream that
// died before the provider said anything conclusive.
type FinishReason string

const (
	FinishStop   FinishReason = "stop"
	FinishLength FinishReason = "length"
	// FinishAborted is SOMEBODY stopping the turn: the user pressed stop,
	// the tab closed, the connection went away. The runtime cannot tell
	// those apart and does not try — from the turn's side they are one
	// fact, that the reader is gone.
	//
	// It is deliberately NOT resumable. A turn nobody is waiting for should
	// not offer to finish itself, and the user who stopped it asked for the
	// opposite. See app.ResumableFinish.
	FinishAborted FinishReason = "aborted"
	// FinishDeadline is a CLOCK stopping the turn: some deadline on the
	// request's context expired while the turn was still working.
	//
	// ── Why it is its own reason ───────────────────────────────────
	// Because for most of this product's life it was recorded as
	// `aborted`, and that is a statement about the user. R1 found 14 turns
	// in the live database that a 30-second router deadline killed while
	// they were answering normally; every one of them was filed as though
	// a person had changed their mind, and the interface said so. The
	// runtime has `context.DeadlineExceeded` in its hand at the moment it
	// decides, and throwing it away was the whole defect.
	//
	// ── It should be rare, and must still be right ─────────────────
	// The streaming turn routes no longer inherit the generic request
	// deadline (see httpserver.WithoutRequestDeadline), so nothing in the
	// ordinary path produces this any more. That is the point: it is the
	// label for a failure that should not happen, kept so that the failure
	// cannot come back wearing somebody else's name.
	//
	// Unlike FinishAborted it IS resumable: nobody chose it, the work may
	// be half done, and the turn is worth finishing.
	FinishDeadline FinishReason = "deadline"
	FinishError    FinishReason = "error"
	// FinishToolCalls is the provider's own reason for stopping to ask for a
	// tool. It is a mid-turn state, never a stored one: the loop reads it,
	// runs the tools and calls the provider again, so a turn that ends this
	// way ended for one of the reasons below instead.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishToolRoundLimit is ours: the turn asked for tools more times than
	// one turn is allowed to. See app/send.go maxToolRounds.
	FinishToolRoundLimit FinishReason = "tool_round_limit"
)

// UsageSource says how much a turn's token counts can be trusted.
//
// It exists because a token count of zero is ambiguous on its own, and the
// ambiguity is dangerous: read as "this turn was free" it under-reports a
// statement and, later, lets a budget through that should have held. This
// type is the authority on what the numbers mean; the numbers are not.
type UsageSource string

const (
	// UsageUnknown: nothing is known about what this turn consumed. The
	// counts stored beside it are zero only because the column cannot be
	// null. Never read them as a measurement.
	UsageUnknown UsageSource = "unknown"
	// UsageProvider: the gateway reported usage. Exact.
	UsageProvider UsageSource = "provider"
	// UsageEstimated: the gateway reported none, but the request did reach
	// the model, so the counts are EstimateTokens over what was sent and
	// what came back. Approximate, and labelled everywhere it is shown.
	UsageEstimated UsageSource = "estimated"
)

func (s UsageSource) Valid() bool {
	return s == UsageUnknown || s == UsageProvider || s == UsageEstimated
}

// OrUnknown normalises the zero value on the way to storage. A row written
// without an explicit source — a user turn, which is recorded before any
// model runs — carries no measurement, and "unknown" is the honest name for
// that. It keeps the column free of a fourth, undocumented state.
func (s UsageSource) OrUnknown() UsageSource {
	if s == "" {
		return UsageUnknown
	}
	return s
}
